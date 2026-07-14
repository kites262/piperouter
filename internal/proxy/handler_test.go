package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kites262/piperouter/internal/config"
	"github.com/kites262/piperouter/internal/logging"
	"github.com/kites262/piperouter/internal/metrics"
	"github.com/kites262/piperouter/internal/router"
	"github.com/kites262/piperouter/internal/runtime"
	"github.com/kites262/piperouter/internal/transport"
)

// buildSnapshot compiles a runtime snapshot from YAML through the real
// config/router/transport packages (no runtime.Manager involved).
func buildSnapshot(t *testing.T, yamlText string) *runtime.Snapshot {
	t.Helper()
	cfg, err := config.Parse([]byte(yamlText))
	if err != nil {
		t.Fatalf("config.Parse: %v", err)
	}
	if err := config.Validate(cfg, ""); err != nil {
		t.Fatalf("config.Validate: %v", err)
	}
	table, err := router.BuildTable(cfg.Routes, "")
	if err != nil {
		t.Fatalf("router.BuildTable: %v", err)
	}
	pool, err := transport.NewPool(cfg.Transports, cfg.Network)
	if err != nil {
		t.Fatalf("transport.NewPool: %v", err)
	}
	return &runtime.Snapshot{
		Config:   cfg,
		Table:    table,
		Pool:     pool,
		Revision: "sha256:test",
		LoadedAt: time.Now(),
	}
}

type staticProvider struct{ snap *runtime.Snapshot }

func (p *staticProvider) Current() *runtime.Snapshot { return p.snap }

type testProxy struct {
	server *httptest.Server
	reg    *metrics.Registry
	ring   *logging.Ring
}

func newTestProxyFromProvider(t *testing.T, sp SnapshotProvider, routeNames []string) *testProxy {
	t.Helper()
	reg := metrics.NewRegistry()
	reg.SetRoutes(routeNames)
	ring := logging.NewRing(128)
	h := NewHandler(sp, reg, ring, slog.New(slog.DiscardHandler))
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &testProxy{server: srv, reg: reg, ring: ring}
}

func newTestProxy(t *testing.T, snap *runtime.Snapshot) *testProxy {
	t.Helper()
	names := make([]string, 0, snap.Table.Len())
	for _, r := range snap.Table.Routes() {
		names = append(names, r.Name)
	}
	return newTestProxyFromProvider(t, &staticProvider{snap}, names)
}

// waitFor polls cond until it holds or the timeout expires. Access entries
// and gauge resets land shortly AFTER the client sees the response, so
// tests must not assert them synchronously.
func waitFor(t *testing.T, timeout time.Duration, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("condition not met within %v: %s", timeout, msg)
}

// lastEntry returns the newest access entry, waiting for it to appear.
func lastEntry(t *testing.T, ring *logging.Ring, want int) logging.AccessEntry {
	t.Helper()
	waitFor(t, 2*time.Second, func() bool {
		return len(ring.Snapshot(0, "", "")) >= want
	}, fmt.Sprintf("expected %d access entries", want))
	return ring.Snapshot(0, "", "")[0]
}

func decodeErrorBody(t *testing.T, r io.Reader) string {
	t.Helper()
	var body struct {
		Error string `json:"error"`
	}
	if err := json.NewDecoder(r).Decode(&body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	return body.Error
}

// closedAddr reserves a port and releases it so connections get refused.
func closedAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := l.Addr().String()
	l.Close()
	return addr
}

