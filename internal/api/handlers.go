package api

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/kites262/piperouter/internal/config"
	"github.com/kites262/piperouter/internal/diagnostics"
	"github.com/kites262/piperouter/internal/logging"
	"github.com/kites262/piperouter/internal/metrics"
)

// Wire shapes pinned by api/openapi.yaml.

type statusResponse struct {
	Version       string       `json:"version"`
	StartedAt     time.Time    `json:"started_at"`
	UptimeSeconds float64      `json:"uptime_seconds"`
	Proxy         proxyStatus  `json:"proxy"`
	Admin         adminStatus  `json:"admin"`
	Config        configStatus `json:"config"`
}

type proxyStatus struct {
	Listen     string `json:"listen"`
	TLSEnabled bool   `json:"tls_enabled"`
}

type adminStatus struct {
	Listen string `json:"listen"`
}

type configStatus struct {
	Valid     bool      `json:"valid"`
	LastError string    `json:"last_error"`
	Revision  string    `json:"revision"`
	LoadedAt  time.Time `json:"loaded_at"`
	Path      string    `json:"path"`
}

type configEnvelope struct {
	Revision string         `json:"revision"`
	Config   *config.Config `json:"config"`
}

type validateResponse struct {
	Valid  bool     `json:"valid"`
	Issues []string `json:"issues"`
}

type routeListResponse struct {
	Routes []config.RouteConfig `json:"routes"`
}

type routeEnvelope struct {
	Revision string             `json:"revision"`
	Route    config.RouteConfig `json:"route"`
}

type transportListResponse struct {
	Transports []config.TransportConfig `json:"transports"`
}

type transportEnvelope struct {
	Revision  string                 `json:"revision"`
	Transport config.TransportConfig `json:"transport"`
}

type metricsResponse struct {
	metrics.Snapshot
	LogDropped uint64 `json:"log_dropped"`
}

type logsResponse struct {
	Entries  []logging.AccessEntry `json:"entries"`
	Dropped  uint64                `json:"dropped"`
	Capacity int                   `json:"capacity"`
}

type routeUpsertRequest struct {
	Revision string              `json:"revision"`
	Route    *config.RouteConfig `json:"route"`
}

type transportUpsertRequest struct {
	Revision  string                  `json:"revision"`
	Transport *config.TransportConfig `json:"transport"`
}

// builtinDirect is the synthetic list/get entry for the built-in
// transport, which never appears in the configuration itself.
func builtinDirect() config.TransportConfig {
	return config.TransportConfig{Name: config.DirectName, Type: config.TransportDirect, URL: ""}
}

// --- status ---------------------------------------------------------------

func (s *server) handleStatus(w http.ResponseWriter, r *http.Request) {
	snap := s.deps.Manager.Current()
	st := s.deps.Manager.Status()
	ms := s.deps.Metrics.Snapshot()
	// Report the EFFECTIVE bound addresses (honoring CLI overrides, §21.3);
	// fall back to the config values when not wired (e.g. in tests).
	proxyListen := s.deps.ProxyAddr
	if proxyListen == "" {
		proxyListen = snap.Config.Server.Proxy.Listen
	}
	adminListen := s.deps.AdminAddr
	if adminListen == "" {
		adminListen = snap.Config.Server.Admin.Listen
	}
	writeJSON(w, http.StatusOK, statusResponse{
		Version:       s.deps.Version,
		StartedAt:     ms.StartedAt,
		UptimeSeconds: ms.UptimeSeconds,
		Proxy: proxyStatus{
			Listen:     proxyListen,
			TLSEnabled: snap.Config.Server.Proxy.TLS.Enabled,
		},
		Admin: adminStatus{Listen: adminListen},
		Config: configStatus{
			Valid:     st.Valid,
			LastError: st.LastError,
			Revision:  st.Revision,
			LoadedAt:  st.LoadedAt,
			Path:      s.deps.Manager.ConfigPath(),
		},
	})
}

// --- config ---------------------------------------------------------------

func (s *server) handleConfigGet(w http.ResponseWriter, r *http.Request) {
	snap := s.deps.Manager.Current()
	writeJSON(w, http.StatusOK, configEnvelope{Revision: snap.Revision, Config: snap.Config})
}

