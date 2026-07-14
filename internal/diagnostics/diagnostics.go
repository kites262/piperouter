// Package diagnostics executes connectivity probes (PRD §16):
//
//   - RequestTest — simulate an inbound data-plane request (path match →
//     rewrite → transport), the Diagnostics console use case.
//   - RouteTest   — probe a named route end-to-end (Routes page "Test").
//   - TransportTest — probe an absolute URL through a named transport
//     (Transports page "Test").
//
// Each probe uses the real pipeline pieces (route table rewrite, transport
// pool RoundTripper) with a bounded overall timeout, no redirect following,
// and at most 64 KiB of the response body read and discarded. Bodies and
// authentication header values are never stored or logged; proxy URLs are
// redacted from error messages.
package diagnostics

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/kites262/piperouter/internal/router"
	"github.com/kites262/piperouter/internal/runtime"
	"github.com/kites262/piperouter/internal/transport"
)

// Timeout bounds one whole probe: connect, TLS handshake, response
// headers and body drain (PRD §16).
const Timeout = 15 * time.Second

// maxBodyRead caps how much response body a probe drains before closing.
const maxBodyRead int64 = 64 << 10

// Error stages reported in Result.ErrorStage.
const (
	StageResolve  = "resolve"  // probe request could not be built (route/transport/path/method)
	StageConnect  = "connect"  // DNS, TCP dial or proxy CONNECT failed
	StageTLS      = "tls"      // TLS handshake with the target failed
	StageResponse = "response" // connected, but no usable HTTP response arrived
)

// RequestTest asks for a probe of a full inbound request path, matched
// against the live route table exactly like the data plane (longest-prefix,
// path-segment boundary). Path must be empty or start with "/"; empty is
// treated as "/". Method defaults to GET; only GET, HEAD and POST are
// allowed. Probes never carry a request body (PRD §16.1).
type RequestTest struct {
	Path   string `json:"path"`
	Method string `json:"method"`
}

// RouteTest asks for a probe of a configured route by name. Path is an
// extra path appended below the route prefix ("" or starting with "/");
// the synthetic request path prefix+path then runs through the real rewrite.
// Method defaults to GET; only GET, HEAD and POST are allowed. Probes
// never carry a request body (PRD §16.1).
type RouteTest struct {
	Route  string `json:"route"`
	Path   string `json:"path"`
	Method string `json:"method"`
}

// TransportTest asks for a probe of an absolute http/https URL through a
// named transport; "direct" addresses the built-in transport (PRD §16.2).
type TransportTest struct {
	Transport string `json:"transport"`
	URL       string `json:"url"`
	Method    string `json:"method"`
}

// Result is the outcome of one probe. Receiving an upstream HTTP
// response of any status — explicitly including 401, 403 and 404 —
// means the network link works, so OK is true (PRD §16.1).
type Result struct {
	OK               bool    `json:"ok"`
	Route            string  `json:"route"` // matched/named route; empty when N/A
	TargetURL        string  `json:"target_url"`
	Transport        string  `json:"transport"`
	Status           int     `json:"status"`      // 0 if no HTTP response
	ErrorStage       string  `json:"error_stage"` // ""|"resolve"|"connect"|"tls"|"response"
	Error            string  `json:"error"`       // sanitized message, "" on success
	HeaderDurationMs float64 `json:"header_duration_ms"`
	TotalDurationMs  float64 `json:"total_duration_ms"`
}

// AllowedMethod reports whether m may be used for a probe: empty
// (defaults to GET), GET, HEAD or POST, case-insensitive.
func AllowedMethod(m string) bool {
	_, ok := normalizeMethod(m)
	return ok
}

func normalizeMethod(m string) (string, bool) {
	switch strings.ToUpper(strings.TrimSpace(m)) {
	case "", http.MethodGet:
		return http.MethodGet, true
	case http.MethodHead:
		return http.MethodHead, true
	case http.MethodPost:
		return http.MethodPost, true
	default:
		return "", false
	}
}

// TestRequest probes as if a client hit the data plane: Match on the
// request path (escaped, segment-boundary longest-prefix), then rewrite
// and forward through the matched route's transport. A path that matches
// no enabled route is a resolve-stage failure.
func TestRequest(ctx context.Context, snap *runtime.Snapshot, t RequestTest) Result {
	method, ok := normalizeMethod(t.Method)
	if !ok {
		return resolveFailure("", "", "", fmt.Sprintf("unsupported method %q (allowed: GET, HEAD, POST)", t.Method))
	}
	if snap == nil || snap.Table == nil || snap.Pool == nil {
		return resolveFailure("", "", "", "no active configuration")
	}

	path := t.Path
	if path == "" {
		path = "/"
	}
	if !strings.HasPrefix(path, "/") {
		return resolveFailure("", "", "", `path must start with "/"`)
	}
	reqURL, err := requestURL(path)
	if err != nil {
		return resolveFailure("", "", "", err.Error())
	}

	route := snap.Table.Match(reqURL.EscapedPath())
	if route == nil {
		return resolveFailure("", "", "", "no route matched")
	}
	return probeRoute(ctx, snap, route, method, reqURL)
}

