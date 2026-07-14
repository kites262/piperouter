package proxy

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
)

// Client-visible error codes (PRD §7.4, §9.6, §22.3; architecture
// error-code vocabulary). Bodies never contain upstream details, proxy
// URLs or credentials — those go to the application log only.
const (
	errRouteNotFound     = "route_not_found"
	errUpstreamFailed    = "upstream_connection_failed"
	errUpstreamTimeout   = "upstream_timeout"
	errInternal          = "internal_error"
	errWebSocketUpgrade  = "websocket_upgrade_failed"
	errMethodNotAllowed  = "method_not_allowed" // static routes: only GET/HEAD
	errClientCanceled    = "client_canceled"    // log-only, never written to a client
	jsonContentType      = "application/json"
	xContentTypeOptions  = "X-Content-Type-Options"
	xContentTypeNosniff  = "nosniff"
	contentTypeHeaderKey = "Content-Type"
)

// writeJSONError writes a fixed JSON error body. code must be one of the
// fixed error-code constants (no escaping is performed).
func writeJSONError(w http.ResponseWriter, status int, code string) {
	h := w.Header()
	h.Set(contentTypeHeaderKey, jsonContentType)
	h.Set(xContentTypeOptions, xContentTypeNosniff)
	w.WriteHeader(status)
	io.WriteString(w, `{"error":"`+code+`"}`+"\n")
}

// Anonymous data-plane errors: unmatched 404s and static-route 405s use the
// stock net/http responses (http.NotFound / http.Error with StatusText), so
// probing clients see exactly what any vanilla Go server would send —
// blending into the largest possible crowd beats minimizing bytes. The JSON
// envelope below is reserved for errors only a real routed client can hit.

// classifyUpstreamError maps a RoundTrip/dial error to a client-visible
// status and error class per PRD §9.6:
//
//   - client cancellation           → status 0, "client_canceled" (write nothing)
//   - dial failures (refused, DNS not found, dial timeout, proxy connect,
//     SOCKS5 negotiation), TLS handshake failures and everything else
//     that is not a post-connect timeout → 502 "upstream_connection_failed"
//   - waiting for response headers timed out → 504 "upstream_timeout"
//
// Dial timeouts also report Timeout()==true, so the dial check runs first:
// a connect-phase timeout is a connection failure (502), not a 504.
func classifyUpstreamError(err error) (int, string) {
	if err == nil {
		return http.StatusBadGateway, errUpstreamFailed
	}
	if errors.Is(err, context.Canceled) {
		return 0, errClientCanceled
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) && opErr.Op == "dial" {
		return http.StatusBadGateway, errUpstreamFailed
	}
	// net/http's private tlsHandshakeTimeoutError reports Timeout()==true
	// but is a connect-phase failure → 502, checked before the generic
	// timeout rule.
	if strings.Contains(err.Error(), "TLS handshake timeout") {
		return http.StatusBadGateway, errUpstreamFailed
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		// Post-connect timeout: http.Transport's ResponseHeaderTimeout
		// ("net/http: timeout awaiting response headers") or an
		// equivalent deadline while waiting for the upstream response.
		return http.StatusGatewayTimeout, errUpstreamTimeout
	}
	return http.StatusBadGateway, errUpstreamFailed
}