func (s *server) handleConfigPut(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Revision string         `json:"revision"`
		Config   *config.Config `json:"config"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Config == nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "missing config")
		return
	}
	next, ok := s.apply(w, req.Config, req.Revision)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, configEnvelope{Revision: next.Revision, Config: next.Config})
}

func (s *server) handleConfigValidate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Config *config.Config `json:"config"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Config == nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "missing config")
		return
	}
	// Validate only — never saved, never applied. Invalid CONTENT is still
	// a 200: problems are data, not a request failure (PRD §15.2).
	cfg := req.Config.Clone()
	cfg.Normalize()
	resp := validateResponse{Valid: true, Issues: []string{}}
	// Resolve relative static targets against the live config file directory
	// (same baseDir as Manager.Apply) so validate matches apply semantics.
	if err := config.Validate(cfg, config.ConfigBaseDir(s.deps.Manager.ConfigPath())); err != nil {
		resp.Valid = false
		var ve *config.ValidationError
		if errors.As(err, &ve) {
			resp.Issues = append(resp.Issues, ve.Issues...)
		} else {
			resp.Issues = append(resp.Issues, err.Error())
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// --- routes ---------------------------------------------------------------

func (s *server) handleRouteList(w http.ResponseWriter, r *http.Request) {
	routes := s.deps.Manager.Current().Config.Routes
	if routes == nil {
		routes = []config.RouteConfig{}
	}
	writeJSON(w, http.StatusOK, routeListResponse{Routes: routes})
}

func (s *server) handleRouteGet(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	cfg := s.deps.Manager.Current().Config
	idx := findRouteIndex(cfg.Routes, name)
	if idx < 0 {
		writeError(w, http.StatusNotFound, "not_found", "no route named "+strconv.Quote(name))
		return
	}
	writeJSON(w, http.StatusOK, cfg.Routes[idx])
}

func (s *server) handleRouteCreate(w http.ResponseWriter, r *http.Request) {
	var req routeUpsertRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Route == nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "missing route")
		return
	}
	next, ok := s.applyUpdate(w, req.Revision, func(cfg *config.Config) error {
		cfg.Routes = append(cfg.Routes, *req.Route) // duplicates → validation_failed
		return nil
	})
	if !ok {
		return
	}
	idx := findRouteIndex(next.Config.Routes, req.Route.Name)
	if idx < 0 {
		writeError(w, http.StatusInternalServerError, "internal_error", "")
		return
	}
	writeJSON(w, http.StatusCreated, routeEnvelope{Revision: next.Revision, Route: next.Config.Routes[idx]})
}

func (s *server) handleRouteUpdate(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var req routeUpsertRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Route == nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "missing route")
		return
	}
	if req.Route.Name != name {
		writeValidationFailed(w, []string{
			"route.name " + strconv.Quote(req.Route.Name) + " must equal the path name " + strconv.Quote(name),
		})
		return
	}
	next, ok := s.applyUpdate(w, req.Revision, func(cfg *config.Config) error {
		idx := findRouteIndex(cfg.Routes, name)
		if idx < 0 {
			return &mutationError{http.StatusNotFound, "not_found", "no route named " + strconv.Quote(name)}
		}
		cfg.Routes[idx] = *req.Route
		return nil
	})
	if !ok {
		return
	}
	idx := findRouteIndex(next.Config.Routes, name)
	if idx < 0 {
		writeError(w, http.StatusInternalServerError, "internal_error", "")
		return
	}
	writeJSON(w, http.StatusOK, routeEnvelope{Revision: next.Revision, Route: next.Config.Routes[idx]})
}

