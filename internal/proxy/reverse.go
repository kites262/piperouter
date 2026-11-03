package proxy

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"slices"

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

// reverseStateKey is the context key for *requestState during a shared
// ReverseProxy hop. Callbacks recover route/entry/rw from that single object.
type reverseStateKey struct{}

// serveReverse forwards a plain (non-WebSocket) request through the
// handler's shared httputil.ReverseProxy. The expensive part — the
// *http.Transport — is the long-lived pool entry; the ReverseProxy
// value itself is process-lifetime and safe for concurrent ServeHTTP.
func (h *handler) serveReverse(rw *responseRecorder, r *http.Request, route *router.Route, entry *transport.Entry, st *requestState) {
	st.route = route
	st.entry = entry
	st.rw = rw
	ctx := context.WithValue(r.Context(), reverseStateKey{}, st)
	h.reverse.ServeHTTP(rw, r.WithContext(ctx))
}

// reverseRoundTrip looks up the per-request transport entry from context.
// Shared ReverseProxy.Transport cannot be a single Entry because routes
// pin different pool members.
type reverseRoundTrip struct{}

func (reverseRoundTrip) RoundTrip(req *http.Request) (*http.Response, error) {
	st, _ := req.Context().Value(reverseStateKey{}).(*requestState)
	if st == nil || st.entry == nil || st.entry.RoundTripper == nil {
		return nil, errors.New("proxy: reverse call context missing transport")
	}
	resp, err := st.entry.RoundTripper.RoundTrip(req)
	if err != nil || resp == nil || resp.Body == nil {
		return resp, err
	}
	if !isEventStream(resp.Header.Get("Content-Type")) {
		return resp, nil
	}
	resp.Body = &sseCompletionTrackingBody{
		ReadCloser:      resp.Body,
		reachedTerminal: &st.sseTerminal,
	}
	return resp, nil
}

// sseCompletionTrackingBody recognizes semantic completion independently of
// the HTTP body's EOF. OpenAI-compatible clients commonly close an SSE
// connection as soon as they receive its terminal event.
type sseCompletionTrackingBody struct {
	io.ReadCloser
	reachedTerminal *bool
	line            [64]byte
	lineLen         int
	lineOverflow    bool
}

func (b *sseCompletionTrackingBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if n > 0 && !*b.reachedTerminal {
		if b.scanLines(p[:n]) {
			*b.reachedTerminal = true
		}
	}
	if err == io.EOF && !*b.reachedTerminal && b.terminalLine() {
		*b.reachedTerminal = true
	}
	return n, err
}

func (b *sseCompletionTrackingBody) scanLines(p []byte) bool {
	for _, c := range p {
		if c != '\n' {
			if b.lineLen < len(b.line) {
				b.line[b.lineLen] = c
				b.lineLen++
			} else {
				b.lineOverflow = true
			}
			continue
		}
		if b.terminalLine() {
			return true
		}
		b.lineLen = 0
		b.lineOverflow = false
	}
	return false
}

func (b *sseCompletionTrackingBody) terminalLine() bool {
	if b.lineOverflow {
		return false
	}
	line := b.line[:b.lineLen]
	if len(line) > 0 && line[len(line)-1] == '\r' {
		line = line[:len(line)-1]
	}
	return bytes.Equal(line, []byte("data: [DONE]")) ||
		bytes.Equal(line, []byte("event: response.completed"))
}

func (h *handler) reverseRewrite(pr *httputil.ProxyRequest) {
	st, _ := pr.In.Context().Value(reverseStateKey{}).(*requestState)
	if st == nil || st.route == nil {
		return
	}
	route := st.route
	pr.Out.URL = route.Rewrite(pr.In.URL)
	// Host = target host, fixed in v0.1 (PRD §9.2). Explicit is
	// better than relying on Host=="" falling back to URL.Host.
	pr.Out.Host = pr.Out.URL.Host
	if st.overwriteBuffered {
		// ReverseProxy suppresses Transport.Close on an inbound body to avoid
		// blocking on unread client uploads. Our replacement is already fully
		// buffered and Close is safe: allow the transport to free it as soon
		// as upload finishes, without retaining memory throughout an SSE.
		pr.Out.Body = pr.In.Body
		pr.Out.Header.Del("Content-Length")
		pr.Out.Header.Del("Transfer-Encoding")
		pr.Out.Header.Del("Trailer")
	}
	if st.overwriteChanged {
		for _, name := range []string{"Content-MD5", "Digest", "Content-Digest", "Repr-Digest"} {
			pr.Out.Header.Del(name)
		}
	}
	if route.StripForwardHeaders {
		// ReverseProxy already deleted Forwarded/X-Forwarded-*
		// before Rewrite; Via is ours to remove.
		for _, k := range forwardHeaders {
			pr.Out.Header.Del(k)
		}
		if st.details != nil {
			st.details.SetOutbound(pr.Out)
		}
		return
	}
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
	if st.details != nil {
		st.details.SetOutbound(pr.Out)
	}
}