func TestUnmatchedRouteReturnsPlain404(t *testing.T) {
	snap := buildSnapshot(t, fmt.Sprintf(`
version: v0.3
routes:
  - name: api
    prefix: /api
    options:
      target: http://%s
`, closedAddr(t)))
	tp := newTestProxy(t, snap)

	resp, err := http.Get(tp.server.URL + "/nope/deeper")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	// A bare "404", not the JSON envelope: the body must not fingerprint
	// PipeRouter to path scanners.
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Fatalf("Content-Type = %q, want text/plain (bare 404)", ct)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if got := string(body); got != "404" {
		t.Fatalf("body = %q, want exactly %q", got, "404")
	}

	e := lastEntry(t, tp.ring, 1)
	if e.Route != "" || e.Status != 404 || e.Error != "route_not_found" ||
		e.Method != "GET" || e.Path != "/nope/deeper" || e.Streaming != "" {
		t.Fatalf("access entry = %+v", e)
	}
	// Unmatched requests count globally but never per-route (§22.2).
	ms := tp.reg.Snapshot()
	if ms.TotalRequests != 1 {
		t.Fatalf("TotalRequests = %d, want 1", ms.TotalRequests)
	}
	if rs, ok := tp.reg.RouteSnapshot("api"); !ok || rs.Total != 0 {
		t.Fatalf("route api Total = %d (ok=%v), want 0", rs.Total, ok)
	}
	waitFor(t, time.Second, func() bool { return tp.reg.Snapshot().ActiveRequests == 0 },
		"active gauge back to 0")
}

func TestMissingTransportReturns502(t *testing.T) {
	// config.Validate would reject this; build the snapshot by hand to
	// prove the invariant-broken path answers 502 instead of panicking.
	table, err := router.BuildTable([]config.RouteConfig{{
		Name:   "ghost",
		Prefix: "/g",
		Proxy: &config.ProxyOptions{
			Target:    "http://127.0.0.1:9",
			Transport: "no-such-transport",
		},
	}}, "")
	if err != nil {
		t.Fatalf("BuildTable: %v", err)
	}
	cfg := config.Default()
	pool, err := transport.NewPool(nil, cfg.Network)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	snap := &runtime.Snapshot{Config: cfg, Table: table, Pool: pool, Revision: "r", LoadedAt: time.Now()}
	tp := newTestProxy(t, snap)

	resp, err := http.Get(tp.server.URL + "/g/x")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", resp.StatusCode)
	}
	if code := decodeErrorBody(t, resp.Body); code != "upstream_connection_failed" {
		t.Fatalf("error code = %q", code)
	}
}

func TestUpstreamDownReturns502(t *testing.T) {
	snap := buildSnapshot(t, fmt.Sprintf(`
version: v0.3
routes:
  - name: api
    prefix: /api
    options:
      target: http://%s
`, closedAddr(t)))
	tp := newTestProxy(t, snap)

	resp, err := http.Get(tp.server.URL + "/api/x")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", resp.StatusCode)
	}
	if code := decodeErrorBody(t, resp.Body); code != "upstream_connection_failed" {
		t.Fatalf("error code = %q", code)
	}

	e := lastEntry(t, tp.ring, 1)
	if e.Route != "api" || e.Status != 502 || e.Error != "upstream_connection_failed" || e.Transport != "direct" {
		t.Fatalf("access entry = %+v", e)
	}
	waitFor(t, time.Second, func() bool {
		rs, _ := tp.reg.RouteSnapshot("api")
		return rs.Total == 1 && rs.Status5xx == 1 && rs.UpstreamErrors == 1
	}, "route metrics observed 502 upstream error")
}

func TestResponseHeaderTimeoutReturns504(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(2 * time.Second):
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(upstream.Close)

	snap := buildSnapshot(t, fmt.Sprintf(`
version: v0.3
network:
  response_header_timeout: 80ms
routes:
  - name: api
    prefix: /api
    options:
      target: %s
`, upstream.URL))
	tp := newTestProxy(t, snap)

	resp, err := http.Get(tp.server.URL + "/api/slow")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want 504", resp.StatusCode)
	}
	if code := decodeErrorBody(t, resp.Body); code != "upstream_timeout" {
		t.Fatalf("error code = %q", code)
	}
	e := lastEntry(t, tp.ring, 1)
	if e.Status != 504 || e.Error != "upstream_timeout" {
		t.Fatalf("access entry = %+v", e)
	}
}