func (s *server) handleRouteDelete(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	revision, ok := decodeOptionalRevision(w, r)
	if !ok {
		return
	}
	if _, ok := s.applyUpdate(w, revision, func(cfg *config.Config) error {
		idx := findRouteIndex(cfg.Routes, name)
		if idx < 0 {
			return &mutationError{http.StatusNotFound, "not_found", "no route named " + strconv.Quote(name)}
		}
		cfg.Routes = append(cfg.Routes[:idx], cfg.Routes[idx+1:]...)
		return nil
	}); !ok {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleRouteMetrics(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	cfg := s.deps.Manager.Current().Config
	if findRouteIndex(cfg.Routes, name) < 0 {
		writeError(w, http.StatusNotFound, "not_found", "no route named "+strconv.Quote(name))
		return
	}
	// Disabled routes are configured but carry no metric series — report
	// zeroes rather than 404 (the route exists).
	rs, ok := s.deps.Metrics.RouteSnapshot(name)
	if !ok {
		rs = metrics.RouteSnapshot{Name: name}
	}
	writeJSON(w, http.StatusOK, rs)
}

// --- transports -----------------------------------------------------------

func (s *server) handleTransportList(w http.ResponseWriter, r *http.Request) {
	cfg := s.deps.Manager.Current().Config
	list := make([]config.TransportConfig, 0, len(cfg.Transports)+1)
	list = append(list, builtinDirect()) // synthetic built-in first
	list = append(list, cfg.Transports...)
	writeJSON(w, http.StatusOK, transportListResponse{Transports: list})
}

func (s *server) handleTransportGet(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == config.DirectName {
		writeJSON(w, http.StatusOK, builtinDirect())
		return
	}
	cfg := s.deps.Manager.Current().Config
	idx := findTransportIndex(cfg.Transports, name)
	if idx < 0 {
		writeError(w, http.StatusNotFound, "not_found", "no transport named "+strconv.Quote(name))
		return
	}
	writeJSON(w, http.StatusOK, cfg.Transports[idx])
}

func (s *server) handleTransportCreate(w http.ResponseWriter, r *http.Request) {
	var req transportUpsertRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Transport == nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "missing transport")
		return
	}
	next, ok := s.applyUpdate(w, req.Revision, func(cfg *config.Config) error {
		cfg.Transports = append(cfg.Transports, *req.Transport) // duplicate or "direct" → validation_failed
		return nil
	})
	if !ok {
		return
	}
	idx := findTransportIndex(next.Config.Transports, req.Transport.Name)
	if idx < 0 {
		writeError(w, http.StatusInternalServerError, "internal_error", "")
		return
	}
	writeJSON(w, http.StatusCreated, transportEnvelope{Revision: next.Revision, Transport: next.Config.Transports[idx]})
}

func (s *server) handleTransportUpdate(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == config.DirectName {
		writeError(w, http.StatusForbidden, "builtin_transport", "the built-in direct transport cannot be updated")
		return
	}
	var req transportUpsertRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Transport == nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "missing transport")
		return
	}
	if req.Transport.Name != name {
		writeValidationFailed(w, []string{
			"transport.name " + strconv.Quote(req.Transport.Name) + " must equal the path name " + strconv.Quote(name),
		})
		return
	}
	next, ok := s.applyUpdate(w, req.Revision, func(cfg *config.Config) error {
		idx := findTransportIndex(cfg.Transports, name)
		if idx < 0 {
			return &mutationError{http.StatusNotFound, "not_found", "no transport named " + strconv.Quote(name)}
		}
		cfg.Transports[idx] = *req.Transport
		return nil
	})
	if !ok {
		return
	}
	idx := findTransportIndex(next.Config.Transports, name)
	if idx < 0 {
		writeError(w, http.StatusInternalServerError, "internal_error", "")
		return
	}
	writeJSON(w, http.StatusOK, transportEnvelope{Revision: next.Revision, Transport: next.Config.Transports[idx]})
}

