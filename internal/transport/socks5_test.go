package transport

import (
	"bufio"
	"context"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/kites262/piperouter/internal/config"
)

// socksRequest records the ATYP and host of one CONNECT request as seen by
// the test SOCKS5 server — used to prove hostname passthrough.
type socksRequest struct {
	atyp byte
	host string
}

// testSOCKS5Server is a minimal in-process SOCKS5 server: no-auth method
// negotiation, CONNECT command, IPv4/domain/IPv6 address types.
type testSOCKS5Server struct {
	ln net.Listener

	mu       sync.Mutex
	requests []socksRequest
}

func newTestSOCKS5Server(t *testing.T) *testSOCKS5Server {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s := &testSOCKS5Server{ln: ln}
	go s.serve()
	t.Cleanup(func() { ln.Close() })
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
		c.Write([]byte{0x05, 0x08, 0x00, 0x01, 0, 0, 0, 0, 0, 0}) // address type not supported
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
		c.Write([]byte{0x05, 0x05, 0x00, 0x01, 0, 0, 0, 0, 0, 0}) // connection refused
		return
	}
	if _, err := c.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0}); err != nil {
		target.Close()
		return
	}
	_ = c.SetDeadline(time.Time{})

	go func() {
		io.Copy(target, br) // br drains bytes buffered during the handshake
		target.Close()      // unblock the other copy direction
	}()
	io.Copy(c, target)
	target.Close()
}

func newSOCKS5Pool(t *testing.T, proxyURL string) *Entry {
	t.Helper()
	p := newTestPool(t, config.TransportConfig{Name: "sp", Type: config.TransportSOCKS5, URL: proxyURL})
	return mustGet(t, p, "sp")
}

func TestSOCKS5RoundTripperHostnamePassthrough(t *testing.T) {
	target := newEchoServer(t)
	socks := newTestSOCKS5Server(t)
	e := newSOCKS5Pool(t, "socks5://"+socks.addr())

	// Address the target BY HOSTNAME: the hostname must reach the SOCKS5
	// server unresolved (ATYP domain), proving resolution happens at the
	// proxy (PRD §11.3).
	_, port, err := net.SplitHostPort(hostPort(t, target.URL))
	if err != nil {
		t.Fatalf("split host port: %v", err)
	}
	status, body := getBody(t, newTestClient(e.RoundTripper), "http://localhost:"+port+"/via-socks")
	if status != http.StatusOK {
		t.Errorf("status = %d, want 200", status)
	}
	if body != echoBody {
		t.Errorf("body = %q, want %q", body, echoBody)
	}

	reqs := socks.seen()
	if len(reqs) != 1 {
		t.Fatalf("SOCKS5 server saw %d CONNECTs, want 1: %v", len(reqs), reqs)
	}
	if reqs[0].atyp != 0x03 {
		t.Errorf("ATYP = %#x, want 0x03 (domain — hostname passthrough)", reqs[0].atyp)
	}
	if reqs[0].host != "localhost" {
		t.Errorf("host = %q, want %q", reqs[0].host, "localhost")
	}
}

func TestSOCKS5RoundTripperConnectionReuse(t *testing.T) {
	target := newEchoServer(t)
	socks := newTestSOCKS5Server(t)
	e := newSOCKS5Pool(t, "socks5://"+socks.addr())
	client := newTestClient(e.RoundTripper)

	for i := 0; i < 2; i++ {
		status, _ := getBody(t, client, target.URL)
		if status != http.StatusOK {
			t.Fatalf("request %d: status = %d, want 200", i, status)
		}
	}
	if reqs := socks.seen(); len(reqs) != 1 {
		t.Errorf("SOCKS5 server saw %d CONNECTs for 2 requests, want 1 (connection not reused)", len(reqs))
	}
}

func TestSOCKS5DialContext(t *testing.T) {
	target := newEchoServer(t)
	socks := newTestSOCKS5Server(t)
	e := newSOCKS5Pool(t, "socks5://"+socks.addr())

	tests := []struct {
		name     string
		addrOf   func(targetAddr string) string
		wantAtyp byte
		wantHost func(targetAddr string) string
	}{
		{
			name: "domain address",
			addrOf: func(targetAddr string) string {
				_, port, _ := net.SplitHostPort(targetAddr)
				return "localhost:" + port
			},
			wantAtyp: 0x03,
			wantHost: func(string) string { return "localhost" },
		},
		{
			name:     "ipv4 address",
			addrOf:   func(targetAddr string) string { return targetAddr },
			wantAtyp: 0x01,
			wantHost: func(targetAddr string) string {
				host, _, _ := net.SplitHostPort(targetAddr)
				return host
			},
		},
	}
	targetAddr := hostPort(t, target.URL)
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			before := len(socks.seen())
			dialAddr := tc.addrOf(targetAddr)

			conn, err := e.DialContext(context.Background(), "tcp", dialAddr)
			if err != nil {
				t.Fatalf("DialContext(%q): %v", dialAddr, err)
			}
			defer conn.Close()

			status, body := rawHTTPGet(t, conn, dialAddr, "/raw-socks")
			if status != http.StatusOK {
				t.Errorf("status = %d, want 200", status)
			}
			if body != echoBody {
				t.Errorf("body = %q, want %q", body, echoBody)
			}

			reqs := socks.seen()
			if len(reqs) != before+1 {
				t.Fatalf("SOCKS5 server saw %d new CONNECTs, want 1", len(reqs)-before)
			}
			last := reqs[len(reqs)-1]
			if last.atyp != tc.wantAtyp {
				t.Errorf("ATYP = %#x, want %#x", last.atyp, tc.wantAtyp)
			}
			if want := tc.wantHost(targetAddr); last.host != want {
				t.Errorf("host = %q, want %q", last.host, want)
			}
		})
	}
}

func TestSOCKS5ServerDown(t *testing.T) {
	target := newEchoServer(t)
	e := newSOCKS5Pool(t, "socks5://"+deadAddr(t))

	if _, err := newTestClient(e.RoundTripper).Get(target.URL); err == nil {
		t.Error("RoundTripper through dead SOCKS5 proxy succeeded, want error")
	}
	conn, err := e.DialContext(context.Background(), "tcp", hostPort(t, target.URL))
	if err == nil {
		conn.Close()
		t.Error("DialContext through dead SOCKS5 proxy succeeded, want error")
	}
}
