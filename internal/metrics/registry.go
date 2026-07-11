// Package metrics implements in-memory, bounded, atomic request metrics
// (PRD §13). All state lives in memory and resets on restart; labels are
// limited to configured route names — arbitrary request paths can never
// create new series (§22.2).
package metrics

import (
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// StreamKind classifies a streaming request for the active-stream gauges.
type StreamKind string

// Stream kinds tracked by dedicated gauges.
const (
	StreamNone      StreamKind = ""
	StreamSSE       StreamKind = "sse"
	StreamWebSocket StreamKind = "websocket"
)

// routeMetrics holds the per-route counters. Everything is atomic so the
// hot path never takes the registry lock for longer than a map read.
type routeMetrics struct {
	total          atomic.Uint64
	status2xx      atomic.Uint64
	status3xx      atomic.Uint64
	status4xx      atomic.Uint64
	status5xx      atomic.Uint64
	upstreamErrors atomic.Uint64
	active         atomic.Int64
	lastRequestAt  atomic.Int64 // unix nanoseconds; 0 = never
	latency        histogram
}

func (m *routeMetrics) snapshot(name string) RouteSnapshot {
	rs := RouteSnapshot{
		Name:           name,
		Total:          m.total.Load(),
		Status2xx:      m.status2xx.Load(),
		Status3xx:      m.status3xx.Load(),
		Status4xx:      m.status4xx.Load(),
		Status5xx:      m.status5xx.Load(),
		UpstreamErrors: m.upstreamErrors.Load(),
		Active:         clampUint(m.active.Load()),
		Latency:        m.latency.summary(),
	}
	if ns := m.lastRequestAt.Load(); ns != 0 {
		t := time.Unix(0, ns)
		rs.LastRequestAt = &t
	}
	return rs
}

// Registry aggregates global and per-route metrics. Counter updates use
// sync/atomic; the route map is guarded by an RWMutex and is only mutated
// or replaced inside SetRoutes (§13.4).
type Registry struct {
	startedAt time.Time

	totalRequests    atomic.Uint64
	errorRequests    atomic.Uint64
	activeRequests   atomic.Int64
	activeWebSockets atomic.Int64
	activeSSE        atomic.Int64
	transportCount   atomic.Int64
	latency          histogram

	mu     sync.RWMutex
	routes map[string]*routeMetrics

	// history is app-lifetime like the registry itself: it survives config
	// reloads (SetRoutes never touches it) and resets on process restart.
	history *history
}

// NewRegistry returns an empty registry; StartedAt is captured now.
func NewRegistry() *Registry {
	now := time.Now()
	return &Registry{
		startedAt: now,
		routes:    make(map[string]*routeMetrics),
		history:   newHistory(now),
	}
}

// SetRoutes swaps the label set at config-swap time: counters for surviving
// names are kept, removed names are dropped, new names start at zero.
// Labels are bounded — this is the only place series are created (§22.2).
func (r *Registry) SetRoutes(names []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	next := make(map[string]*routeMetrics, len(names))
	for _, name := range names {
		if existing, ok := r.routes[name]; ok {
			next[name] = existing
		} else if _, ok := next[name]; !ok {
			next[name] = &routeMetrics{}
		}
	}
	r.routes = next
}

// SetTransportCount records the current number of transports (built-in
// "direct" included, per the caller's convention).
func (r *Registry) SetTransportCount(n int) {
	r.transportCount.Store(int64(n))
}

// route returns the per-route counters, or nil for unknown names.
func (r *Registry) route(name string) *routeMetrics {
	r.mu.RLock()
	rm := r.routes[name]
	r.mu.RUnlock()
	return rm
}

// ActiveHandle balances the active gauges for one in-flight request. It
// captures the exact per-route series resolved at IncActive time so a config
// swap between start and finish (e.g. a route disabled then re-enabled while
// a long stream runs) can never skew a freshly-created series (PRD §13.3).
type ActiveHandle struct {
	reg *Registry
	rm  *routeMetrics // nil for unmatched requests
}

// IncActive marks a request as in flight and returns a handle to balance it.
// route may be "" (unmatched); kind != StreamNone also increments the
// matching stream gauge. Pair every call with handle.Done.
func (r *Registry) IncActive(route string, kind StreamKind) *ActiveHandle {
	r.activeRequests.Add(1)
	r.bumpStream(kind, 1)
	rm := r.route(route)
	if rm != nil {
		rm.active.Add(1)
	}
	return &ActiveHandle{reg: r, rm: rm}
}

// Done marks the request finished. The caller passes the FINAL stream kind:
// if the request was upgraded mid-flight via MarkStream, that same kind must
// be passed so the stream gauge balances. The per-route active gauge is
// decremented on the SAME series IncActive incremented, even if the route
// was reconfigured meanwhile.
func (h *ActiveHandle) Done(kind StreamKind) {
	h.reg.activeRequests.Add(-1)
	h.reg.bumpStream(kind, -1)
	if h.rm != nil {
		h.rm.active.Add(-1)
	}
}

// MarkStream records that an already-active request turned out to stream
// (e.g. SSE detected from the response Content-Type). It only increments
// the stream gauge; the caller must pass the same kind to DecActive.
func (r *Registry) MarkStream(route string, kind StreamKind) {
	_ = route // stream gauges are global; route kept for interface stability
	r.bumpStream(kind, 1)
}

func (r *Registry) bumpStream(kind StreamKind, delta int64) {
	switch kind {
	case StreamSSE:
		r.activeSSE.Add(delta)
	case StreamWebSocket:
		r.activeWebSockets.Add(delta)
	}
}

// Observe records one completed request. Global counters always update;
// per-route counters update only when route is a configured name — unknown
// or removed names are dropped silently, never auto-created (§22.2).
// A request is an error when status >= 500 or the upstream failed.
func (r *Registry) Observe(route string, status int, upstreamErr bool, latency time.Duration) {
	now := time.Now()
	ms := float64(latency) / float64(time.Millisecond)
	isErr := status >= 500 || upstreamErr
	r.totalRequests.Add(1)
	if isErr {
		r.errorRequests.Add(1)
	}
	r.latency.observe(ms)
	r.history.observe(now, isErr)

	rm := r.route(route)
	if rm == nil {
		return
	}
	rm.total.Add(1)
	switch status / 100 {
	case 2:
		rm.status2xx.Add(1)
	case 3:
		rm.status3xx.Add(1)
	case 4:
		rm.status4xx.Add(1)
	case 5:
		rm.status5xx.Add(1)
	}
	if upstreamErr {
		rm.upstreamErrors.Add(1)
	}
	rm.latency.observe(ms)
	rm.lastRequestAt.Store(now.UnixNano())
}

// History returns the rolling 48h success/error series.
func (r *Registry) History() HistorySnapshot {
	return r.history.snapshot(time.Now())
}

// Snapshot returns a consistent-enough point-in-time view. It is cheap:
// one RLock over the route map plus atomic loads; Routes sorted by name.
func (r *Registry) Snapshot() Snapshot {
	snap := Snapshot{
		StartedAt:        r.startedAt,
		UptimeSeconds:    time.Since(r.startedAt).Seconds(),
		TotalRequests:    r.totalRequests.Load(),
		ErrorRequests:    r.errorRequests.Load(),
		ActiveRequests:   clampUint(r.activeRequests.Load()),
		ActiveWebSockets: clampUint(r.activeWebSockets.Load()),
		ActiveSSE:        clampUint(r.activeSSE.Load()),
		TransportCount:   int(r.transportCount.Load()),
		Latency:          r.latency.summary(),
	}

	r.mu.RLock()
	names := make([]string, 0, len(r.routes))
	for name := range r.routes {
		names = append(names, name)
	}
	sort.Strings(names)
	snap.RouteCount = len(names)
	snap.Routes = make([]RouteSnapshot, 0, len(names))
	for _, name := range names {
		snap.Routes = append(snap.Routes, r.routes[name].snapshot(name))
	}
	r.mu.RUnlock()
	return snap
}

// RouteSnapshot returns the snapshot for a single configured route.
func (r *Registry) RouteSnapshot(name string) (RouteSnapshot, bool) {
	rm := r.route(name)
	if rm == nil {
		return RouteSnapshot{}, false
	}
	return rm.snapshot(name), true
}

func clampUint(v int64) uint64 {
	if v < 0 {
		return 0
	}
	return uint64(v)
}
