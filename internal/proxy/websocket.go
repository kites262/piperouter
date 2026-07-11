package proxy

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"
	"time"

	"github.com/kites262/piperouter/internal/config"
	"github.com/kites262/piperouter/internal/router"
	"github.com/kites262/piperouter/internal/transport"
)

// hopByHopHeaders are removed when building the outbound upgrade request
// and when relaying a refusal response (PRD §9.4). Connection and Upgrade
// are re-added explicitly for the upgrade itself.
var hopByHopHeaders = []string{
	"Connection",
	"Proxy-Connection",
	"Keep-Alive",
	"Proxy-Authenticate",
	"Proxy-Authorization",
	"Te",
	"Trailer",
	"Transfer-Encoding",
	"Upgrade",
}

// serveWebSocket tunnels an HTTP/1.1 WebSocket upgrade (PRD §10.4): dial
// the upstream through the route's transport chain, replay the handshake,
// then copy bytes both ways with no idle deadlines and no frame parsing.
func (h *handler) serveWebSocket(rw *responseRecorder, r *http.Request, netCfg config.NetworkConfig, route *router.Route, entry *transport.Entry, st *requestState) {
	target := route.Rewrite(r.URL)

	dialCtx := r.Context()
	if d := netCfg.DialTimeout.Std(); d > 0 {
		var cancel context.CancelFunc
		dialCtx, cancel = context.WithTimeout(dialCtx, d)
		defer cancel()
	}
	upstream, err := entry.DialContext(dialCtx, "tcp", wsHostPort(target))
	if err != nil {
		h.wsUpgradeFailed(rw, st, "dial", err)
		return
	}
	defer upstream.Close()

	if target.Scheme == "https" {
		tlsConn := tls.Client(upstream, &tls.Config{ServerName: target.Hostname()})
		hsCtx := r.Context()
		if d := netCfg.TLSHandshakeTimeout.Std(); d > 0 {
			var cancel context.CancelFunc
			hsCtx, cancel = context.WithTimeout(hsCtx, d)
			defer cancel()
		}
		if err := tlsConn.HandshakeContext(hsCtx); err != nil {
			h.wsUpgradeFailed(rw, st, "tls_handshake", err)
			return
		}
		upstream = tlsConn
	}

	// Outbound HTTP/1.1 handshake: rewritten path?query, Host = target
	// host (§9.2), end-to-end headers plus the forced upgrade pair; all
	// Sec-WebSocket-* pass through (§10.4). req.Write encodes it.
	outReq := &http.Request{
		Method:     r.Method,
		URL:        target,
		Proto:      "HTTP/1.1",
		ProtoMajor: 1,
		ProtoMinor: 1,
		Header:     wsOutboundHeader(r.Header),
		Host:       target.Host,
	}
	// Bound the upstream handshake (request write + response read) so a
	// stalled upstream cannot hang the client forever, and poison it if the
	// client disconnects mid-handshake (PRD §9.6, §11.5). The tunnel and the
	// refusal relay below run WITHOUT deadlines (§10.4, §9.5).
	if d := netCfg.ResponseHeaderTimeout.Std(); d > 0 {
		upstream.SetDeadline(time.Now().Add(d)) //nolint:errcheck // best-effort
	}
	watchDone := make(chan struct{})
	go func() {
		select {
		case <-r.Context().Done():
			upstream.SetDeadline(time.Now()) //nolint:errcheck // unblock a stuck handshake
		case <-watchDone:
		}
	}()
	stopHandshakeWatch := func() {
		close(watchDone)
		upstream.SetDeadline(time.Time{}) //nolint:errcheck // clear for the tunnel/relay
	}

	if err := outReq.Write(upstream); err != nil {
		stopHandshakeWatch()
		h.wsUpgradeFailed(rw, st, "write_request", err)
		return
	}
	br := bufio.NewReader(upstream)
	resp, err := http.ReadResponse(br, outReq)
	if err != nil {
		stopHandshakeWatch()
		h.wsUpgradeFailed(rw, st, "read_response", err)
		return
	}
	stopHandshakeWatch()

	if resp.StatusCode != http.StatusSwitchingProtocols {
		// The upstream itself answered: relay its refusal transparently
		// (§9.5) instead of masking it as a proxy error. Stream the FULL
		// body so the forwarded Content-Length stays accurate — io.Copy is
		// constant-memory, so no cap is needed.
		defer resp.Body.Close()
		dst := rw.Header()
		for k, vv := range stripHopByHop(resp.Header) {
			dst[k] = vv
		}
		rw.WriteHeader(resp.StatusCode)
		io.Copy(rw, resp.Body) //nolint:errcheck // best-effort relay
		return
	}

	clientConn, clientBuf, err := http.NewResponseController(rw).Hijack()
	if err != nil {
		h.wsUpgradeFailed(rw, st, "hijack", err)
		return
	}
	defer clientConn.Close()
	// Track the tunnel so graceful shutdown can drain it then force it
	// closed (hijacked conns are invisible to http.Server.Shutdown, §22.3).
	// Force-close sets both deadlines to now, unblocking the copy loops —
	// the same teardown the normal exit path uses.
	unregister := h.registerTunnel(func() {
		now := time.Now()
		clientConn.SetDeadline(now) //nolint:errcheck // best-effort unblock
		upstream.SetDeadline(now)   //nolint:errcheck // best-effort unblock
	})
	defer unregister()
	// The response head goes to the hijacked conn; record the status for
	// metrics and the access log ourselves.
	rw.status = http.StatusSwitchingProtocols
	rw.wroteHeader = true

	if err := writeResponseHead(clientBuf.Writer, resp); err != nil {
		// Client is already gone; the tunnel never started.
		st.errClass = errClientCanceled
		st.skipObserve = true
		h.logger.Debug("proxy: websocket client went away before 101 relay",
			slog.String("route", st.routeName))
		return
	}

	// Bidirectional copy, no idle deadlines during the tunnel (§10.4).
	// clientBuf.Reader carries any bytes the client sent right after the
	// handshake; br carries any bytes the upstream sent after the 101.
	errc := make(chan error, 2)
	go wsCopy(upstream, clientBuf.Reader, errc)
	go wsCopy(clientConn, br, errc)
	<-errc
	// One direction is done (its wsCopy already half-closed its dst).
	// Unblock the other direction and tear the tunnel down.
	deadline := time.Now()
	clientConn.SetDeadline(deadline) //nolint:errcheck // best-effort unblock
	upstream.SetDeadline(deadline)   //nolint:errcheck // best-effort unblock
	<-errc
}