func TestClientCancelWritesNothingAndSkipsObserve(t *testing.T) {
	entered := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-r.Context().Done() // blocks until the proxy propagates the cancel
	}))
	t.Cleanup(upstream.Close)

	snap := buildSnapshot(t, fmt.Sprintf(`
version: v0.3
routes:
  - name: api
    prefix: /api
    options:
      target: %s
`, upstream.URL))
	tp := newTestProxy(t, snap)

	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, tp.server.URL+"/api/block", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	errCh := make(chan error, 1)
	go func() {
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			resp.Body.Close()
		}
		errCh <- err
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("upstream never saw the request")
	}
	cancel()
	if err := <-errCh; err == nil {
		t.Fatal("client Do succeeded, want cancellation error")
	}

	e := lastEntry(t, tp.ring, 1)
	if e.Status != 0 || e.Error != "client_canceled" || e.Route != "api" {
		t.Fatalf("access entry = %+v", e)
	}
	// Neither success nor upstream error: Observe is skipped entirely.
	if got := tp.reg.Snapshot().TotalRequests; got != 0 {
		t.Fatalf("TotalRequests = %d, want 0", got)
	}
	// The upstream request must be canceled and the request fully retired.
	waitFor(t, 2*time.Second, func() bool { return tp.reg.Snapshot().ActiveRequests == 0 },
		"active gauge back to 0 after cancel")
}

// flakyProvider panics on the first Current() call, then serves a real
// snapshot: the process must survive and keep serving (§22.3).
type flakyProvider struct {
	calls atomic.Int32
	snap  *runtime.Snapshot
}

func (p *flakyProvider) Current() *runtime.Snapshot {
	if p.calls.Add(1) == 1 {
		panic("snapshot provider exploded")
	}
	return p.snap
}

func TestPanicRecoveryKeepsServing(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, "ok")
	}))
	t.Cleanup(upstream.Close)

	snap := buildSnapshot(t, fmt.Sprintf(`
version: v0.3
routes:
  - name: api
    prefix: /api
    options:
      target: %s
`, upstream.URL))
	tp := newTestProxyFromProvider(t, &flakyProvider{snap: snap}, []string{"api"})

	resp, err := http.Get(tp.server.URL + "/api/x")
	if err != nil {
		t.Fatalf("GET #1: %v", err)
	}
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("first status = %d, want 500", resp.StatusCode)
	}
	if code := decodeErrorBody(t, resp.Body); code != "internal_error" {
		t.Fatalf("error code = %q, want internal_error", code)
	}
	resp.Body.Close()

	e := lastEntry(t, tp.ring, 1)
	if e.Status != 500 || e.Error != "internal_error" {
		t.Fatalf("access entry = %+v", e)
	}

	// Process alive: the next request must succeed.
	resp2, err := http.Get(tp.server.URL + "/api/x")
	if err != nil {
		t.Fatalf("GET #2: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("second status = %d, want 200", resp2.StatusCode)
	}
	body, _ := io.ReadAll(resp2.Body)
	if string(body) != "ok" {
		t.Fatalf("second body = %q", body)
	}
}

func TestIsWebSocketUpgrade(t *testing.T) {
	cases := []struct {
		name       string
		connection []string
		upgrade    string
		want       bool
	}{
		{"standard", []string{"Upgrade"}, "websocket", true},
		{"case insensitive", []string{"upgrade"}, "WebSocket", true},
		{"token list", []string{"keep-alive, Upgrade"}, "websocket", true},
		{"multiple values", []string{"keep-alive", "Upgrade"}, "websocket", true},
		{"missing upgrade header", []string{"Upgrade"}, "", false},
		{"wrong upgrade protocol", []string{"Upgrade"}, "h2c", false},
		{"no connection token", []string{"keep-alive"}, "websocket", false},
		{"substring is not a token", []string{"NotUpgrade"}, "websocket", false},
		{"empty", nil, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			for _, v := range tc.connection {
				r.Header.Add("Connection", v)
			}
			if tc.upgrade != "" {
				r.Header.Set("Upgrade", tc.upgrade)
			}
			if got := isWebSocketUpgrade(r); got != tc.want {
				t.Fatalf("isWebSocketUpgrade = %v, want %v", got, tc.want)
			}
		})
	}
}
