// Package integration_test exercises the fully assembled PipeRouter
// application end to end (PRD §23 acceptance cases, §22.1 performance,
// §22.6 testability): the real app is started via app.Start with
// ephemeral 127.0.0.1:0 listeners written into a real config file on
// disk, upstreams run in-process via httptest, and all traffic flows
// through the real proxy and admin listeners.
//
// Only the exported app surface (app.Start / Options / ProxyAddr /
// AdminAddr / Shutdown) and the config package (Load / WriteAtomic /
// Normalize) are used; no internal state is reached into.
package integration_test

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kites262/piperouter/internal/app"
	"github.com/kites262/piperouter/internal/config"
)

// ---------------------------------------------------------------------------
// application harness
// ---------------------------------------------------------------------------

// testApp is one started PipeRouter instance backed by a temp config file.
type testApp struct {
	tb         testing.TB
	App        *app.App
	ConfigPath string
}

// startApp writes cfg to a temp file, starts the real application on
// ephemeral ports and registers a bounded-time shutdown. App.Shutdown is
// idempotent, so tests that need to stop the app early may call it
// themselves.
func startApp(tb testing.TB, cfg *config.Config) *testApp {
	tb.Helper()
	path := filepath.Join(tb.TempDir(), "piperouter.yaml")
	writeConfig(tb, path, cfg)
	a, err := app.Start(context.Background(), app.Options{ConfigPath: path, Version: "integration-test"})
	if err != nil {
		tb.Fatalf("app.Start: %v", err)
	}
	ta := &testApp{tb: tb, App: a, ConfigPath: path}
	tb.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
		defer cancel()
		if err := ta.App.Shutdown(ctx); err != nil {
			tb.Errorf("app.Shutdown: %v", err)
		}
	})
	return ta
}

func (ta *testApp) proxyURL() string { return "http://" + ta.App.ProxyAddr() }
func (ta *testApp) adminURL() string { return "http://" + ta.App.AdminAddr() }

// shutdownApp stops the app immediately (App.Shutdown is idempotent, so
// the cleanup registered by startApp remains harmless afterwards).
func shutdownApp(tb testing.TB, ta *testApp) {
	tb.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	if err := ta.App.Shutdown(ctx); err != nil {
		tb.Fatalf("app.Shutdown: %v", err)
	}
}

// bp is a *bool literal helper for config pointer fields.
func bp(b bool) *bool { return &b }

// baseConfig builds a valid config bound to ephemeral loopback ports.
// log_level "error" keeps test output quiet; the recent-logs ring records
// access entries regardless of the slog level.
func baseConfig(routes ...config.RouteConfig) *config.Config {
	recent := 512
	cfg := &config.Config{Version: config.SupportedVersion}
	cfg.Server.Proxy.Listen = "127.0.0.1:0"
	cfg.Server.Admin.Enabled = bp(true)
	cfg.Server.Admin.Listen = "127.0.0.1:0"
	cfg.Runtime.LogLevel = "error"
	cfg.Runtime.RecentLogs = &recent
	cfg.Routes = routes
	return cfg
}

func route(name, prefix, target string) config.RouteConfig {
	return config.RouteConfig{Name: name, Prefix: prefix, Target: target}
}

func routeVia(name, prefix, target, transportName string) config.RouteConfig {
	r := route(name, prefix, target)
	r.Transport = transportName
	return r
}

// writeConfig normalizes cfg and persists it with the production atomic
// write path (PRD §6.5).
func writeConfig(tb testing.TB, path string, cfg *config.Config) {
	tb.Helper()
	cfg.Normalize()
	if err := config.WriteAtomic(path, cfg); err != nil {
		tb.Fatalf("config.WriteAtomic(%s): %v", path, err)
	}
}

// ---------------------------------------------------------------------------
// HTTP client helpers
// ---------------------------------------------------------------------------

// newClient returns an isolated client whose pooled connections are closed
// at test end (goroutine hygiene for the leak checks).
func newClient(tb testing.TB) *http.Client {
	tb.Helper()
	tr := &http.Transport{MaxIdleConnsPerHost: 256}
	tb.Cleanup(tr.CloseIdleConnections)
	return &http.Client{Transport: tr}
}

