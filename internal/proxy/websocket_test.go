package proxy

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/websocket"
)

// newWSFixture builds an upstream with a websocket echo handler at /echo,
// a custom websocket handler at /custom and a plain HTTP refusal handler
// at /refuse, proxied under /ws.
func newWSFixture(t *testing.T, custom websocket.Handler) *testProxy {
	t.Helper()
	mux := http.NewServeMux()
	mux.Handle("/echo", websocket.Handler(func(ws *websocket.Conn) {
		io.Copy(ws, ws)
	}))
	if custom != nil {
		mux.Handle("/custom", custom)
	}
	mux.HandleFunc("/refuse", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Refused", "yes")
		w.WriteHeader(http.StatusForbidden)
		io.WriteString(w, "denied")
	})
	upstream := httptest.NewServer(mux)
	t.Cleanup(upstream.Close)

	snap := buildSnapshot(t, fmt.Sprintf(`
version: 1
routes:
  - name: ws
    prefix: /ws
    target: %s
`, upstream.URL))
	return newTestProxy(t, snap)
}

func wsDial(t *testing.T, tp *testProxy, path string) *websocket.Conn {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(tp.server.URL, "http") + path
	cfg, err := websocket.NewConfig(wsURL, "http://client.test/")
	if err != nil {
		t.Fatalf("NewConfig: %v", err)
	}
	conn, err := websocket.DialConfig(cfg)
	if err != nil {
		t.Fatalf("DialConfig(%s): %v", wsURL, err)
	}
	return conn
}

func TestWebSocketEchoThroughProxy(t *testing.T) {
	tp := newWSFixture(t, nil)
	conn := wsDial(t, tp, "/ws/echo")
	defer conn.Close()

	// Gauge is up for the whole tunnel lifetime (§10.4, §13.2).
	if got := tp.reg.Snapshot().ActiveWebSockets; got != 1 {
		t.Fatalf("ActiveWebSockets during tunnel = %d, want 1", got)
	}

	for i := range 3 {
		msg := fmt.Sprintf("message-%d", i)
		if err := websocket.Message.Send(conn, msg); err != nil {
			t.Fatalf("send #%d: %v", i, err)
		}
		var got string
		conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		if err := websocket.Message.Receive(conn, &got); err != nil {
			t.Fatalf("receive #%d: %v", i, err)
		}
		if got != msg {
			t.Fatalf("echo #%d = %q, want %q", i, got, msg)
		}
	}
	conn.Close()

	waitFor(t, 2*time.Second, func() bool {
		s := tp.reg.Snapshot()
		return s.ActiveWebSockets == 0 && s.ActiveRequests == 0
	}, "websocket gauge back to 0 after close")

	e := lastEntry(t, tp.ring, 1)
	if e.Status != 101 || e.Streaming != "websocket" || e.Route != "ws" || e.Error != "" {
		t.Fatalf("access entry = %+v", e)
	}
}

func TestWebSocketClientCloseReachesUpstream(t *testing.T) {
	gotMsg := make(chan struct{})
	upstreamErr := make(chan error, 1)
	tp := newWSFixture(t, websocket.Handler(func(ws *websocket.Conn) {
		var s string
		if err := websocket.Message.Receive(ws, &s); err != nil {
			upstreamErr <- err
			return
		}
		close(gotMsg)
		var s2 string
		upstreamErr <- websocket.Message.Receive(ws, &s2) // blocks until the close propagates
	}))

	conn := wsDial(t, tp, "/ws/custom")
	if err := websocket.Message.Send(conn, "hi"); err != nil {
		t.Fatalf("send: %v", err)
	}
	select {
	case <-gotMsg:
	case <-time.After(2 * time.Second):
		t.Fatal("upstream never received the message")
	}

	conn.Close()
	select {
	case err := <-upstreamErr:
		if err == nil {
			t.Fatal("upstream read succeeded after client close, want error")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("client close did not reach the upstream within 2s")
	}
}

func TestWebSocketUpstreamCloseReachesClient(t *testing.T) {
	tp := newWSFixture(t, websocket.Handler(func(ws *websocket.Conn) {
		var s string
		websocket.Message.Receive(ws, &s)
		ws.Close() // upstream hangs up
	}))

	conn := wsDial(t, tp, "/ws/custom")
	defer conn.Close()
	if err := websocket.Message.Send(conn, "bye"); err != nil {
		t.Fatalf("send: %v", err)
	}
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var s string
	if err := websocket.Message.Receive(conn, &s); err == nil {
		t.Fatal("client read succeeded after upstream close, want error")
	} else if ne, ok := err.(net.Error); ok && ne.Timeout() {
		t.Fatal("upstream close did not reach the client within 2s")
	}
}

func TestWebSocketUpgradeRefusalIsRelayed(t *testing.T) {
	tp := newWSFixture(t, nil)

	// Raw upgrade request so we can inspect the plain HTTP refusal.
	conn, err := net.Dial("tcp", strings.TrimPrefix(tp.server.URL, "http://"))
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer conn.Close()
	fmt.Fprintf(conn, "GET /ws/refuse HTTP/1.1\r\n"+
		"Host: proxy.test\r\n"+
		"Connection: Upgrade\r\n"+
		"Upgrade: websocket\r\n"+
		"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n"+
		"Sec-WebSocket-Version: 13\r\n"+
		"Origin: http://client.test/\r\n\r\n")
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	defer resp.Body.Close()

	// Upstream answered non-101: relayed, not masked as 502 (§9.5).
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Refused"); got != "yes" {
		t.Fatalf("X-Refused = %q", got)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "denied" {
		t.Fatalf("body = %q, want denied", body)
	}

	e := lastEntry(t, tp.ring, 1)
	if e.Status != 403 || e.Streaming != "websocket" || e.Error != "" {
		t.Fatalf("access entry = %+v", e)
	}
	waitFor(t, 2*time.Second, func() bool {
		s := tp.reg.Snapshot()
		return s.ActiveWebSockets == 0 && s.ActiveRequests == 0
	}, "gauges back to 0 after refusal")
}

func TestWebSocketUpstreamDownReturns502(t *testing.T) {
	snap := buildSnapshot(t, fmt.Sprintf(`
version: 1
routes:
  - name: ws
    prefix: /ws
    target: http://%s
`, closedAddr(t)))
	tp := newTestProxy(t, snap)

	conn, err := net.Dial("tcp", strings.TrimPrefix(tp.server.URL, "http://"))
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer conn.Close()
	fmt.Fprintf(conn, "GET /ws/echo HTTP/1.1\r\n"+
		"Host: proxy.test\r\n"+
		"Connection: Upgrade\r\n"+
		"Upgrade: websocket\r\n"+
		"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n"+
		"Sec-WebSocket-Version: 13\r\n\r\n")
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", resp.StatusCode)
	}
	if code := decodeErrorBody(t, resp.Body); code != "websocket_upgrade_failed" {
		t.Fatalf("error code = %q, want websocket_upgrade_failed", code)
	}
}