// wsCopy copies one tunnel direction, then half-closes the destination so
// the peer observes EOF promptly (close propagation §10.4, §23.6).
func wsCopy(dst net.Conn, src io.Reader, errc chan<- error) {
	_, err := io.Copy(dst, src)
	closeWrite(dst)
	errc <- err
}

// closeWrite half-closes a connection when supported (*net.TCPConn sends
// FIN, *tls.Conn sends close_notify).
func closeWrite(c net.Conn) {
	if cw, ok := c.(interface{ CloseWrite() error }); ok {
		cw.CloseWrite() //nolint:errcheck // best-effort half-close
	}
}

// wsUpgradeFailed answers 502 websocket_upgrade_failed for failures before
// the upstream produced any response. Details go to the app log only.
func (h *handler) wsUpgradeFailed(rw *responseRecorder, st *requestState, stage string, err error) {
	st.errClass = errWebSocketUpgrade
	h.logger.Error("proxy: websocket upgrade failed",
		slog.String("route", st.routeName),
		slog.String("transport", st.transportName),
		slog.String("stage", stage),
		slog.String("error", err.Error()))
	if !rw.wroteHeader {
		writeJSONError(rw, http.StatusBadGateway, errWebSocketUpgrade)
	}
}

// wsHostPort returns host:port for dialing, defaulting to 80/443.
func wsHostPort(u *url.URL) string {
	if u.Port() != "" {
		return u.Host
	}
	if u.Scheme == "https" {
		return net.JoinHostPort(u.Hostname(), "443")
	}
	return net.JoinHostPort(u.Hostname(), "80")
}

// wsOutboundHeader builds the upgrade request headers: end-to-end headers
// pass through, hop-by-hop headers (standard + Connection-listed) are
// stripped, Sec-WebSocket-* always survive, and the Connection/Upgrade
// pair is forced (§9.4, §10.4).
func wsOutboundHeader(in http.Header) http.Header {
	out := in.Clone()
	for _, v := range in["Connection"] {
		for _, name := range strings.Split(v, ",") {
			name = textproto.CanonicalMIMEHeaderKey(strings.TrimSpace(name))
			if name == "" || strings.HasPrefix(name, "Sec-Websocket-") {
				continue
			}
			out.Del(name)
		}
	}
	for _, k := range hopByHopHeaders {
		out.Del(k)
	}
	out.Set("Connection", "Upgrade")
	out.Set("Upgrade", "websocket")
	// Transparency §9.1/§9.3: req.Write would add Go's default User-Agent;
	// suppress it when the client sent none.
	if _, ok := in["User-Agent"]; !ok {
		out.Set("User-Agent", "")
	}
	return out
}

// stripHopByHop returns a copy of h without hop-by-hop headers (standard
// list + names declared in Connection), for relaying refusal responses.
func stripHopByHop(h http.Header) http.Header {
	out := h.Clone()
	for _, v := range h["Connection"] {
		for _, name := range strings.Split(v, ",") {
			if name = strings.TrimSpace(name); name != "" {
				out.Del(name)
			}
		}
	}
	for _, k := range hopByHopHeaders {
		out.Del(k)
	}
	return out
}

// writeResponseHead relays the upstream 101 response head to the hijacked
// client connection.
func writeResponseHead(w *bufio.Writer, resp *http.Response) error {
	status := resp.Status
	if status == "" {
		status = fmt.Sprintf("%d %s", resp.StatusCode, http.StatusText(resp.StatusCode))
	}
	if _, err := fmt.Fprintf(w, "HTTP/1.1 %s\r\n", status); err != nil {
		return err
	}
	if err := resp.Header.Write(w); err != nil {
		return err
	}
	if _, err := io.WriteString(w, "\r\n"); err != nil {
		return err
	}
	return w.Flush()
}