// TestRoute probes a route end-to-end: it resolves the ENABLED route by
// name from the snapshot's table (a disabled or unknown route is a
// resolve-stage failure), builds the synthetic request path
// prefix+Path, rewrites it exactly like the data plane would and sends
// the probe through the route's transport entry.
func TestRoute(ctx context.Context, snap *runtime.Snapshot, t RouteTest) Result {
	method, ok := normalizeMethod(t.Method)
	if !ok {
		return resolveFailure("", "", "", fmt.Sprintf("unsupported method %q (allowed: GET, HEAD, POST)", t.Method))
	}
	if snap == nil || snap.Table == nil || snap.Pool == nil {
		return resolveFailure("", "", "", "no active configuration")
	}

	var route *router.Route
	for _, r := range snap.Table.Routes() {
		if r.Name == t.Route {
			route = r
			break
		}
	}
	if route == nil {
		return resolveFailure("", "", "", "route disabled or not found")
	}

	if t.Path != "" && !strings.HasPrefix(t.Path, "/") {
		return resolveFailure(route.Name, "", route.TransportName, `path must be empty or start with "/"`)
	}
	if route.Exact && t.Path != "" {
		// The data plane would never route prefix+extra to this route, so a
		// probe of it would be a lie about connectivity.
		return resolveFailure(route.Name, "", route.TransportName,
			"route matches its prefix exactly; an appended path never matches")
	}
	// The synthetic request path is prefix+path; the root prefix "/" is
	// trimmed first so it never produces a spurious "//" head.
	syntheticPath := strings.TrimSuffix(route.Prefix, "/") + t.Path
	if syntheticPath == "" {
		syntheticPath = "/"
	}
	reqURL, err := requestURL(syntheticPath)
	if err != nil {
		return resolveFailure(route.Name, "", route.TransportName, err.Error())
	}
	return probeRoute(ctx, snap, route, method, reqURL)
}

// TestTransport probes an absolute http/https URL through the named
// transport entry of the snapshot's pool.
func TestTransport(ctx context.Context, snap *runtime.Snapshot, t TransportTest) Result {
	method, ok := normalizeMethod(t.Method)
	if !ok {
		return resolveFailure("", "", t.Transport, fmt.Sprintf("unsupported method %q (allowed: GET, HEAD, POST)", t.Method))
	}
	if snap == nil || snap.Pool == nil {
		return resolveFailure("", "", t.Transport, "no active configuration")
	}
	entry, found := snap.Pool.Get(t.Transport)
	if !found {
		return resolveFailure("", "", t.Transport, "transport not found")
	}
	u, err := url.Parse(t.URL)
	if err != nil || !u.IsAbs() || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return resolveFailure("", "", t.Transport, "url must be an absolute http or https URL")
	}
	if u.User != nil {
		return resolveFailure("", "", t.Transport, "url must not contain userinfo")
	}
	return probe(ctx, entry, method, u)
}

// requestURL builds a path-only URL from an escaped path string without
// treating a leading "//" as an authority (never url.Parse on the raw path).
func requestURL(escapedPath string) (*url.URL, error) {
	unescaped, err := url.PathUnescape(escapedPath)
	if err != nil {
		return nil, errors.New("path contains an invalid percent escape")
	}
	u := &url.URL{Path: unescaped}
	if unescaped != escapedPath {
		u.RawPath = escapedPath
	}
	return u, nil
}

func probeRoute(ctx context.Context, snap *runtime.Snapshot, route *router.Route, method string, reqURL *url.URL) Result {
	if route.IsStatic() {
		return probeStatic(route)
	}
	target := route.Rewrite(reqURL)
	entry, found := snap.Pool.Get(route.TransportName)
	if !found {
		return resolveFailure(route.Name, target.String(), route.TransportName,
			fmt.Sprintf("transport %q not found", route.TransportName))
	}
	res := probe(ctx, entry, method, target)
	res.Route = route.Name
	return res
}

