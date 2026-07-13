// Package api implements the PipeRouter Admin REST API under /api/v1
// (PRD §15) and mounts the embedded WebUI for non-API paths.
//
// Security posture (PRD §15.3): v0.1 has no authentication, so the
// handler never emits CORS headers, rejects mutating requests whose
// Origin header is not same-host, caps request bodies at 1 MiB and sets
// conservative security headers on every response. Errors use the
// envelope {"error":code,"detail":...,"issues":[...]} ("issues" only for
// validation_failed). Nothing sensitive — bodies, query strings, header
// values, proxy URLs — is ever echoed to clients or logs.
package api

import (
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"runtime/debug"
	"strings"

	"github.com/kites262/piperouter/internal/logging"
	"github.com/kites262/piperouter/internal/metrics"
	"github.com/kites262/piperouter/internal/runtime"
)

// maxBodyBytes caps every request body (PRD §15.3).
const maxBodyBytes = 1 << 20 // 1 MiB

// Deps wires the handler to the rest of the process.
type Deps struct {
	Manager *runtime.Manager
	Metrics *metrics.Registry
	Ring    *logging.Ring
	Logger  *slog.Logger
	Version string
	WebUI   http.Handler // nil → no UI mounted, non-API paths get 404 JSON

	// ProxyAddr/AdminAddr are the EFFECTIVE bound listener addresses,
	// which may differ from the config file when CLI flags override them
	// (PRD §21.3). Empty falls back to the config value in /status.
	ProxyAddr string
	AdminAddr string

	// RestrictHost, when true, rejects any request whose Host header is not
	// a loopback name. Set by app when the admin plane is bound to loopback
	// (the default) to defend the unauthenticated API against DNS-rebinding
	// attacks (PRD §15.3, §22.4). Off when admin is deliberately exposed on
	// a non-loopback address, where a fronting proxy forwards arbitrary Host.
	RestrictHost bool
}

// NewHandler builds the admin-plane handler: /api and /api/v1/* serve
// the REST API; everything else is delegated to Deps.WebUI when set.
func NewHandler(d Deps) http.Handler {
	if d.Logger == nil {
		d.Logger = slog.New(slog.DiscardHandler)
	}
	s := &server{deps: d, mux: http.NewServeMux()}
	s.registerRoutes()

	var h http.Handler = http.HandlerFunc(s.dispatch)
	h = maxBytesMiddleware(h)
	h = originCheckMiddleware(h)
	if d.RestrictHost {
		h = hostCheckMiddleware(h)
	}
	h = securityHeadersMiddleware(h)
	h = recoverMiddleware(h, d.Logger)
	return h
}

type server struct {
	deps Deps
	mux  *http.ServeMux
}

func (s *server) registerRoutes() {
	s.mux.HandleFunc("GET /api/v1/status", s.handleStatus)

	s.mux.HandleFunc("GET /api/v1/config", s.handleConfigGet)
	s.mux.HandleFunc("PUT /api/v1/config", s.handleConfigPut)
	s.mux.HandleFunc("POST /api/v1/config/validate", s.handleConfigValidate)

	s.mux.HandleFunc("GET /api/v1/routes", s.handleRouteList)
	s.mux.HandleFunc("POST /api/v1/routes", s.handleRouteCreate)
	s.mux.HandleFunc("GET /api/v1/routes/{name}", s.handleRouteGet)
	s.mux.HandleFunc("PUT /api/v1/routes/{name}", s.handleRouteUpdate)
	s.mux.HandleFunc("DELETE /api/v1/routes/{name}", s.handleRouteDelete)
	s.mux.HandleFunc("GET /api/v1/routes/{name}/metrics", s.handleRouteMetrics)

	s.mux.HandleFunc("GET /api/v1/transports", s.handleTransportList)
	s.mux.HandleFunc("POST /api/v1/transports", s.handleTransportCreate)
	s.mux.HandleFunc("GET /api/v1/transports/{name}", s.handleTransportGet)
	s.mux.HandleFunc("PUT /api/v1/transports/{name}", s.handleTransportUpdate)
	s.mux.HandleFunc("DELETE /api/v1/transports/{name}", s.handleTransportDelete)

	s.mux.HandleFunc("GET /api/v1/metrics", s.handleMetrics)
	s.mux.HandleFunc("GET /api/v1/metrics/history", s.handleMetricsHistory)
	s.mux.HandleFunc("GET /api/v1/logs", s.handleLogs)

	s.mux.HandleFunc("POST /api/v1/diagnostics/request", s.handleDiagnosticsRequest)
	s.mux.HandleFunc("POST /api/v1/diagnostics/route", s.handleDiagnosticsRoute)
	s.mux.HandleFunc("POST /api/v1/diagnostics/transport", s.handleDiagnosticsTransport)
}

