package transport

// Shared in-process test helpers. No test in this package touches the
// external network: every upstream, HTTP proxy and SOCKS5 proxy is a
// local server.

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/kites262/piperouter/internal/config"
)

func testNetCfg() config.NetworkConfig {
	return config.NetworkConfig{
		DialTimeout:           config.Duration(5 * time.Second),
		TLSHandshakeTimeout:   config.Duration(5 * time.Second),
		ResponseHeaderTimeout: config.Duration(10 * time.Second),
		IdleConnectionTimeout: config.Duration(30 * time.Second),
	}
}

// newTestPool builds a pool and registers idle-connection cleanup. Create
// servers BEFORE the pool so the pool's cleanup runs first (t.Cleanup is
// LIFO) and open tunnels unblock server Close.
func newTestPool(t *testing.T, transports ...config.TransportConfig) *Pool {
	t.Helper()
	p, err := NewPool(transports, testNetCfg())
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	t.Cleanup(p.CloseIdleConnections)
	return p
}

func mustGet(t *testing.T, p *Pool, name string) *Entry {
	t.Helper()
	e, ok := p.Get(name)
	if !ok {
		t.Fatalf("Get(%q) = _, false; want entry", name)
	}
	return e
}

const echoBody = "hello from target"

// newEchoServer starts a plain HTTP upstream that responds with echoBody.
func newEchoServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Echo-Path", r.URL.Path)
		io.WriteString(w, echoBody)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// hostPort extracts "host:port" from a server URL like "http://127.0.0.1:1234".
func hostPort(t *testing.T, rawURL string) string {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse url %q: %v", rawURL, err)
	}
	return u.Host
}

// deadAddr returns a "host:port" that is guaranteed to refuse connections:
// the port was just released by a closed listener.
func deadAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close()
	return addr
}

func newTestClient(rt http.RoundTripper) *http.Client {
	return &http.Client{Transport: rt, Timeout: 10 * time.Second}
}

func getBody(t *testing.T, client *http.Client, url string) (int, string) {
	t.Helper()
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp.StatusCode, string(body)
}

// rawHTTPGet performs a manual HTTP/1.1 GET over an already-established raw
// connection (as the WebSocket path would) and returns status code and body.
func rawHTTPGet(t *testing.T, conn net.Conn, host, path string) (int, string) {
	t.Helper()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	req := fmt.Sprintf("GET %s HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", path, host)
	if _, err := io.WriteString(conn, req); err != nil {
		t.Fatalf("write raw request: %v", err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("read raw response: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read raw body: %v", err)
	}
	return resp.StatusCode, string(body)
}
