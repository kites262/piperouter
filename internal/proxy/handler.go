// Package proxy implements the PipeRouter data plane (PRD §9, §10, §20.1):
// match → rewrite → forward with full streaming (no body buffering ever),
// SSE awareness, WebSocket tunneling, transparent header handling and
// bounded error mapping. A panic in a single request never brings the
// process down (§22.3).
package proxy

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/kites262/piperouter/internal/logging"
	"github.com/kites262/piperouter/internal/metrics"
	"github.com/kites262/piperouter/internal/runtime"
)

// SnapshotProvider yields the current immutable runtime snapshot. Each
// request captures exactly one snapshot and uses it for its whole lifetime
// (PRD §12.2).
type SnapshotProvider interface {
	Current() *runtime.Snapshot
}

// Handler is the data-plane HTTP handler. It also exposes graceful
// draining of hijacked WebSocket tunnels, which the net/http server does
// not track (PRD §22.3).
type Handler interface {
	http.Handler
	// DrainWebSockets waits for active WebSocket tunnels to close on their
	// own, up to ctx; when ctx expires it force-closes the survivors.
	DrainWebSockets(ctx context.Context)
}

// NewHandler builds the data-plane HTTP handler.
func NewHandler(sp SnapshotProvider, reg *metrics.Registry, ring *logging.Ring, logger *slog.Logger) Handler {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &handler{sp: sp, reg: reg, ring: ring, logger: logger, tunnels: map[uint64]func(){}}
}

type handler struct {
	sp     SnapshotProvider
	reg    *metrics.Registry
	ring   *logging.Ring
	logger *slog.Logger

	// Active WebSocket tunnels, tracked so shutdown can drain then force
	// close them (they are hijacked, so net/http.Server cannot). tunnelWG
	// counts live tunnels; tunnels maps an id to its force-close func.
	drainMu      sync.Mutex
	tunnels      map[uint64]func()
	nextTunnelID uint64
	tunnelWG     sync.WaitGroup
}

// registerTunnel records a live tunnel and returns an unregister func. force
// is called to tear the tunnel down when a drain budget is exceeded.
func (h *handler) registerTunnel(force func()) func() {
	h.drainMu.Lock()
	id := h.nextTunnelID
	h.nextTunnelID++
	h.tunnels[id] = force
	h.drainMu.Unlock()
	h.tunnelWG.Add(1)
	return func() {
		h.drainMu.Lock()
		delete(h.tunnels, id)
		h.drainMu.Unlock()
		h.tunnelWG.Done()
	}
}

// DrainWebSockets waits for tunnels to close within ctx, then force-closes
// any that remain so the process can exit (PRD §22.3).
func (h *handler) DrainWebSockets(ctx context.Context) {
	done := make(chan struct{})
	go func() {
		h.tunnelWG.Wait()
		close(done)
	}()
	select {
	case <-done:
		return
	case <-ctx.Done():
	}
	h.drainMu.Lock()
	forces := make([]func(), 0, len(h.tunnels))
	for _, f := range h.tunnels {
		forces = append(forces, f)
	}
	h.drainMu.Unlock()
	if len(forces) > 0 {
		h.logger.Warn("proxy: forcing close of active WebSocket tunnels at shutdown",
			slog.Int("count", len(forces)))
	}
	for _, f := range forces {
		f()
	}
	<-done
}

// requestState accumulates per-request accounting. It is only touched from
// the request goroutine (ReverseProxy calls Rewrite/ModifyResponse/
// ErrorHandler synchronously).
type requestState struct {
	routeName     string
	transportName string
	path          string                // escaped path, never includes the query (§14.3)
	streaming     metrics.StreamKind    // final stream kind, also used by handle.Done
	errClass      string                // classification code for the access entry
	handle        *metrics.ActiveHandle // non-nil once the active gauges were incremented
	skipObserve   bool                  // client canceled: no Observe (§9.6)
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	rw := newResponseRecorder(w)
	st := &requestState{path: r.URL.EscapedPath()}
	defer h.finalize(rw, r, st, start)

	snap := h.sp.Current()
	if snap == nil || snap.Config == nil || snap.Table == nil || snap.Pool == nil {
		h.logger.Error("proxy: no usable runtime snapshot")
		st.errClass = errInternal
		writeJSONError(rw, http.StatusInternalServerError, errInternal)
		return
	}

	route := snap.Table.Match(st.path)
	if route == nil {
		// Unmatched requests carry no route label; only global counters
		// and the access log record them (bounded labels §22.2).
		st.handle = h.reg.IncActive("", metrics.StreamNone)
		st.errClass = errRouteNotFound
		// Stock Go 404, not the JSON envelope: unmatched paths are mostly
		// scanner probes, and a distinctive body would fingerprint the
		// gateway. errClass stays internal (access log and metrics only).
		http.NotFound(rw, r)
		return
	}
	st.routeName = route.Name
	st.transportName = route.TransportName

	// Static routes serve one local file — no transport, no WebSocket, no rewrite.
	if route.IsStatic() {
		st.handle = h.reg.IncActive(route.Name, metrics.StreamNone)
		h.serveStatic(rw, r, route, st)
		return
	}

	entry, ok := snap.Pool.Get(route.TransportName)
	if !ok {
		// config.Validate + runtime.Manager guarantee this cannot happen;
		// log loudly if the invariant ever breaks.
		st.handle = h.reg.IncActive(route.Name, metrics.StreamNone)
		h.logger.Error("proxy: transport missing from pool (config invariant broken)",
			slog.String("route", route.Name),
			slog.String("transport", route.TransportName))
		st.errClass = errUpstreamFailed
		writeJSONError(rw, http.StatusBadGateway, errUpstreamFailed)
		return
	}

	if isWebSocketUpgrade(r) {
		st.streaming = metrics.StreamWebSocket
		st.handle = h.reg.IncActive(route.Name, metrics.StreamWebSocket)
		h.serveWebSocket(rw, r, snap.Config.Network, route, entry, st)
		return
	}

	st.handle = h.reg.IncActive(route.Name, metrics.StreamNone)
	h.serveReverse(rw, r, route, entry, st)
}

