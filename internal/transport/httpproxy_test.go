package transport

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kites262/piperouter/internal/config"
)

// testHTTPProxy is a minimal in-process HTTP proxy that handles both
// absolute-form plain requests and CONNECT tunneling (hijack + io.Copy).
type testHTTPProxy struct {
	srv           *httptest.Server
	fwd           *http.Transport // no proxy, ignores environment
	rejectConnect bool            // set at construction only (handlers read it concurrently)

	mu       sync.Mutex
	plain    int // non-CONNECT requests seen
	absolute int // ...of which were absolute-form
	connects int // CONNECT requests seen
}

func newTestHTTPProxy(t *testing.T, rejectConnect bool) *testHTTPProxy {
	t.Helper()
	p := &testHTTPProxy{fwd: &http.Transport{}, rejectConnect: rejectConnect}
	p.srv = httptest.NewServer(http.HandlerFunc(p.handle))
	t.Cleanup(func() {
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
	io.Copy(w, resp.Body)
}

func (p *testHTTPProxy) handleConnect(w http.ResponseWriter, r *http.Request) {
	if p.rejectConnect {
		http.Error(w, "tunneling forbidden", http.StatusForbidden)
		return
	}
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
		io.Copy(target, brw) // brw drains any bytes buffered during hijack
		target.Close()       // unblock the other copy direction
	}()
	io.Copy(client, target)
	client.Close()
	target.Close()
}

func newHTTPProxyPool(t *testing.T, proxyURL string) *Entry {
	t.Helper()
	p := newTestPool(t, config.TransportConfig{Name: "hp", Type: config.TransportHTTP, URL: proxyURL})
	return mustGet(t, p, "hp")
}

func TestHTTPProxyPlainTargetAbsoluteForm(t *testing.T) {
	target := newEchoServer(t)
	proxy := newTestHTTPProxy(t, false)
	e := newHTTPProxyPool(t, proxy.srv.URL)

	status, body := getBody(t, newTestClient(e.RoundTripper), target.URL+"/via-proxy")
	if status != http.StatusOK {
		t.Errorf("status = %d, want 200", status)
	}
	if body != echoBody {
		t.Errorf("body = %q, want %q", body, echoBody)
	}

	plain, absolute, connects := proxy.counts()
	if plain != 1 || absolute != 1 {
		t.Errorf("proxy saw plain=%d absolute=%d, want 1/1 (absolute-form)", plain, absolute)
	}
	if connects != 0 {
		t.Errorf("proxy saw %d CONNECTs for a plain http target, want 0", connects)
	}
}

func TestHTTPProxyHTTPSTargetViaCONNECT(t *testing.T) {
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "secure "+r.URL.Path)
	}))
	t.Cleanup(target.Close)
	proxy := newTestHTTPProxy(t, false)
	e := newHTTPProxyPool(t, proxy.srv.URL)

	// Test-only TLS trust injection: the entry's transport is a plain
	// *http.Transport, so give it the test server's cert pool.
	pool := x509.NewCertPool()
	pool.AddCert(target.Certificate())
	e.RoundTripper.(*http.Transport).TLSClientConfig = &tls.Config{RootCAs: pool}

	status, body := getBody(t, newTestClient(e.RoundTripper), target.URL+"/tls")
	if status != http.StatusOK {
		t.Errorf("status = %d, want 200", status)
	}
	if body != "secure /tls" {
		t.Errorf("body = %q, want %q", body, "secure /tls")
	}

	plain, _, connects := proxy.counts()
	if connects != 1 {
		t.Errorf("proxy saw %d CONNECTs, want 1", connects)
	}
	if plain != 0 {
		t.Errorf("proxy saw %d plain requests for an https target, want 0", plain)
	}
}