func TestWSOutboundHeader(t *testing.T) {
	in := http.Header{}
	in.Set("Connection", "Upgrade, X-Hop, Sec-WebSocket-Key")
	in.Set("Upgrade", "websocket")
	in.Set("X-Hop", "zap")
	in.Set("Keep-Alive", "timeout=5")
	in.Set("Proxy-Authorization", "Basic c2VjcmV0")
	in.Set("Transfer-Encoding", "chunked")
	in.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	in.Set("Sec-WebSocket-Version", "13")
	in.Set("Sec-WebSocket-Protocol", "chat")
	in.Set("Origin", "http://client.test/")
	in.Set("Authorization", "Bearer tok")

	out := wsOutboundHeader(in)

	// Hop-by-hop gone, even the Connection-listed X-Hop.
	for _, k := range []string{"X-Hop", "Keep-Alive", "Proxy-Authorization", "Transfer-Encoding"} {
		if _, ok := out[k]; ok {
			t.Errorf("%s survived: %q", k, out[k])
		}
	}
	// The upgrade pair is forced.
	if got := out.Get("Connection"); got != "Upgrade" {
		t.Errorf("Connection = %q", got)
	}
	if got := out.Get("Upgrade"); got != "websocket" {
		t.Errorf("Upgrade = %q", got)
	}
	// Sec-WebSocket-* always pass, even when listed in Connection (§10.4).
	if got := out.Get("Sec-WebSocket-Key"); got != "dGhlIHNhbXBsZSBub25jZQ==" {
		t.Errorf("Sec-WebSocket-Key = %q", got)
	}
	for _, k := range []string{"Sec-WebSocket-Version", "Sec-WebSocket-Protocol", "Origin", "Authorization"} {
		if _, ok := out[http.CanonicalHeaderKey(k)]; !ok {
			t.Errorf("end-to-end header %s was stripped", k)
		}
	}
	// No client User-Agent → suppress Go's default one in req.Write.
	if got, ok := out["User-Agent"]; !ok || got[0] != "" {
		t.Errorf("User-Agent = %q, want suppression sentinel", got)
	}

	// A client-supplied User-Agent passes through untouched.
	in.Set("User-Agent", "custom/1.0")
	if got := wsOutboundHeader(in).Get("User-Agent"); got != "custom/1.0" {
		t.Errorf("User-Agent = %q, want custom/1.0", got)
	}
}

func TestWSHostPort(t *testing.T) {
	cases := []struct{ rawURL, want string }{
		{"http://h.test", "h.test:80"},
		{"https://h.test", "h.test:443"},
		{"http://h.test:8081", "h.test:8081"},
		{"https://h.test:8443/path", "h.test:8443"},
	}
	for _, tc := range cases {
		u, err := url.Parse(tc.rawURL)
		if err != nil {
			t.Fatalf("parse %q: %v", tc.rawURL, err)
		}
		if got := wsHostPort(u); got != tc.want {
			t.Errorf("wsHostPort(%q) = %q, want %q", tc.rawURL, got, tc.want)
		}
	}
}