func (s *server) handleTransportDelete(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == config.DirectName {
		writeError(w, http.StatusForbidden, "builtin_transport", "the built-in direct transport cannot be deleted")
		return
	}
	revision, ok := decodeOptionalRevision(w, r)
	if !ok {
		return
	}
	if _, ok := s.applyUpdate(w, revision, func(cfg *config.Config) error {
		idx := findTransportIndex(cfg.Transports, name)
		if idx < 0 {
			return &mutationError{http.StatusNotFound, "not_found", "no transport named " + strconv.Quote(name)}
		}
		// Friendly pre-check: deleting a referenced transport would only
		// fail validation later; report the referencing routes instead
		// (PRD §15.2). Evaluated under the mutation lock so the reference
		// set matches the config actually being written.
		var refs []string
		for i := range cfg.Routes {
			if cfg.Routes[i].Transport == name {
				refs = append(refs, cfg.Routes[i].Name)
			}
		}
		if len(refs) > 0 {
			return &mutationError{http.StatusConflict, "transport_in_use",
				"transport is referenced by routes: " + strings.Join(refs, ", ")}
		}
		cfg.Transports = append(cfg.Transports[:idx], cfg.Transports[idx+1:]...)
		return nil
	}); !ok {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- metrics & logs -------------------------------------------------------

func (s *server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	var dropped uint64
	if s.deps.Ring != nil {
		dropped = s.deps.Ring.Dropped()
	}
	writeJSON(w, http.StatusOK, metricsResponse{
		Snapshot:   s.deps.Metrics.Snapshot(),
		LogDropped: dropped,
	})
}

func (s *server) handleMetricsHistory(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.deps.Metrics.History())
}

func (s *server) handleLogs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	// Per api/openapi.yaml: an omitted or non-positive limit returns all
	// buffered entries (already bounded by the ring capacity, §22.2); a
	// positive limit caps the result. 0 = "all" to Ring.Snapshot.
	limit := 0
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", "limit must be an integer")
			return
		}
		if n > 0 {
			limit = n
		}
	}

	statusClass := q.Get("status_class")
	switch statusClass {
	case "", "2xx", "3xx", "4xx", "5xx", "error":
	default:
		writeError(w, http.StatusBadRequest, "invalid_request",
			"status_class must be one of 2xx, 3xx, 4xx, 5xx, error")
		return
	}

	resp := logsResponse{Entries: []logging.AccessEntry{}}
	if s.deps.Ring != nil {
		resp.Entries = s.deps.Ring.Snapshot(limit, q.Get("route"), statusClass)
		resp.Dropped = s.deps.Ring.Dropped()
		resp.Capacity = s.deps.Ring.Capacity()
	}
	writeJSON(w, http.StatusOK, resp)
}

// --- diagnostics ----------------------------------------------------------

func (s *server) handleDiagnosticsRequest(w http.ResponseWriter, r *http.Request) {
	var req diagnostics.RequestTest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Path != "" && !strings.HasPrefix(req.Path, "/") {
		writeError(w, http.StatusBadRequest, "invalid_request", `path must be empty or start with "/"`)
		return
	}
	if !diagnostics.AllowedMethod(req.Method) {
		writeError(w, http.StatusBadRequest, "invalid_request", "method must be GET, HEAD or POST")
		return
	}
	snap := s.deps.Manager.Current()
	writeJSON(w, http.StatusOK, diagnostics.TestRequest(r.Context(), snap, req))
}

func (s *server) handleDiagnosticsRoute(w http.ResponseWriter, r *http.Request) {
	var req diagnostics.RouteTest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Route == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "missing route")
		return
	}
	snap := s.deps.Manager.Current()
	// Unknown to the CONFIG → 404. A configured-but-disabled route still
	// runs and reports a resolve-stage failure in the 200 result.
	if findRouteIndex(snap.Config.Routes, req.Route) < 0 {
		writeError(w, http.StatusNotFound, "not_found", "no route named "+strconv.Quote(req.Route))
		return
	}
	if !diagnostics.AllowedMethod(req.Method) {
		writeError(w, http.StatusBadRequest, "invalid_request", "method must be GET, HEAD or POST")
		return
	}
	if req.Path != "" && !strings.HasPrefix(req.Path, "/") {
		writeError(w, http.StatusBadRequest, "invalid_request", `path must be empty or start with "/"`)
		return
	}
	writeJSON(w, http.StatusOK, diagnostics.TestRoute(r.Context(), snap, req))
}

func (s *server) handleDiagnosticsTransport(w http.ResponseWriter, r *http.Request) {
	var req diagnostics.TransportTest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Transport == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "missing transport")
		return
	}
	snap := s.deps.Manager.Current()
	if _, ok := snap.Pool.Get(req.Transport); !ok {
		writeError(w, http.StatusNotFound, "not_found", "no transport named "+strconv.Quote(req.Transport))
		return
	}
	if u, err := url.Parse(req.URL); err != nil || !u.IsAbs() || u.Host == "" ||
		(u.Scheme != "http" && u.Scheme != "https") {
		writeError(w, http.StatusBadRequest, "invalid_request", "url must be an absolute http or https URL")
		return
	}
	if !diagnostics.AllowedMethod(req.Method) {
		writeError(w, http.StatusBadRequest, "invalid_request", "method must be GET, HEAD or POST")
		return
	}
	writeJSON(w, http.StatusOK, diagnostics.TestTransport(r.Context(), snap, req))
}