func TestHTTPProxyCONNECTReuse(t *testing.T) {
	// Two sequential https requests must reuse one tunnel (PRD §23.7).
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "ok")
	}))
	t.Cleanup(target.Close)
	proxy := newTestHTTPProxy(t, false)
	e := newHTTPProxyPool(t, proxy.srv.URL)

	pool := x509.NewCertPool()
	pool.AddCert(target.Certificate())
	e.RoundTripper.(*http.Transport).TLSClientConfig = &tls.Config{RootCAs: pool}
	client := newTestClient(e.RoundTripper)

	for i := 0; i < 2; i++ {
		status, body := getBody(t, client, target.URL)
		if status != http.StatusOK || body != "ok" {
			t.Fatalf("request %d: status=%d body=%q", i, status, body)
		}
	}
	if _, _, connects := proxy.counts(); connects != 1 {
		t.Errorf("proxy saw %d CONNECTs for 2 requests, want 1 (tunnel not reused)", connects)
	}
}

func TestHTTPProxyDialContextCONNECT(t *testing.T) {
	target := newEchoServer(t)
	proxy := newTestHTTPProxy(t, false)
	e := newHTTPProxyPool(t, proxy.srv.URL)

	addr := hostPort(t, target.URL)
	conn, err := e.DialContext(context.Background(), "tcp", addr)
	if err != nil {
		t.Fatalf("DialContext: %v", err)
	}
	defer conn.Close()

	// The tunnel is a working raw conn: run a manual HTTP exchange on it.
	status, body := rawHTTPGet(t, conn, addr, "/tunnel")
	if status != http.StatusOK {
		t.Errorf("status = %d, want 200", status)
	}
	if body != echoBody {
		t.Errorf("body = %q, want %q", body, echoBody)
	}
	if _, _, connects := proxy.counts(); connects != 1 {
		t.Errorf("proxy saw %d CONNECTs, want 1", connects)
	}
}

func TestHTTPProxyDialContextRejectedCONNECT(t *testing.T) {
	proxy := newTestHTTPProxy(t, true)
	e := newHTTPProxyPool(t, proxy.srv.URL)

	conn, err := e.DialContext(context.Background(), "tcp", "203.0.113.1:443")
	if err == nil {
		conn.Close()
		t.Fatal("DialContext succeeded through rejecting proxy, want error")
	}
	if !strings.Contains(err.Error(), "403") {
		t.Errorf("error = %q, want it to mention status 403", err)
	}
}

func TestHTTPProxyDialContextCanceledContext(t *testing.T) {
	proxy := newTestHTTPProxy(t, false)
	e := newHTTPProxyPool(t, proxy.srv.URL)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	conn, err := e.DialContext(ctx, "tcp", "203.0.113.1:443")
	if err == nil {
		conn.Close()
		t.Fatal("DialContext with canceled context succeeded, want error")
	}
}

func TestHTTPProxyDown(t *testing.T) {
	target := newEchoServer(t)
	e := newHTTPProxyPool(t, "http://"+deadAddr(t))

	if _, err := newTestClient(e.RoundTripper).Get(target.URL); err == nil {
		t.Error("RoundTripper through dead proxy succeeded, want error")
	}
	conn, err := e.DialContext(context.Background(), "tcp", hostPort(t, target.URL))
	if err == nil {
		conn.Close()
		t.Error("DialContext through dead proxy succeeded, want error")
	}
}

func TestParseConnectStatus(t *testing.T) {
	tests := []struct {
		line     string
		wantCode int
		wantErr  bool
	}{
		{line: "HTTP/1.1 200 Connection established", wantCode: 200},
		{line: "HTTP/1.0 200 OK", wantCode: 200},
		{line: "HTTP/1.1 200", wantCode: 200},
		{line: "HTTP/1.1 407 Proxy Authentication Required", wantCode: 407},
		{line: "HTTP/1.1 502 Bad Gateway", wantCode: 502},
		{line: "HTTP/1.1", wantErr: true},
		{line: "HTTP/1.1 abc", wantErr: true},
		{line: "HTTP/1.1 99 Too Low", wantErr: true},
		{line: "HTTP/1.1 600 Too High", wantErr: true},
		{line: "SSH-2.0-OpenSSH banner", wantErr: true},
		{line: "", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.line, func(t *testing.T) {
			code, err := parseConnectStatus(tc.line)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parseConnectStatus(%q) = %d, nil; want error", tc.line, code)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseConnectStatus(%q): %v", tc.line, err)
			}
			if code != tc.wantCode {
				t.Errorf("code = %d, want %d", code, tc.wantCode)
			}
		})
	}
}
