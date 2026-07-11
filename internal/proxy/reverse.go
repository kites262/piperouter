package proxy

import (
	"context"
	"errors"
	"log"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"slices"
	"strings"

	"github.com/kites262/piperouter/internal/metrics"
	"github.com/kites262/piperouter/internal/router"
	"github.com/kites262/piperouter/internal/transport"
)

// forwardHeaders are the proxy-metadata request headers governed by the
// route's strip_forward_headers option: Forwarded/X-Forwarded-* reveal the
// original client, Via reveals the proxy chain. Stripping (the default)
// removes them so upstream targets never see them; keeping passes inbound
// values through unchanged. PipeRouter never ADDS any of them (§9.3).
var forwardHeaders = []string{
	"Forwarded",
	"Via",
	"X-Forwarded-For",
	"X-Forwarded-Host",
	"X-Forwarded-Proto",
}

// serveReverse forwards a plain (non-WebSocket) request through
// httputil.ReverseProxy. The ReverseProxy value is per-request and cheap:
// the expensive part — the *http.Transport — is the long-lived pool entry.
func (h *handler) serveReverse(rw *responseRecorder, r *http.Request, route *router.Route, entry *transport.Entry, st *requestState) {
	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL = route.Rewrite(pr.In.URL)
			// Host = target host, fixed in v0.1 (PRD §9.2). Explicit is
			// better than relying on Host=="" falling back to URL.Host.
			pr.Out.Host = pr.Out.URL.Host
			if route.StripForwardHeaders {
				// ReverseProxy already deleted Forwarded/X-Forwarded-*
				// before Rewrite; Via is ours to remove.
				for _, k := range forwardHeaders {
					pr.Out.Header.Del(k)
				}
			} else {
				// Transparency §9.3: never add X-Forwarded-*/Forwarded (so
				// no SetXForwarded), but restore inbound values that
				// ReverseProxy stripped before calling Rewrite — UNLESS the
				// client declared them hop-by-hop via Connection, which
				// §9.4 requires removing.
				connTokens := pr.In.Header["Connection"]
				for _, k := range forwardHeaders {
					if headerValuesContainToken(connTokens, k) {
						continue
					}
					if vals, ok := pr.In.Header[k]; ok {
						pr.Out.Header[k] = slices.Clone(vals)
					}
				}
			}
		},
		Transport: entry.RoundTripper,
		// Immediate flush after every write: SSE and other streaming
		// responses are never aggregated (PRD §10.3).
		FlushInterval: -1,
		ModifyResponse: func(resp *http.Response) error {
			if isEventStream(resp.Header.Get("Content-Type")) {
				st.streaming = metrics.StreamSSE
				h.reg.MarkStream(route.Name, metrics.StreamSSE)
			}
			return nil
		},
		ErrorHandler: func(_ http.ResponseWriter, req *http.Request, err error) {
			h.handleUpstreamError(rw, req, err, st)
		},
		// Aux ReverseProxy diagnostics go to the app log (never to
		// clients); upstream details are fine there.
		ErrorLog: log.New(slogWriter{h.logger}, "", 0),
	}
	rp.ServeHTTP(rw, r)
}

// handleUpstreamError implements the PRD §9.6 error mapping. The response
// body is always one of the fixed JSON codes; err details (which may
// contain upstream or proxy hosts) go to the app log only.
func (h *handler) handleUpstreamError(rw *responseRecorder, r *http.Request, err error, st *requestState) {
	status, class := classifyUpstreamError(err)
	if class == errClientCanceled || errors.Is(r.Context().Err(), context.Canceled) {
		// The client is gone: generate no response (§9.6) and skip
		// Observe — neither a success nor an upstream failure.
		st.errClass = errClientCanceled
		st.skipObserve = true
		h.logger.Debug("proxy: client canceled request",
			slog.String("route", st.routeName),
			slog.String("path", st.path))
		return
	}
	st.errClass = class
	h.logger.Error("proxy: upstream request failed",
		slog.String("route", st.routeName),
		slog.String("transport", st.transportName),
		slog.String("class", class),
		slog.String("error", err.Error()))
	if !rw.wroteHeader {
		writeJSONError(rw, status, class)
	}
}

// isEventStream reports whether a response Content-Type declares SSE.
func isEventStream(contentType string) bool {
	ct := strings.TrimSpace(contentType)
	return len(ct) >= len("text/event-stream") &&
		strings.EqualFold(ct[:len("text/event-stream")], "text/event-stream")
}

// slogWriter bridges the *log.Logger that httputil.ReverseProxy expects to
// the structured app logger.
type slogWriter struct{ l *slog.Logger }

func (w slogWriter) Write(p []byte) (int, error) {
	w.l.Warn("proxy: reverse proxy", slog.String("detail", strings.TrimSpace(string(p))))
	return len(p), nil
}