// get performs a GET and returns the (closed) response plus its body.
func get(tb testing.TB, client *http.Client, url string) (*http.Response, string) {
	tb.Helper()
	resp, err := client.Get(url)
	if err != nil {
		tb.Fatalf("GET %s: %v", url, err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		tb.Fatalf("GET %s: read body: %v", url, err)
	}
	return resp, string(body)
}

// getJSON performs a GET expecting 200 and decodes the JSON body into out.
func getJSON(tb testing.TB, client *http.Client, url string, out any) {
	tb.Helper()
	resp, body := get(tb, client, url)
	if resp.StatusCode != http.StatusOK {
		tb.Fatalf("GET %s: status %d, body %q", url, resp.StatusCode, body)
	}
	if err := json.Unmarshal([]byte(body), out); err != nil {
		tb.Fatalf("GET %s: decode JSON: %v (body %q)", url, err, body)
	}
}

// postJSON posts a JSON body and returns the (closed) response plus body.
func postJSON(tb testing.TB, client *http.Client, url, body string) (*http.Response, string) {
	tb.Helper()
	resp, err := client.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		tb.Fatalf("POST %s: %v", url, err)
	}
	out, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		tb.Fatalf("POST %s: read body: %v", url, err)
	}
	return resp, string(out)
}

// statusView is the subset of GET /api/v1/status used by the tests.
type statusView struct {
	Config struct {
		Valid     bool   `json:"valid"`
		LastError string `json:"last_error"`
		Revision  string `json:"revision"`
	} `json:"config"`
}

func adminStatus(tb testing.TB, client *http.Client, ta *testApp) statusView {
	tb.Helper()
	var sv statusView
	getJSON(tb, client, ta.adminURL()+"/api/v1/status", &sv)
	return sv
}

// ---------------------------------------------------------------------------
// waiting helpers (deadline-guarded, no long blind sleeps)
// ---------------------------------------------------------------------------

// eventually polls cond every 20ms until it holds or timeout elapses.
func eventually(tb testing.TB, timeout time.Duration, msg string, cond func() bool) {
	tb.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			tb.Fatalf("condition not met within %v: %s", timeout, msg)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// waitClosed fails the test if ch is not closed (or sent to) within d.
func waitClosed(tb testing.TB, ch <-chan struct{}, d time.Duration, msg string) {
	tb.Helper()
	select {
	case <-ch:
	case <-time.After(d):
		tb.Fatalf("timed out after %v: %s", d, msg)
	}
}

// ---------------------------------------------------------------------------
// upstream servers
// ---------------------------------------------------------------------------

// pathEcho starts an upstream answering 200 "<tag>:<path>" so tests can
// tell which upstream served a request and which rewritten path it saw.
func pathEcho(tb testing.TB, tag string) *httptest.Server {
	tb.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s:%s", tag, r.URL.Path)
	}))
	tb.Cleanup(srv.Close)
	return srv
}

// sseUpstream serves paced Server-Sent Events: one "data: event-<i>" frame
// immediately, then one every interval, flushed individually. count < 0
// streams until the request context is canceled. The returned channel is
// closed the first time any request observes cancellation (client-cancel
// propagation, §23.5).
func sseUpstream(tb testing.TB, interval time.Duration, count int) (*httptest.Server, <-chan struct{}) {
	tb.Helper()
	canceled := make(chan struct{})
	var once sync.Once
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		rc := http.NewResponseController(w)
		for i := 0; count < 0 || i < count; i++ {
			if i > 0 {
				select {
				case <-r.Context().Done():
					once.Do(func() { close(canceled) })
					return
				case <-time.After(interval):
				}
			}
			fmt.Fprintf(w, "data: event-%d\n\n", i)
			if err := rc.Flush(); err != nil {
				once.Do(func() { close(canceled) })
				return
			}
		}
	}))
	tb.Cleanup(srv.Close)
	return srv, canceled
}