// probeStatic checks that the configured file is present and a regular file.
// No HTTP round-trip is performed; TargetURL is the absolute file path.
func probeStatic(route *router.Route) Result {
	res := Result{Route: route.Name, TargetURL: route.File}
	fi, err := os.Stat(route.File)
	if err != nil {
		res.ErrorStage = StageConnect
		if os.IsNotExist(err) {
			res.Error = "static file not found"
		} else {
			res.Error = "static file not accessible"
		}
		return res
	}
	if fi.IsDir() || !fi.Mode().IsRegular() {
		res.ErrorStage = StageResolve
		res.Error = "static target is not a regular file"
		return res
	}
	res.OK = true
	res.Status = http.StatusOK
	return res
}

func resolveFailure(routeName, targetURL, transportName, msg string) Result {
	return Result{
		Route:      routeName,
		TargetURL:  targetURL,
		Transport:  transportName,
		ErrorStage: StageResolve,
		Error:      msg,
	}
}

// traceState collects httptrace signals for error-stage attribution.
// Callbacks may fire on transport-internal goroutines, hence the mutex.
type traceState struct {
	mu         sync.Mutex
	gotConn    bool // a connection (new or pooled) was obtained
	tlsStarted bool
	tlsDone    bool // TLS handshake completed successfully
	firstByte  time.Time
}

func (s *traceState) update(fn func(*traceState)) {
	s.mu.Lock()
	fn(s)
	s.mu.Unlock()
}

// stage attributes a RoundTrip error to the phase it happened in.
func (s *traceState) stage() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case s.tlsStarted && !s.tlsDone:
		return StageTLS
	case !s.gotConn:
		return StageConnect
	default:
		return StageResponse
	}
}

func (s *traceState) firstByteTime() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.firstByte
}

// probe sends one bodyless request for target through entry, never
// following redirects, and drains at most maxBodyRead of the response.
// Callers that know a route name set Result.Route after probe returns.
func probe(ctx context.Context, entry *transport.Entry, method string, target *url.URL) Result {
	res := Result{TargetURL: target.String(), Transport: entry.Name}

	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()

	st := &traceState{}
	trace := &httptrace.ClientTrace{
		GotConn: func(httptrace.GotConnInfo) {
			st.update(func(s *traceState) { s.gotConn = true })
		},
		TLSHandshakeStart: func() {
			st.update(func(s *traceState) { s.tlsStarted = true })
		},
		TLSHandshakeDone: func(_ tls.ConnectionState, err error) {
			if err == nil {
				st.update(func(s *traceState) { s.tlsDone = true })
			}
		},
		GotFirstResponseByte: func() {
			now := time.Now()
			st.update(func(s *traceState) { s.firstByte = now })
		},
	}

	req, err := http.NewRequestWithContext(httptrace.WithClientTrace(ctx, trace), method, target.String(), nil)
	if err != nil {
		res.ErrorStage = StageResolve
		res.Error = "failed to build probe request"
		return res
	}

	client := &http.Client{
		Transport: entry.RoundTripper,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse // report redirects, never follow them
		},
	}

	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		res.TotalDurationMs = msSince(start)
		res.ErrorStage = st.stage()
		res.Error = sanitizeError(err, entry.ProxyURL)
		return res
	}
	defer resp.Body.Close()

	headerAt := st.firstByteTime()
	if headerAt.IsZero() {
		headerAt = time.Now()
	}
	res.HeaderDurationMs = float64(headerAt.Sub(start)) / float64(time.Millisecond)
	res.Status = resp.StatusCode

	// Drain a bounded slice of the body so the connection can be reused;
	// the content itself is never stored or logged (PRD §16.2). A body
	// read error does not flip OK: the HTTP link was established.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxBodyRead))
	res.TotalDurationMs = msSince(start)
	res.OK = true
	return res
}

func msSince(t time.Time) float64 {
	return float64(time.Since(t)) / float64(time.Millisecond)
}

// sanitizeError renders err for the client without ever exposing the
// proxy URL (credentials/topology, §23.12); the target URL is already
// part of the result.
func sanitizeError(err error, proxyURL string) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "probe timed out"
	}
	if errors.Is(err, context.Canceled) {
		return "probe canceled"
	}
	var ue *url.Error
	if errors.As(err, &ue) && ue.Err != nil {
		err = ue.Err // drop the "METHOD url:" wrapper
	}
	msg := err.Error()
	if proxyURL != "" {
		msg = strings.ReplaceAll(msg, proxyURL, "[proxy]")
		if u, perr := url.Parse(proxyURL); perr == nil && u.Host != "" {
			msg = strings.ReplaceAll(msg, u.Host, "[proxy]")
		}
	}
	const maxLen = 200
	if len(msg) > maxLen {
		msg = msg[:maxLen]
	}
	return msg
}
