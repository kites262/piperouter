package metrics

import (
	"sync"
	"testing"
	"time"
)

func TestObserveStatusClassesAndErrorRule(t *testing.T) {
	tests := []struct {
		name        string
		status      int
		upstreamErr bool
		want2xx     uint64
		want3xx     uint64
		want4xx     uint64
		want5xx     uint64
		wantUpErrs  uint64
		wantErrors  uint64 // global error_requests
	}{
		{name: "200 ok", status: 200, want2xx: 1},
		{name: "204 ok", status: 204, want2xx: 1},
		{name: "301 redirect", status: 301, want3xx: 1},
		{name: "404 client error is not an error request", status: 404, want4xx: 1},
		{name: "500 counts as error", status: 500, want5xx: 1, wantErrors: 1},
		{name: "599 counts as error", status: 599, want5xx: 1, wantErrors: 1},
		{name: "502 with upstream err counts error once", status: 502, upstreamErr: true, want5xx: 1, wantUpErrs: 1, wantErrors: 1},
		{name: "200 with upstream err still an error", status: 200, upstreamErr: true, want2xx: 1, wantUpErrs: 1, wantErrors: 1},
		{name: "101 has no class bucket", status: 101},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := NewRegistry()
			r.SetRoutes([]string{"api"})
			r.Observe("api", tt.status, tt.upstreamErr, 3*time.Millisecond)

			rs, ok := r.RouteSnapshot("api")
			if !ok {
				t.Fatal("RouteSnapshot(api) not found")
			}
			if rs.Total != 1 {
				t.Errorf("Total = %d, want 1", rs.Total)
			}
			if rs.Status2xx != tt.want2xx || rs.Status3xx != tt.want3xx ||
				rs.Status4xx != tt.want4xx || rs.Status5xx != tt.want5xx {
				t.Errorf("class buckets = %d/%d/%d/%d, want %d/%d/%d/%d",
					rs.Status2xx, rs.Status3xx, rs.Status4xx, rs.Status5xx,
					tt.want2xx, tt.want3xx, tt.want4xx, tt.want5xx)
			}
			if rs.UpstreamErrors != tt.wantUpErrs {
				t.Errorf("UpstreamErrors = %d, want %d", rs.UpstreamErrors, tt.wantUpErrs)
			}
			if rs.Latency.Count != 1 {
				t.Errorf("route Latency.Count = %d, want 1", rs.Latency.Count)
			}

			snap := r.Snapshot()
			if snap.TotalRequests != 1 {
				t.Errorf("TotalRequests = %d, want 1", snap.TotalRequests)
			}
			if snap.ErrorRequests != tt.wantErrors {
				t.Errorf("ErrorRequests = %d, want %d", snap.ErrorRequests, tt.wantErrors)
			}
			if snap.Latency.Count != 1 {
				t.Errorf("global Latency.Count = %d, want 1", snap.Latency.Count)
			}
		})
	}
}

func TestObserveUnknownRouteDropped(t *testing.T) {
	r := NewRegistry()
	r.SetRoutes([]string{"api"})

	r.Observe("ghost", 200, false, time.Millisecond)
	r.Observe("", 404, false, time.Millisecond) // unmatched request

	if _, ok := r.RouteSnapshot("ghost"); ok {
		t.Error("RouteSnapshot(ghost) exists; unknown route must never be auto-created")
	}
	snap := r.Snapshot()
	if snap.TotalRequests != 2 {
		t.Errorf("TotalRequests = %d, want 2 (global counters still update)", snap.TotalRequests)
	}
	if len(snap.Routes) != 1 || snap.Routes[0].Name != "api" {
		t.Fatalf("Routes = %+v, want only [api]", snap.Routes)
	}
	if snap.Routes[0].Total != 0 {
		t.Errorf("api.Total = %d, want 0", snap.Routes[0].Total)
	}
}