// sseEvent is one received SSE data frame with its client arrival time.
type sseEvent struct {
	data string
	at   time.Time
}

// openSSE issues a streaming GET and parses "data: ..." frames onto the
// returned channel, which is closed when the stream ends. The error
// channel (buffered) receives the read-loop result exactly once.
func openSSE(ctx context.Context, client *http.Client, url string) (<-chan sseEvent, <-chan error, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Accept", "text/event-stream")
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, nil, fmt.Errorf("SSE GET %s: status %d", url, resp.StatusCode)
	}
	events := make(chan sseEvent, 64)
	errc := make(chan error, 1)
	go func() {
		defer resp.Body.Close()
		defer close(events)
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			if data, ok := strings.CutPrefix(sc.Text(), "data: "); ok {
				events <- sseEvent{data: data, at: time.Now()}
			}
		}
		errc <- sc.Err() // nil on clean EOF
	}()
	return events, errc, nil
}

// recvEvent waits for the next SSE event with a deadline.
func recvEvent(tb testing.TB, ch <-chan sseEvent, d time.Duration, msg string) sseEvent {
	tb.Helper()
	select {
	case ev, ok := <-ch:
		if !ok {
			tb.Fatalf("SSE stream closed while waiting: %s", msg)
		}
		return ev
	case <-time.After(d):
		tb.Fatalf("no SSE event within %v: %s", d, msg)
	}
	return sseEvent{}
}

// ---------------------------------------------------------------------------
// in-test outbound proxies (§23.7, §23.8)
// ---------------------------------------------------------------------------

// deadAddr returns a loopback host:port that refuses connections (the
// listener is bound and immediately closed).
func deadAddr(tb testing.TB) string {
	tb.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		tb.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close()
	return addr
}

// testHTTPProxy is a minimal in-process HTTP proxy handling absolute-form
// plain requests and CONNECT tunneling. Adapted from
// internal/transport/httpproxy_test.go for use through the whole app.
type testHTTPProxy struct {
	srv *httptest.Server
	fwd *http.Transport

	mu       sync.Mutex
	plain    int // non-CONNECT requests seen
	absolute int // ...of which were absolute-form
	connects int // CONNECT requests seen
}

func newTestHTTPProxy(tb testing.TB) *testHTTPProxy {
	tb.Helper()
	p := &testHTTPProxy{fwd: &http.Transport{}}
	p.srv = httptest.NewServer(http.HandlerFunc(p.handle))
	tb.Cleanup(func() {
		p.srv.Close()
		p.fwd.CloseIdleConnections()
	})
	return p
}

func (p *testHTTPProxy) counts() (plain, absolute, connects int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.plain, p.absolute, p.connects
}

func (p *testHTTPProxy) handle(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		p.mu.Lock()
		p.connects++
		p.mu.Unlock()
		p.handleConnect(w, r)
		return
	}
	p.mu.Lock()
	p.plain++
	if r.URL.IsAbs() {
		p.absolute++
	}
	p.mu.Unlock()
	p.handlePlain(w, r)
}

