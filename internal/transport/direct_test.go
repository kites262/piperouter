package transport

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/kites262/piperouter/internal/config"
)

func TestDirectRoundTripper(t *testing.T) {
	target := newEchoServer(t)
	p := newTestPool(t)
	e := mustGet(t, p, config.DirectName)

	status, body := getBody(t, newTestClient(e.RoundTripper), target.URL+"/ping")
	if status != http.StatusOK {
		t.Errorf("status = %d, want 200", status)
	}
	if body != echoBody {
		t.Errorf("body = %q, want %q", body, echoBody)
	}
}

// countingListener counts accepted connections to prove pooling/reuse.
type countingListener struct {
	net.Listener
	accepts atomic.Int64
}

func (l *countingListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err == nil {
		l.accepts.Add(1)
	}
	return c, err
}

func TestDirectConnectionReuse(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	cl := &countingListener{Listener: ln}

	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))
	srv.Listener.Close()
	srv.Listener = cl
	srv.Start()
	t.Cleanup(srv.Close)

	p := newTestPool(t)
	e := mustGet(t, p, config.DirectName)
	client := newTestClient(e.RoundTripper)

	for i := 0; i < 2; i++ {
		status, body := getBody(t, client, srv.URL)
		if status != http.StatusOK || body != "ok" {
			t.Fatalf("request %d: status=%d body=%q", i, status, body)
		}
	}
	if got := cl.accepts.Load(); got != 1 {
		t.Errorf("accepted connections = %d, want 1 (connection not reused)", got)
	}
}

func TestDirectDialContext(t *testing.T) {
	target := newEchoServer(t)
	p := newTestPool(t)
	e := mustGet(t, p, config.DirectName)

	addr := hostPort(t, target.URL)
	conn, err := e.DialContext(context.Background(), "tcp", addr)
	if err != nil {
		t.Fatalf("DialContext: %v", err)
	}
	defer conn.Close()

	status, body := rawHTTPGet(t, conn, addr, "/raw")
	if status != http.StatusOK {
		t.Errorf("status = %d, want 200", status)
	}
	if body != echoBody {
		t.Errorf("body = %q, want %q", body, echoBody)
	}
}

func TestDirectDialContextRefused(t *testing.T) {
	p := newTestPool(t)
	e := mustGet(t, p, config.DirectName)

	conn, err := e.DialContext(context.Background(), "tcp", deadAddr(t))
	if err == nil {
		conn.Close()
		t.Fatal("DialContext to dead address succeeded, want error")
	}
}