func TestSetRoutesKeepsSurvivorsDropsRemoved(t *testing.T) {
	r := NewRegistry()
	r.SetRoutes([]string{"a", "b"})
	r.Observe("a", 200, false, time.Millisecond)
	r.Observe("a", 200, false, time.Millisecond)
	r.Observe("b", 500, false, time.Millisecond)

	r.SetRoutes([]string{"a", "c"})

	a, ok := r.RouteSnapshot("a")
	if !ok || a.Total != 2 || a.Status2xx != 2 {
		t.Errorf("survivor a = %+v (ok=%v), want Total=2 Status2xx=2", a, ok)
	}
	if _, ok := r.RouteSnapshot("b"); ok {
		t.Error("removed route b still present after SetRoutes")
	}
	c, ok := r.RouteSnapshot("c")
	if !ok || c.Total != 0 || c.LastRequestAt != nil {
		t.Errorf("new route c = %+v (ok=%v), want zeroed counters", c, ok)
	}
	if snap := r.Snapshot(); snap.RouteCount != 2 {
		t.Errorf("RouteCount = %d, want 2", snap.RouteCount)
	}
}

func TestGaugesAndMarkStream(t *testing.T) {
	r := NewRegistry()
	r.SetRoutes([]string{"a"})

	h1 := r.IncActive("a", StreamNone)      // plain request, later upgrades to SSE
	h2 := r.IncActive("a", StreamWebSocket) // websocket from the start
	h3 := r.IncActive("", StreamNone)       // unmatched request
	r.MarkStream("a", StreamSSE)            // first request turned out to stream

	snap := r.Snapshot()
	if snap.ActiveRequests != 3 {
		t.Errorf("ActiveRequests = %d, want 3", snap.ActiveRequests)
	}
	if snap.ActiveWebSockets != 1 {
		t.Errorf("ActiveWebSockets = %d, want 1", snap.ActiveWebSockets)
	}
	if snap.ActiveSSE != 1 {
		t.Errorf("ActiveSSE = %d, want 1", snap.ActiveSSE)
	}
	if a, _ := r.RouteSnapshot("a"); a.Active != 2 {
		t.Errorf("route a Active = %d, want 2", a.Active)
	}

	// Callers pass the FINAL kind to Done.
	h1.Done(StreamSSE)
	h2.Done(StreamWebSocket)
	h3.Done(StreamNone)

	snap = r.Snapshot()
	if snap.ActiveRequests != 0 || snap.ActiveWebSockets != 0 || snap.ActiveSSE != 0 {
		t.Errorf("gauges after balanced dec = %d/%d/%d, want 0/0/0",
			snap.ActiveRequests, snap.ActiveWebSockets, snap.ActiveSSE)
	}
	if a, _ := r.RouteSnapshot("a"); a.Active != 0 {
		t.Errorf("route a Active = %d, want 0", a.Active)
	}
}

// TestActiveHandleSurvivesRouteReconfig covers the disable/re-enable race:
// a long-lived request's Done must decrement the SAME series IncActive
// touched, even after the route was dropped and re-created by config swaps,
// so a subsequently active request still reports Active correctly.
func TestActiveHandleSurvivesRouteReconfig(t *testing.T) {
	r := NewRegistry()
	r.SetRoutes([]string{"a"})

	h := r.IncActive("a", StreamNone) // long-lived stream starts on route a
	r.SetRoutes([]string{})           // operator disables route a
	r.SetRoutes([]string{"a"})        // route a re-enabled → fresh series

	h.Done(StreamNone) // old stream ends; must not skew the fresh series

	if a, ok := r.RouteSnapshot("a"); !ok || a.Active != 0 {
		t.Fatalf("route a Active = %d (ok=%v), want 0 (no skew)", a.Active, ok)
	}
	// A genuinely new request on the re-created series must read as active.
	h2 := r.IncActive("a", StreamNone)
	if a, _ := r.RouteSnapshot("a"); a.Active != 1 {
		t.Errorf("route a Active = %d, want 1", a.Active)
	}
	h2.Done(StreamNone)
	if a, _ := r.RouteSnapshot("a"); a.Active != 0 {
		t.Errorf("route a Active after done = %d, want 0", a.Active)
	}
}

func TestMarkStreamOnlyIncrementsStreamGauge(t *testing.T) {
	r := NewRegistry()
	r.SetRoutes([]string{"a"})
	h := r.IncActive("a", StreamNone)
	r.MarkStream("a", StreamSSE)

	snap := r.Snapshot()
	if snap.ActiveRequests != 1 {
		t.Errorf("ActiveRequests = %d, want 1 (MarkStream must not touch active)", snap.ActiveRequests)
	}
	if snap.ActiveSSE != 1 {
		t.Errorf("ActiveSSE = %d, want 1", snap.ActiveSSE)
	}
	h.Done(StreamSSE)
}

