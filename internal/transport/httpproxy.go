package transport

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/textproto"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/kites262/piperouter/internal/config"
)

// newHTTPEntry builds the entry for a type "http" transport (PRD §11.2).
// The *http.Transport routes everything through the proxy: plain http
// targets in absolute form, https targets via CONNECT (net/http does the
// tunneling itself, so target TLS is end-to-end — no MITM). Entry.DialContext
// performs a manual CONNECT handshake and hands back the raw tunnel; it is
// used by the WebSocket path.
func newHTTPEntry(tc config.TransportConfig, netCfg config.NetworkConfig) (*Entry, error) {
	u, err := url.Parse(tc.URL)
	if err != nil {
		return nil, fmt.Errorf("invalid proxy url: %w", err)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("proxy url has no host")
	}
	proxyAddr := u.Host
	if u.Port() == "" {
		proxyAddr = net.JoinHostPort(u.Hostname(), "80")
	}

	dialer := &net.Dialer{Timeout: netCfg.DialTimeout.Std()}
	t := newBaseTransport(dialer.DialContext, netCfg)
	t.Proxy = http.ProxyURL(u)

	handshakeTimeout := netCfg.DialTimeout.Std()
	return &Entry{
		Name:         tc.Name,
		Type:         config.TransportHTTP,
		ProxyURL:     tc.URL,
		RoundTripper: t,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return dialViaCONNECT(ctx, dialer, network, proxyAddr, addr, handshakeTimeout)
		},
	}, nil
}

// dialViaCONNECT dials the HTTP proxy and establishes a raw TCP tunnel to
// targetAddr with a manual CONNECT handshake. The handshake is bounded by
// the dial timeout (and the context deadline, whichever is sooner); the
// deadline is cleared before the connection is returned.
func dialViaCONNECT(ctx context.Context, dialer *net.Dialer, network, proxyAddr, targetAddr string, handshakeTimeout time.Duration) (net.Conn, error) {
	conn, err := dialer.DialContext(ctx, network, proxyAddr)
	if err != nil {
		return nil, fmt.Errorf("dial http proxy: %w", err)
	}

	var deadline time.Time
	if handshakeTimeout > 0 {
		deadline = time.Now().Add(handshakeTimeout)
	}
	if d, ok := ctx.Deadline(); ok && (deadline.IsZero() || d.Before(deadline)) {
		deadline = d
	}
	if !deadline.IsZero() {
		_ = conn.SetDeadline(deadline)
	}

	// Abort the handshake promptly on context cancellation. The watcher is
	// always joined before the deadline is cleared so it can never stamp a
	// deadline on a connection that has been handed to the caller.
	stop := make(chan struct{})
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		select {
		case <-ctx.Done():
			_ = conn.SetDeadline(time.Now())
		case <-stop:
		}
	}()

	// Read exactly the status line plus MIME headers of the CONNECT
	// response and nothing more. After a 2xx the proxy sends nothing until
	// we do (the tunnel is idle), so the bufio.Reader cannot buffer bytes
	// past the terminating blank line; if a non-compliant proxy sent early
	// data anyway, it is replayed ahead of the raw connection below.
	br := bufio.NewReader(conn)
	var code int
	hsErr := func() error {
		if _, err := io.WriteString(conn, "CONNECT "+targetAddr+" HTTP/1.1\r\nHost: "+targetAddr+"\r\n\r\n"); err != nil {
			return fmt.Errorf("write CONNECT request: %w", err)
		}
		tp := textproto.NewReader(br)
		line, err := tp.ReadLine()
		if err != nil {
			return fmt.Errorf("read CONNECT response: %w", err)
		}
		code, err = parseConnectStatus(line)
		if err != nil {
			return err
		}
		if _, err := tp.ReadMIMEHeader(); err != nil {
			return fmt.Errorf("read CONNECT response headers: %w", err)
		}
		return nil
	}()
	close(stop)
	<-watchDone

	if hsErr == nil && ctx.Err() != nil {
		hsErr = ctx.Err()
	}
	if hsErr == nil && (code < 200 || code >= 300) {
		hsErr = fmt.Errorf("http proxy CONNECT to %s failed: status %d", targetAddr, code)
	}
	if hsErr != nil {
		conn.Close()
		return nil, hsErr
	}

	_ = conn.SetDeadline(time.Time{})
	if n := br.Buffered(); n > 0 {
		peek, err := br.Peek(n)
		if err != nil {
			conn.Close()
			return nil, fmt.Errorf("recover buffered tunnel bytes: %w", err)
		}
		prefix := make([]byte, n)
		copy(prefix, peek)
		return newPrefixConn(conn, prefix), nil
	}
	return conn, nil
}

// parseConnectStatus extracts the status code from a CONNECT response
// status line such as "HTTP/1.1 200 Connection established".
func parseConnectStatus(line string) (int, error) {
	proto, rest, ok := strings.Cut(line, " ")
	if !ok || !strings.HasPrefix(proto, "HTTP/") {
		return 0, fmt.Errorf("malformed CONNECT response status line %q", line)
	}
	statusStr, _, _ := strings.Cut(strings.TrimSpace(rest), " ")
	code, err := strconv.Atoi(statusStr)
	if err != nil || code < 100 || code > 599 {
		return 0, fmt.Errorf("malformed CONNECT response status line %q", line)
	}
	return code, nil
}

// prefixConn replays bytes that were buffered past the CONNECT response
// headers before reading from the underlying connection. A compliant proxy
// never produces such bytes; this is a safety net so none are lost.
type prefixConn struct {
	net.Conn
	r io.Reader
}

func newPrefixConn(conn net.Conn, prefix []byte) *prefixConn {
	return &prefixConn{Conn: conn, r: io.MultiReader(bytes.NewReader(prefix), conn)}
}

func (c *prefixConn) Read(p []byte) (int, error) { return c.r.Read(p) }