// finalize is the single deferred exit point: it recovers panics (§22.3),
// balances the active gauges, records metrics and always emits an access
// entry — also for 404/502/504/panics (§13, §14).
func (h *handler) finalize(rw *responseRecorder, r *http.Request, st *requestState, start time.Time) {
	rec := recover()
	abort := false
	if rec != nil {
		if err, ok := rec.(error); ok && errors.Is(err, http.ErrAbortHandler) {
			// ReverseProxy aborts the response when the client goes away
			// mid-stream; account for it, then let the server tear the
			// connection down (it recovers ErrAbortHandler silently).
			st.errClass = errClientCanceled
			st.skipObserve = true
			abort = true
		} else {
			h.logger.Error("proxy: panic recovered",
				slog.Any("panic", rec),
				slog.String("method", r.Method),
				slog.String("path", st.path),
				slog.String("route", st.routeName),
				slog.String("stack", string(debug.Stack())))
			if !rw.wroteHeader {
				writeJSONError(rw, http.StatusInternalServerError, errInternal)
			}
			st.errClass = errInternal
		}
	}

	duration := time.Since(start)
	if st.handle != nil {
		st.handle.Done(st.streaming)
	}
	if !st.skipObserve {
		upstreamErr := st.errClass == errUpstreamFailed || st.errClass == errUpstreamTimeout
		h.reg.Observe(st.routeName, rw.status, upstreamErr, duration)
	}

	entry := logging.AccessEntry{
		Time:       start,
		Route:      st.routeName,
		Method:     r.Method,
		Path:       truncatePath(st.path), // never the query string (§14.3); capped so the ring stays bounded (§22.2)
		Status:     rw.status,
		DurationMs: float64(duration) / float64(time.Millisecond),
		Transport:  st.transportName,
		Streaming:  string(st.streaming),
		Error:      st.errClass,
	}
	if h.ring != nil && h.ring.Enabled() {
		// Ring only — LogAccess never emits header values to stdout. Not
		// captured at all when recent_logs disables the ring.
		entry.ForwardHeaders = captureForwardHeaders(r.Header)
		h.ring.Add(entry)
	}
	logging.LogAccess(h.logger, entry)

	if abort {
		panic(rec)
	}
}

// maxLoggedPath caps the request path stored in the access ring and written
// to the structured log. Without it an unauthenticated client could pin
// unbounded RSS by sending huge paths that the ring retains verbatim
// (PRD §1.3 bounded state, §22.2).
const maxLoggedPath = 2048

// truncatePath bounds a path for logging, marking truncation.
func truncatePath(p string) string {
	if len(p) <= maxLoggedPath {
		return p
	}
	return p[:maxLoggedPath] + "…(truncated)"
}

// maxLoggedHeaderValue caps each captured forward-header value so the ring
// stays bounded against absurd client-sent values (§22.2).
const maxLoggedHeaderValue = 256

// captureForwardHeaders extracts the inbound proxy-metadata headers
// (forwardHeaders) for the access ring, so the WebUI can show the original
// client even when strip_forward_headers removes them from the outbound
// request. Only headers the client actually sent are listed, in the stable
// forwardHeaders order; multiple values are comma-joined. Returns nil when
// none are present. At most one allocation: with a tiny live heap the GC
// cadence tracks allocation volume, so the hot path stays lean.
func captureForwardHeaders(h http.Header) []logging.ForwardHeader {
	var out []logging.ForwardHeader
	for _, k := range forwardHeaders {
		vals := h[k]
		if len(vals) == 0 {
			continue
		}
		if out == nil {
			out = make([]logging.ForwardHeader, 0, len(forwardHeaders))
		}
		var v string
		if len(vals) == 1 {
			// Copy, never reference: the ring retains entries long after
			// the request, and keeping the original string would pin the
			// request's whole header-parse slab (§22.2).
			v = strings.Clone(vals[0])
		} else {
			v = strings.Join(vals, ", ")
		}
		if len(v) > maxLoggedHeaderValue {
			v = v[:maxLoggedHeaderValue] + "…(truncated)"
		}
		out = append(out, logging.ForwardHeader{Name: k, Value: v})
	}
	return out
}

// isWebSocketUpgrade reports whether the request asks for a WebSocket
// upgrade: Connection contains the token "Upgrade" and Upgrade equals
// "websocket" (both case-insensitive), per RFC 9110 §7.8 / RFC 6455 §4.1.
func isWebSocketUpgrade(r *http.Request) bool {
	return headerValuesContainToken(r.Header["Connection"], "Upgrade") &&
		strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
}

// headerValuesContainToken reports whether any of the comma-separated
// header values contains token, compared case-insensitively (the
// httpguts.HeaderValuesContainsToken check, implemented locally).
func headerValuesContainToken(values []string, token string) bool {
	for _, v := range values {
		for _, t := range strings.Split(v, ",") {
			if strings.EqualFold(strings.TrimSpace(t), token) {
				return true
			}
		}
	}
	return false
}