func TestSnapshotSortedRoutesAndUptime(t *testing.T) {
	r := NewRegistry()
	r.SetRoutes([]string{"zeta", "alpha", "mid"})
	time.Sleep(2 * time.Millisecond)

	snap := r.Snapshot()
	if snap.StartedAt.IsZero() {
		t.Error("StartedAt is zero")
	}
	if snap.UptimeSeconds <= 0 {
		t.Errorf("UptimeSeconds = %v, want > 0", snap.UptimeSeconds)
	}
	want := []string{"alpha", "mid", "zeta"}
	if len(snap.Routes) != len(want) {
		t.Fatalf("len(Routes) = %d, want %d", len(snap.Routes), len(want))
	}
	for i, name := range want {
		if snap.Routes[i].Name != name {
			t.Errorf("Routes[%d].Name = %q, want %q (sorted by name)", i, snap.Routes[i].Name, name)
		}
	}
	if snap.RouteCount != 3 {
		t.Errorf("RouteCount = %d, want 3", snap.RouteCount)
	}
}

func TestLastRequestAt(t *testing.T) {
	r := NewRegistry()
	r.SetRoutes([]string{"a"})

	rs, _ := r.RouteSnapshot("a")
	if rs.LastRequestAt != nil {
		t.Fatalf("LastRequestAt = %v before any request, want nil", rs.LastRequestAt)
	}

	before := time.Now()
	r.Observe("a", 200, false, time.Millisecond)
	after := time.Now()

	rs, _ = r.RouteSnapshot("a")
	if rs.LastRequestAt == nil {
		t.Fatal("LastRequestAt = nil after a request, want non-nil")
	}
	if rs.LastRequestAt.Before(before) || rs.LastRequestAt.After(after) {
		t.Errorf("LastRequestAt = %v, want within [%v, %v]", rs.LastRequestAt, before, after)
	}
}

func TestSetTransportCount(t *testing.T) {
	r := NewRegistry()
	if got := r.Snapshot().TransportCount; got != 0 {
		t.Errorf("initial TransportCount = %d, want 0", got)
	}
	r.SetTransportCount(4)
	if got := r.Snapshot().TransportCount; got != 4 {
		t.Errorf("TransportCount = %d, want 4", got)
	}
	r.SetTransportCount(1)
	if got := r.Snapshot().TransportCount; got != 1 {
		t.Errorf("TransportCount = %d, want 1", got)
	}
}

func TestRegistryConcurrent(t *testing.T) {
	const workers = 8
	const iterations = 200

	r := NewRegistry()
	r.SetRoutes([]string{"a", "b"})

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				h := r.IncActive("a", StreamNone)
				r.MarkStream("a", StreamSSE)
				r.Observe("a", 200, false, 3*time.Millisecond)
				h.Done(StreamSSE)
			}
		}()
	}
	// Concurrent readers and label swaps (always keeping "a").
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			_ = r.Snapshot()
			_, _ = r.RouteSnapshot("a")
			r.SetRoutes([]string{"a", "b"})
		}
	}()
	wg.Wait()

	const total = workers * iterations
	snap := r.Snapshot()
	if snap.TotalRequests != total {
		t.Errorf("TotalRequests = %d, want %d", snap.TotalRequests, total)
	}
	if snap.ActiveRequests != 0 || snap.ActiveSSE != 0 || snap.ActiveWebSockets != 0 {
		t.Errorf("gauges = %d/%d/%d, want 0/0/0",
			snap.ActiveRequests, snap.ActiveSSE, snap.ActiveWebSockets)
	}
	a, ok := r.RouteSnapshot("a")
	if !ok || a.Total != total {
		t.Errorf("route a Total = %d (ok=%v), want %d", a.Total, ok, total)
	}
	if a.Latency.Count != total {
		t.Errorf("route a Latency.Count = %d, want %d", a.Latency.Count, total)
	}
}