// dispatch splits the admin plane: the /api tree is JSON-only (unknown
// paths get 404 JSON, method mismatches 405 JSON); anything else is the
// WebUI when mounted, 404 JSON otherwise.
func (s *server) dispatch(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/") {
		s.serveAPI(w, r)
		return
	}
	if s.deps.WebUI != nil {
		s.deps.WebUI.ServeHTTP(w, r)
		return
	}
	writeError(w, http.StatusNotFound, "not_found", "")
}

func (s *server) serveAPI(w http.ResponseWriter, r *http.Request) {
	h, pattern := s.mux.Handler(r)
	if pattern != "" {
		// Serve through the mux itself so it wires up r.PathValue.
		s.mux.ServeHTTP(w, r)
		return
	}
	// No pattern matched. The mux built-in handler is either a plain-text
	// 404 or a 405 carrying an Allow header (Go 1.22 method patterns).
	// Probe it against a discarding writer and re-emit the outcome as JSON.
	probe := &discardWriter{header: make(http.Header)}
	h.ServeHTTP(probe, r)
	if probe.status == http.StatusMethodNotAllowed {
		if allow := probe.header.Get("Allow"); allow != "" {
			w.Header().Set("Allow", allow)
		}
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "")
		return
	}
	writeError(w, http.StatusNotFound, "not_found", "")
}

// discardWriter captures status and headers of the mux built-in error
// handlers without writing anything to the client.
type discardWriter struct {
	header http.Header
	status int
}

func (d *discardWriter) Header() http.Header { return d.header }

func (d *discardWriter) WriteHeader(code int) {
	if d.status == 0 {
		d.status = code
	}
}

func (d *discardWriter) Write(b []byte) (int, error) {
	if d.status == 0 {
		d.status = http.StatusOK
	}
	return len(b), nil
}

// recoverMiddleware turns a per-request panic into a 500 JSON error
// instead of killing the process (§22.3).
func recoverMiddleware(next http.Handler, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tw := &trackingWriter{ResponseWriter: w}
		defer func() {
			if v := recover(); v != nil {
				logger.Error("api: panic serving request",
					"method", r.Method,
					"path", r.URL.Path, // path only, never the query
					"panic", fmt.Sprint(v),
					"stack", string(debug.Stack()),
				)
				if !tw.wrote {
					writeError(tw, http.StatusInternalServerError, "internal_error", "")
				}
			}
		}()
		next.ServeHTTP(tw, r)
	})
}

// trackingWriter records whether a response has started so the recover
// handler knows if it may still write an error body.
type trackingWriter struct {
	http.ResponseWriter
	wrote bool
}

func (t *trackingWriter) WriteHeader(code int) {
	t.wrote = true
	t.ResponseWriter.WriteHeader(code)
}

func (t *trackingWriter) Write(b []byte) (int, error) {
	t.wrote = true
	return t.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (t *trackingWriter) Unwrap() http.ResponseWriter { return t.ResponseWriter }

// securityHeadersMiddleware sets conservative headers on every response.
// No CORS headers are ever emitted (PRD §15.3).
func securityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

// originCheckMiddleware rejects mutating requests whose Origin header is
// present but not same-host (PRD §15.3). Browsers always attach Origin
// to cross-site POST/PUT/DELETE, so this blocks CSRF against the
// unauthenticated admin plane; non-browser clients omit Origin and pass.
func originCheckMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" && !sameOrigin(origin, r.Host) {
			writeError(w, http.StatusForbidden, "origin_not_allowed",
				"cross-origin mutation requests are not allowed")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// hostCheckMiddleware rejects requests whose Host header is not a loopback
// name (all methods, including GET). Against a loopback-bound admin plane a
// same-origin Origin check is not enough: DNS rebinding lets a remote page
// reach 127.0.0.1 while sending Host: attacker.tld. Requiring a loopback
// Host closes that hole because the browser always sends the attacker's
// hostname, never 127.0.0.1/localhost (PRD §15.3, §22.4).
func hostCheckMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isLoopbackHost(r.Host) {
			writeError(w, http.StatusForbidden, "host_not_allowed",
				"the admin plane only accepts loopback Host headers; use 127.0.0.1 or localhost")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// isLoopbackHost reports whether a Host header names a loopback interface.
// An empty Host (HTTP/1.0 without one) is allowed — a browser, and therefore
// any rebinding attack, always sends a Host.
func isLoopbackHost(host string) bool {
	if host == "" {
		return true
	}
	hostname := host
	if h, _, err := net.SplitHostPort(host); err == nil {
		hostname = h
	}
	if hostname == "localhost" {
		return true
	}
	ip := net.ParseIP(hostname)
	return ip != nil && ip.IsLoopback()
}

// sameOrigin compares the Origin header's host:port with the request
// Host exactly. An unparsable Origin and the literal "null" (opaque
// origin: sandboxed frames, file://, redirects) are rejected.
func sameOrigin(origin, host string) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	return u.Host == host
}

// maxBytesMiddleware caps request bodies at 1 MiB; exceeding it surfaces
// as *http.MaxBytesError from body reads (mapped to 413 invalid_request).
func maxBytesMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
		}
		next.ServeHTTP(w, r)
	})
}