func (p *testHTTPProxy) handlePlain(w http.ResponseWriter, r *http.Request) {
	if !r.URL.IsAbs() {
		http.Error(w, "request target must be absolute-form", http.StatusBadRequest)
		return
	}
	out := r.Clone(r.Context())
	out.RequestURI = ""
	out.Header.Del("Proxy-Connection")
	resp, err := p.fwd.RoundTrip(out)
	if err != nil {
		http.Error(w, "upstream error", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	for k, vv := range resp.Header {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body) //nolint:errcheck // best-effort relay
}

func (p *testHTTPProxy) handleConnect(w http.ResponseWriter, r *http.Request) {
	target, err := net.DialTimeout("tcp", r.Host, 5*time.Second)
	if err != nil {
		http.Error(w, "dial failed", http.StatusBadGateway)
		return
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		target.Close()
		http.Error(w, "hijacking unsupported", http.StatusInternalServerError)
		return
	}
	client, brw, err := hj.Hijack()
	if err != nil {
		target.Close()
		return
	}
	if _, err := client.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
		client.Close()
		target.Close()
		return
	}
	go func() {
		io.Copy(target, brw) //nolint:errcheck // brw drains bytes buffered during hijack
		target.Close()       // unblock the other copy direction
	}()
	io.Copy(client, target) //nolint:errcheck
	client.Close()
	target.Close()
}

// socksRequest records the ATYP and host of one CONNECT as seen by the
// test SOCKS5 server — proves hostname passthrough (§23.8, PRD §11.3).
type socksRequest struct {
	atyp byte
	host string
}

// testSOCKS5Server is a minimal in-process SOCKS5 server: no-auth method
// negotiation, CONNECT command, IPv4/domain/IPv6 address types. Adapted
// from internal/transport/socks5_test.go.
type testSOCKS5Server struct {
	ln net.Listener

	mu       sync.Mutex
	requests []socksRequest
}

func newTestSOCKS5Server(tb testing.TB) *testSOCKS5Server {
	tb.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		tb.Fatalf("listen: %v", err)
	}
	s := &testSOCKS5Server{ln: ln}
	go s.serve()
	tb.Cleanup(func() { ln.Close() })
	return s
}

func (s *testSOCKS5Server) addr() string { return s.ln.Addr().String() }

func (s *testSOCKS5Server) seen() []socksRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]socksRequest, len(s.requests))
	copy(out, s.requests)
	return out
}

func (s *testSOCKS5Server) serve() {
	for {
		c, err := s.ln.Accept()
		if err != nil {
			return
		}
		go s.handle(c)
	}
}

func (s *testSOCKS5Server) handle(c net.Conn) {
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))
	br := bufio.NewReader(c)

	// Method negotiation: VER NMETHODS METHODS... → VER METHOD(no-auth).
	hdr := make([]byte, 2)
	if _, err := io.ReadFull(br, hdr); err != nil || hdr[0] != 0x05 {
		return
	}
	methods := make([]byte, hdr[1])
	if _, err := io.ReadFull(br, methods); err != nil {
		return
	}
	if _, err := c.Write([]byte{0x05, 0x00}); err != nil {
		return
	}

	// Request: VER CMD RSV ATYP DST.ADDR DST.PORT.
	req := make([]byte, 4)
	if _, err := io.ReadFull(br, req); err != nil || req[0] != 0x05 || req[1] != 0x01 {
		return
	}
	var host string
	switch req[3] {
	case 0x01: // IPv4
		b := make([]byte, 4)
		if _, err := io.ReadFull(br, b); err != nil {
			return
		}
		host = net.IP(b).String()
	case 0x03: // domain
		l := make([]byte, 1)
		if _, err := io.ReadFull(br, l); err != nil {
			return
		}
		b := make([]byte, l[0])
		if _, err := io.ReadFull(br, b); err != nil {
			return
		}
		host = string(b)
	case 0x04: // IPv6
		b := make([]byte, 16)
		if _, err := io.ReadFull(br, b); err != nil {
			return
		}
		host = net.IP(b).String()
	default:
		c.Write([]byte{0x05, 0x08, 0x00, 0x01, 0, 0, 0, 0, 0, 0}) //nolint:errcheck // address type not supported
		return
	}
	pb := make([]byte, 2)
	if _, err := io.ReadFull(br, pb); err != nil {
		return
	}
	port := binary.BigEndian.Uint16(pb)

	s.mu.Lock()
	s.requests = append(s.requests, socksRequest{atyp: req[3], host: host})
	s.mu.Unlock()

	target, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(int(port))), 5*time.Second)
	if err != nil {
		c.Write([]byte{0x05, 0x05, 0x00, 0x01, 0, 0, 0, 0, 0, 0}) //nolint:errcheck // connection refused
		return
	}
	if _, err := c.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0}); err != nil {
		target.Close()
		return
	}
	_ = c.SetDeadline(time.Time{})

	go func() {
		io.Copy(target, br) //nolint:errcheck // br drains bytes buffered during the handshake
		target.Close()      // unblock the other copy direction
	}()
	io.Copy(c, target) //nolint:errcheck
	target.Close()
}