func (h *handler) reverseModifyResponse(resp *http.Response) error {
	if resp == nil || resp.Request == nil {
		return nil
	}
	st, _ := resp.Request.Context().Value(reverseStateKey{}).(*requestState)
	if st == nil || st.route == nil {
		return nil
	}
	if isEventStream(resp.Header.Get("Content-Type")) {
		st.streaming = metrics.StreamSSE
		h.reg.MarkStream(st.route.Name, metrics.StreamSSE)
	}
	return nil
}

func (h *handler) reverseErrorHandler(_ http.ResponseWriter, req *http.Request, err error) {
	st, _ := req.Context().Value(reverseStateKey{}).(*requestState)
	if st == nil {
		return
	}
	h.handleUpstreamError(st.rw, req, err, st)
}

// newReverseProxy builds the process-lifetime ReverseProxy shared by every
// plain HTTP request. Callbacks read reverseCall from the request context.
func newReverseProxy(h *handler, errorLog *log.Logger) *httputil.ReverseProxy {
	return &httputil.ReverseProxy{
		Rewrite:        h.reverseRewrite,
		Transport:      reverseRoundTrip{},
		FlushInterval:  -1, // SSE and streaming: flush after every write (§10.3)
		ModifyResponse: h.reverseModifyResponse,
		ErrorHandler:   h.reverseErrorHandler,
		ErrorLog:       errorLog,
	}
}

// handleUpstreamError implements the PRD §9.6 error mapping. The response
// body is always one of the fixed JSON codes; err details (which may
// contain upstream or proxy hosts) go to the app log only.
func (h *handler) handleUpstreamError(rw *responseRecorder, r *http.Request, err error, st *requestState) {
	status, class := classifyUpstreamError(err)
	if st.sseTerminal && (class == errClientCanceled || errors.Is(r.Context().Err(), context.Canceled)) {
		// The application-level stream completed; a client that closes
		// immediately after the terminal SSE event must not turn that successful
		// request into a cancellation entry.
		st.errClass = ""
		return
	}
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
// Equivalent to TrimSpace + EqualFold on the "text/event-stream" prefix,
// without allocating.
func isEventStream(contentType string) bool {
	const sse = "text/event-stream"
	i := 0
	for i < len(contentType) && (contentType[i] == ' ' || contentType[i] == '\t') {
		i++
	}
	if len(contentType)-i < len(sse) {
		return false
	}
	for j := 0; j < len(sse); j++ {
		cc := contentType[i+j]
		if cc >= 'A' && cc <= 'Z' {
			cc += 'a' - 'A'
		}
		if cc != sse[j] {
			return false
		}
	}
	return true
}

// slogWriter bridges the *log.Logger that httputil.ReverseProxy expects to
// the structured app logger.
type slogWriter struct{ l *slog.Logger }

func (w slogWriter) Write(p []byte) (int, error) {
	// Avoid allocating when detail is empty; TrimSpace may still allocate
	// for non-empty inputs — rare (ErrorLog is cold).
	msg := string(p)
	for len(msg) > 0 && (msg[0] == ' ' || msg[0] == '\t' || msg[0] == '\n' || msg[0] == '\r') {
		msg = msg[1:]
	}
	for len(msg) > 0 {
		c := msg[len(msg)-1]
		if c != ' ' && c != '\t' && c != '\n' && c != '\r' {
			break
		}
		msg = msg[:len(msg)-1]
	}
	w.l.Warn("proxy: reverse proxy", slog.String("detail", msg))
	return len(p), nil
}
