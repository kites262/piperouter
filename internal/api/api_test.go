package api_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kites262/piperouter/internal/api"
	"github.com/kites262/piperouter/internal/config"
	"github.com/kites262/piperouter/internal/logging"
	"github.com/kites262/piperouter/internal/metrics"
	"github.com/kites262/piperouter/internal/runtime"
)

const testConfigYAML = `version: v0.3
transports:
  - name: t1
    type: http
    url: http://127.0.0.1:18080
routes:
  - name: alpha
    prefix: /alpha
    options:
      target: https://alpha.example.com
  - name: beta
    prefix: /beta
    options:
      target: https://beta.example.com/v1
      transport: t1
`

type testEnv struct {
	srv     *httptest.Server
	manager *runtime.Manager
	reg     *metrics.Registry
	ring    *logging.Ring
	cfgPath string
}

// newTestEnv spins a REAL runtime.Manager on a temp config file behind a
// real api handler served by httptest. mutate lets a test adjust Deps
// (e.g. attach a WebUI or swap the ring) before the handler is built.
func newTestEnv(t *testing.T, mutate ...func(*api.Deps)) *testEnv {
	t.Helper()
	cfgPath := filepath.Join(t.TempDir(), "piperouter.yaml")
	if err := os.WriteFile(cfgPath, []byte(testConfigYAML), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	logger := slog.New(slog.DiscardHandler)
	reg := metrics.NewRegistry()
	mgr, err := runtime.NewManager(cfgPath, logger, reg)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	env := &testEnv{manager: mgr, reg: reg, ring: logging.NewRing(64), cfgPath: cfgPath}
	deps := api.Deps{
		Manager: mgr,
		Metrics: reg,
		Ring:    env.ring,
		Logger:  logger,
		Version: "test-1.0",
	}
	for _, fn := range mutate {
		fn(&deps)
	}
	env.ring = deps.Ring
	env.srv = httptest.NewServer(api.NewHandler(deps))
	t.Cleanup(env.srv.Close)
	return env
}

type header [2]string

// do sends one request. body may be nil, a raw string/[]byte, or any
// JSON-marshalable value.
func (e *testEnv) do(t *testing.T, method, path string, body any, headers ...header) (*http.Response, []byte) {
	t.Helper()
	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		rd = strings.NewReader(b)
	case []byte:
		rd = bytes.NewReader(b)
	default:
		buf, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request body: %v", err)
		}
		rd = bytes.NewReader(buf)
	}
	req, err := http.NewRequest(method, e.srv.URL+path, rd)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	for _, h := range headers {
		req.Header.Set(h[0], h[1])
	}
	resp, err := e.srv.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	return resp, data
}

func mustJSON(t *testing.T, data []byte, dst any) {
	t.Helper()
	if err := json.Unmarshal(data, dst); err != nil {
		t.Fatalf("unmarshal %q: %v", data, err)
	}
}

func loadFile(t *testing.T, path string) *config.Config {
	t.Helper()
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("reload config file: %v", err)
	}
	return cfg
}

type errBody struct {
	Error  string   `json:"error"`
	Detail string   `json:"detail"`
	Issues []string `json:"issues"`
}

func assertError(t *testing.T, resp *http.Response, body []byte, status int, code string) errBody {
	t.Helper()
	if resp.StatusCode != status {
		t.Fatalf("status = %d, want %d (body %s)", resp.StatusCode, status, body)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var e errBody
	mustJSON(t, body, &e)
	if e.Error != code {
		t.Errorf("error code = %q, want %q (body %s)", e.Error, code, body)
	}
	return e
}

// --- status & headers -------------------------------------------------------

func TestStatusEndpoint(t *testing.T) {
	env := newTestEnv(t)
	resp, body := env.do(t, "GET", "/api/v1/status", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", resp.StatusCode, body)
	}
	var st struct {
		Version       string    `json:"version"`
		StartedAt     time.Time `json:"started_at"`
		UptimeSeconds float64   `json:"uptime_seconds"`
		Proxy         struct {
			Listen     string `json:"listen"`
			TLSEnabled bool   `json:"tls_enabled"`
		} `json:"proxy"`
		Admin struct {
			Listen string `json:"listen"`
		} `json:"admin"`
		Config struct {
			Valid     bool      `json:"valid"`
			LastError string    `json:"last_error"`
			Revision  string    `json:"revision"`
			LoadedAt  time.Time `json:"loaded_at"`
			Path      string    `json:"path"`
		} `json:"config"`
	}
	mustJSON(t, body, &st)
	if st.Version != "test-1.0" {
		t.Errorf("version = %q", st.Version)
	}
	if st.Proxy.Listen != ":8080" || st.Proxy.TLSEnabled {
		t.Errorf("proxy = %+v, want default listen and no TLS", st.Proxy)
	}
	if st.Admin.Listen != "127.0.0.1:9090" {
		t.Errorf("admin.listen = %q", st.Admin.Listen)
	}
	if !st.Config.Valid || st.Config.LastError != "" {
		t.Errorf("config status = %+v, want valid", st.Config)
	}
	if want := env.manager.Current().Revision; st.Config.Revision != want {
		t.Errorf("config.revision = %q, want %q", st.Config.Revision, want)
	}
	if st.Config.Path != env.cfgPath {
		t.Errorf("config.path = %q, want %q", st.Config.Path, env.cfgPath)
	}
	if st.StartedAt.IsZero() || st.UptimeSeconds < 0 {
		t.Errorf("started_at/uptime not populated: %v / %v", st.StartedAt, st.UptimeSeconds)
	}
}

func TestSecurityHeadersAndNoCORS(t *testing.T) {
	env := newTestEnv(t)
	resp, _ := env.do(t, "GET", "/api/v1/status", nil)
	for k, want := range map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "no-referrer",
	} {
		if got := resp.Header.Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	if v := resp.Header.Get("Access-Control-Allow-Origin"); v != "" {
		t.Errorf("CORS header emitted: %q", v)
	}
}

// --- config -----------------------------------------------------------------

func TestConfigGetAndPut(t *testing.T) {
	env := newTestEnv(t)

	resp, body := env.do(t, "GET", "/api/v1/config", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET config = %d (%s)", resp.StatusCode, body)
	}
	var envl struct {
		Revision string         `json:"revision"`
		Config   *config.Config `json:"config"`
	}
	mustJSON(t, body, &envl)
	oldRev := env.manager.Current().Revision
	if envl.Revision != oldRev {
		t.Fatalf("revision = %q, want %q", envl.Revision, oldRev)
	}
	if envl.Config == nil || envl.Config.Version != config.SupportedVersion || len(envl.Config.Routes) != 2 {
		t.Fatalf("unexpected config payload: %+v", envl.Config)
	}

	// Full replace with the matching revision succeeds and returns the NEW revision.
	envl.Config.Runtime.LogLevel = "debug"
	resp, body = env.do(t, "PUT", "/api/v1/config",
		map[string]any{"revision": envl.Revision, "config": envl.Config})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT config = %d (%s)", resp.StatusCode, body)
	}
	var updated struct {
		Revision string         `json:"revision"`
		Config   *config.Config `json:"config"`
	}
	mustJSON(t, body, &updated)
	if updated.Revision == oldRev || updated.Revision != env.manager.Current().Revision {
		t.Errorf("new revision = %q (old %q, manager %q)", updated.Revision, oldRev, env.manager.Current().Revision)
	}
	if updated.Config.Runtime.LogLevel != "debug" {
		t.Errorf("returned config log_level = %q, want debug", updated.Config.Runtime.LogLevel)
	}
	// The file is the source of truth: it must actually be rewritten.
	if onDisk := loadFile(t, env.cfgPath); onDisk.Runtime.LogLevel != "debug" {
		t.Errorf("file log_level = %q, want debug", onDisk.Runtime.LogLevel)
	}

	// Stale revision → 409, nothing applied.
	envl.Config.Runtime.LogLevel = "warn"
	resp, body = env.do(t, "PUT", "/api/v1/config",
		map[string]any{"revision": oldRev, "config": envl.Config})
	assertError(t, resp, body, http.StatusConflict, "revision_conflict")
	if onDisk := loadFile(t, env.cfgPath); onDisk.Runtime.LogLevel != "debug" {
		t.Errorf("file changed after 409: log_level = %q", onDisk.Runtime.LogLevel)
	}

	// Invalid content → 400 validation_failed with issues; file untouched.
	bad := envl.Config.Clone()
	bad.Routes[0].Prefix = "no-slash"
	resp, body = env.do(t, "PUT", "/api/v1/config", map[string]any{"config": bad})
	e := assertError(t, resp, body, http.StatusBadRequest, "validation_failed")
	if len(e.Issues) == 0 {
		t.Error("validation_failed without issues")
	}
	if onDisk := loadFile(t, env.cfgPath); onDisk.Routes[0].Prefix != "/alpha" {
		t.Errorf("file changed after validation failure: %q", onDisk.Routes[0].Prefix)
	}

	// Malformed body / missing config.
	resp, body = env.do(t, "PUT", "/api/v1/config", "{not json")
	assertError(t, resp, body, http.StatusBadRequest, "invalid_request")
	resp, body = env.do(t, "PUT", "/api/v1/config", map[string]any{"revision": ""})
	assertError(t, resp, body, http.StatusBadRequest, "invalid_request")
}

func TestConfigValidateEndpoint(t *testing.T) {
	env := newTestEnv(t)

	valid := map[string]any{"config": map[string]any{"version": string(config.SupportedVersion)}}
	resp, body := env.do(t, "POST", "/api/v1/config/validate", valid)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("validate(valid) = %d (%s)", resp.StatusCode, body)
	}
	if !bytes.Contains(body, []byte(`"issues":[]`)) {
		t.Errorf("valid response must carry an empty issues array, got %s", body)
	}
	var vr struct {
		Valid  bool     `json:"valid"`
		Issues []string `json:"issues"`
	}
	mustJSON(t, body, &vr)
	if !vr.Valid || len(vr.Issues) != 0 {
		t.Errorf("got %+v, want valid with no issues", vr)
	}

	// Invalid CONTENT is still a 200 — never a request error.
	invalid := map[string]any{"config": map[string]any{
		"version": "v9.9",
		"routes": []map[string]any{
			{"name": "x", "prefix": "bad", "options": map[string]any{"target": "not-a-url"}},
		},
	}}
	resp, body = env.do(t, "POST", "/api/v1/config/validate", invalid)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("validate(invalid) = %d (%s)", resp.StatusCode, body)
	}
	mustJSON(t, body, &vr)
	if vr.Valid || len(vr.Issues) < 2 {
		t.Errorf("got %+v, want invalid with several issues", vr)
	}

	// Validation never persists or applies anything.
	if got := loadFile(t, env.cfgPath).Version; got != config.SupportedVersion {
		t.Errorf("file version = %q after validate", got)
	}

	resp, body = env.do(t, "POST", "/api/v1/config/validate", "###")
	assertError(t, resp, body, http.StatusBadRequest, "invalid_request")
}

// --- routes -----------------------------------------------------------------

func TestRouteCRUD(t *testing.T) {
	env := newTestEnv(t)

	// List, config order.
	resp, body := env.do(t, "GET", "/api/v1/routes", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list = %d (%s)", resp.StatusCode, body)
	}
	var list struct {
		Routes []config.RouteConfig `json:"routes"`
	}
	mustJSON(t, body, &list)
	if len(list.Routes) != 2 || list.Routes[0].Name != "alpha" || list.Routes[1].Name != "beta" {
		t.Fatalf("routes = %+v", list.Routes)
	}

	// Single GET is the bare RouteConfig (openapi.yaml), not an envelope.
	resp, body = env.do(t, "GET", "/api/v1/routes/alpha", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get = %d (%s)", resp.StatusCode, body)
	}
	var raw map[string]any
	mustJSON(t, body, &raw)
	if raw["name"] != "alpha" {
		t.Errorf("get route name = %v", raw["name"])
	}
	if _, hasRev := raw["revision"]; hasRev {
		t.Errorf("single GET must be a bare RouteConfig, got envelope: %s", body)
	}

	resp, body = env.do(t, "GET", "/api/v1/routes/ghost", nil)
	assertError(t, resp, body, http.StatusNotFound, "not_found")

	// Create with revision check; envelope + normalization + file rewrite.
	rev := env.manager.Current().Revision
	resp, body = env.do(t, "POST", "/api/v1/routes", map[string]any{
		"revision": rev,
		"route": map[string]any{
			"name": "gamma", "prefix": "/gamma",
			"options": map[string]any{"target": "https://g.example.com"},
		},
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create = %d (%s)", resp.StatusCode, body)
	}
	var created struct {
		Revision string             `json:"revision"`
		Route    config.RouteConfig `json:"route"`
	}
	mustJSON(t, body, &created)
	if created.Revision == rev || created.Revision != env.manager.Current().Revision {
		t.Errorf("create revision = %q (old %q)", created.Revision, rev)
	}
	if created.Route.TransportName() != "direct" || created.Route.Enabled == nil || !*created.Route.Enabled {
		t.Errorf("created route not normalized: %+v", created.Route)
	}
	onDisk := loadFile(t, env.cfgPath)
	if len(onDisk.Routes) != 3 || onDisk.Routes[2].Name != "gamma" {
		t.Errorf("file not rewritten with gamma: %+v", onDisk.Routes)
	}

	// Duplicate name → validation_failed.
	resp, body = env.do(t, "POST", "/api/v1/routes", map[string]any{
		"route": map[string]any{
			"name": "alpha", "prefix": "/dup",
			"options": map[string]any{"target": "https://d.example.com"},
		},
	})
	e := assertError(t, resp, body, http.StatusBadRequest, "validation_failed")
	if len(e.Issues) == 0 {
		t.Error("duplicate create carried no issues")
	}

	// Stale revision on create → 409.
	resp, body = env.do(t, "POST", "/api/v1/routes", map[string]any{
		"revision": "sha256:0000000000000000000000000000000000000000000000000000000000000000",
		"route": map[string]any{
			"name": "delta", "prefix": "/delta",
			"options": map[string]any{"target": "https://d.example.com"},
		},
	})
	assertError(t, resp, body, http.StatusConflict, "revision_conflict")

	// PUT: name mismatch → validation_failed.
	resp, body = env.do(t, "PUT", "/api/v1/routes/gamma", map[string]any{
		"route": map[string]any{
			"name": "other", "prefix": "/gamma",
			"options": map[string]any{"target": "https://g.example.com"},
		},
	})
	assertError(t, resp, body, http.StatusBadRequest, "validation_failed")

	// PUT happy path.
	resp, body = env.do(t, "PUT", "/api/v1/routes/gamma", map[string]any{
		"revision": env.manager.Current().Revision,
		"route": map[string]any{
			"name": "gamma", "prefix": "/gamma",
			"options": map[string]any{"target": "https://g2.example.com"},
		},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update = %d (%s)", resp.StatusCode, body)
	}
	var updated struct {
		Revision string             `json:"revision"`
		Route    config.RouteConfig `json:"route"`
	}
	mustJSON(t, body, &updated)
	if updated.Route.Proxy == nil || updated.Route.Proxy.Target != "https://g2.example.com" {
		t.Errorf("updated route = %+v, want target https://g2.example.com", updated.Route)
	}
	onDisk = loadFile(t, env.cfgPath)
	if onDisk.Routes[2].Proxy == nil || onDisk.Routes[2].Proxy.Target != "https://g2.example.com" {
		t.Errorf("file route = %+v, want target https://g2.example.com", onDisk.Routes[2])
	}

	// PUT unknown → 404.
	resp, body = env.do(t, "PUT", "/api/v1/routes/ghost", map[string]any{
		"route": map[string]any{
			"name": "ghost", "prefix": "/ghost",
			"options": map[string]any{"target": "https://x.example.com"},
		},
	})
	assertError(t, resp, body, http.StatusNotFound, "not_found")

	// DELETE with stale revision → 409, route still there.
	resp, body = env.do(t, "DELETE", "/api/v1/routes/gamma",
		map[string]any{"revision": "sha256:0000000000000000000000000000000000000000000000000000000000000000"})
	assertError(t, resp, body, http.StatusConflict, "revision_conflict")

	// DELETE with the current revision (frontend behavior) → 204, no body.
	resp, body = env.do(t, "DELETE", "/api/v1/routes/gamma",
		map[string]any{"revision": env.manager.Current().Revision})
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete = %d (%s)", resp.StatusCode, body)
	}
	if len(body) != 0 {
		t.Errorf("delete body = %q, want empty", body)
	}
	if onDisk = loadFile(t, env.cfgPath); len(onDisk.Routes) != 2 {
		t.Errorf("file still has %d routes", len(onDisk.Routes))
	}

	// DELETE without a body skips the revision check.
	resp, body = env.do(t, "DELETE", "/api/v1/routes/beta", nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("bodyless delete = %d (%s)", resp.StatusCode, body)
	}

	// DELETE unknown → 404.
	resp, body = env.do(t, "DELETE", "/api/v1/routes/ghost", nil)
	assertError(t, resp, body, http.StatusNotFound, "not_found")
}

// --- transports ---------------------------------------------------------------

func TestTransportCRUD(t *testing.T) {
	env := newTestEnv(t)

	// List: synthetic direct first, then config order.
	resp, body := env.do(t, "GET", "/api/v1/transports", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list = %d (%s)", resp.StatusCode, body)
	}
	var list struct {
		Transports []config.TransportConfig `json:"transports"`
	}
	mustJSON(t, body, &list)
	if len(list.Transports) != 2 ||
		list.Transports[0].Name != "direct" || list.Transports[0].Type != "direct" || list.Transports[0].URL != "" ||
		list.Transports[1].Name != "t1" {
		t.Fatalf("transports = %+v", list.Transports)
	}

	// direct is readable but immutable.
	resp, body = env.do(t, "GET", "/api/v1/transports/direct", nil)
	if resp.StatusCode != http.StatusOK || !bytes.Contains(body, []byte(`"direct"`)) {
		t.Errorf("get direct = %d (%s)", resp.StatusCode, body)
	}
	resp, body = env.do(t, "PUT", "/api/v1/transports/direct", map[string]any{
		"transport": map[string]any{"name": "direct", "type": "http", "url": "http://127.0.0.1:1"},
	})
	assertError(t, resp, body, http.StatusForbidden, "builtin_transport")
	resp, body = env.do(t, "DELETE", "/api/v1/transports/direct", nil)
	assertError(t, resp, body, http.StatusForbidden, "builtin_transport")

	// Deleting a transport still referenced by routes → 409 with the route names.
	resp, body = env.do(t, "DELETE", "/api/v1/transports/t1", nil)
	e := assertError(t, resp, body, http.StatusConflict, "transport_in_use")
	if !strings.Contains(e.Detail, "beta") {
		t.Errorf("detail = %q, want the referencing route name", e.Detail)
	}

	// Create.
	resp, body = env.do(t, "POST", "/api/v1/transports", map[string]any{
		"revision":  env.manager.Current().Revision,
		"transport": map[string]any{"name": "t2", "type": "socks5", "url": "socks5://127.0.0.1:1080"},
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create = %d (%s)", resp.StatusCode, body)
	}
	var created struct {
		Revision  string                 `json:"revision"`
		Transport config.TransportConfig `json:"transport"`
	}
	mustJSON(t, body, &created)
	if created.Transport.Name != "t2" || created.Revision != env.manager.Current().Revision {
		t.Errorf("created = %+v", created)
	}
	if onDisk := loadFile(t, env.cfgPath); len(onDisk.Transports) != 2 {
		t.Errorf("file transports = %+v", onDisk.Transports)
	}

	// Reserved name → validation_failed (validator owns the message).
	resp, body = env.do(t, "POST", "/api/v1/transports", map[string]any{
		"transport": map[string]any{"name": "direct", "type": "http", "url": "http://127.0.0.1:1"},
	})
	assertError(t, resp, body, http.StatusBadRequest, "validation_failed")

	// Update.
	resp, body = env.do(t, "PUT", "/api/v1/transports/t2", map[string]any{
		"transport": map[string]any{"name": "t2", "type": "socks5", "url": "socks5://127.0.0.1:2080"},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update = %d (%s)", resp.StatusCode, body)
	}
	mustJSON(t, body, &created)
	if created.Transport.URL != "socks5://127.0.0.1:2080" {
		t.Errorf("updated url = %q", created.Transport.URL)
	}

	// Name mismatch and unknowns.
	resp, body = env.do(t, "PUT", "/api/v1/transports/t2", map[string]any{
		"transport": map[string]any{"name": "zz", "type": "socks5", "url": "socks5://127.0.0.1:1"},
	})
	assertError(t, resp, body, http.StatusBadRequest, "validation_failed")
	resp, body = env.do(t, "GET", "/api/v1/transports/ghost", nil)
	assertError(t, resp, body, http.StatusNotFound, "not_found")
	resp, body = env.do(t, "PUT", "/api/v1/transports/ghost", map[string]any{
		"transport": map[string]any{"name": "ghost", "type": "http", "url": "http://127.0.0.1:1"},
	})
	assertError(t, resp, body, http.StatusNotFound, "not_found")
	resp, body = env.do(t, "DELETE", "/api/v1/transports/ghost", nil)
	assertError(t, resp, body, http.StatusNotFound, "not_found")

	// Unreferenced delete succeeds.
	resp, body = env.do(t, "DELETE", "/api/v1/transports/t2", nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete = %d (%s)", resp.StatusCode, body)
	}
	if onDisk := loadFile(t, env.cfgPath); len(onDisk.Transports) != 1 {
		t.Errorf("file transports after delete = %+v", onDisk.Transports)
	}
}

// --- origin check -------------------------------------------------------------

func TestOriginCheck(t *testing.T) {
	env := newTestEnv(t)
	newRoute := func(name string) map[string]any {
		return map[string]any{
			"route": map[string]any{
				"name": name, "prefix": "/" + name,
				"options": map[string]any{"target": "https://o.example.com"},
			},
		}
	}

	// Cross-origin mutation → 403, and nothing must have been applied.
	before := env.manager.Current().Revision
	resp, body := env.do(t, "POST", "/api/v1/routes", newRoute("evil"),
		header{"Origin", "http://evil.example.com"})
	assertError(t, resp, body, http.StatusForbidden, "origin_not_allowed")
	if env.manager.Current().Revision != before {
		t.Error("config changed despite origin rejection")
	}

	// The opaque "null" origin is rejected too.
	resp, body = env.do(t, "POST", "/api/v1/routes", newRoute("nully"), header{"Origin", "null"})
	assertError(t, resp, body, http.StatusForbidden, "origin_not_allowed")

	// Same host:port (what the same-origin WebUI sends) is allowed.
	host := strings.TrimPrefix(env.srv.URL, "http://")
	resp, body = env.do(t, "POST", "/api/v1/routes", newRoute("good"), header{"Origin", "http://" + host})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("same-origin create = %d (%s)", resp.StatusCode, body)
	}

	// Reads are exempt from the origin check.
	resp, _ = env.do(t, "GET", "/api/v1/routes", nil, header{"Origin", "http://evil.example.com"})
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET with foreign origin = %d, want 200", resp.StatusCode)
	}
}

// --- metrics ------------------------------------------------------------------

func TestMetricsEndpoints(t *testing.T) {
	env := newTestEnv(t)
	env.reg.Observe("alpha", 200, false, 5*time.Millisecond)

	resp, body := env.do(t, "GET", "/api/v1/metrics", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("metrics = %d (%s)", resp.StatusCode, body)
	}
	var m map[string]any
	mustJSON(t, body, &m)
	for _, key := range []string{"started_at", "uptime_seconds", "total_requests", "error_requests",
		"active_requests", "active_websockets", "active_sse", "route_count", "transport_count",
		"latency", "routes", "log_dropped"} {
		if _, ok := m[key]; !ok {
			t.Errorf("metrics response missing %q", key)
		}
	}
	if m["total_requests"].(float64) != 1 {
		t.Errorf("total_requests = %v, want 1", m["total_requests"])
	}

	// 48h history: totals-only assertions — bucket placement would flake
	// across a real hour boundary (placement is covered in metrics with an
	// injected clock).
	resp, body = env.do(t, "GET", "/api/v1/metrics/history", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("metrics history = %d (%s)", resp.StatusCode, body)
	}
	var hist metrics.HistorySnapshot
	mustJSON(t, body, &hist)
	if hist.BucketSeconds != 3600 {
		t.Errorf("bucket_seconds = %d, want 3600", hist.BucketSeconds)
	}
	if len(hist.Buckets) != 49 {
		t.Errorf("len(buckets) = %d, want 49", len(hist.Buckets))
	}
	if hist.Totals.Success != 1 || hist.Totals.Errors != 0 {
		t.Errorf("history totals = %+v, want success=1 errors=0", hist.Totals)
	}
	for i := 1; i < len(hist.Buckets); i++ {
		if hist.Buckets[i].Start.Sub(hist.Buckets[i-1].Start) != time.Hour {
			t.Fatalf("history buckets not contiguous at %d", i)
		}
	}

	// Per-route metrics for a configured, observed route.
	resp, body = env.do(t, "GET", "/api/v1/routes/alpha/metrics", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("route metrics = %d (%s)", resp.StatusCode, body)
	}
	var rm metrics.RouteSnapshot
	mustJSON(t, body, &rm)
	if rm.Name != "alpha" || rm.Total != 1 || rm.Status2xx != 1 {
		t.Errorf("route metrics = %+v", rm)
	}

	// Unknown to the CONFIG → 404, even though metrics also lack it.
	resp, body = env.do(t, "GET", "/api/v1/routes/ghost/metrics", nil)
	assertError(t, resp, body, http.StatusNotFound, "not_found")

	// Configured but disabled → no metric series, still 200 with zeroes.
	resp, body = env.do(t, "POST", "/api/v1/routes", map[string]any{
		"route": map[string]any{
			"name": "off", "prefix": "/off", "enabled": false,
			"options": map[string]any{"target": "https://off.example.com"},
		},
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create disabled route = %d (%s)", resp.StatusCode, body)
	}
	resp, body = env.do(t, "GET", "/api/v1/routes/off/metrics", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("disabled route metrics = %d (%s)", resp.StatusCode, body)
	}
	mustJSON(t, body, &rm)
	if rm.Name != "off" || rm.Total != 0 {
		t.Errorf("disabled route metrics = %+v, want zeroes", rm)
	}
}

// --- logs ---------------------------------------------------------------------

func TestLogsEndpoint(t *testing.T) {
	env := newTestEnv(t)
	base := time.Now()
	env.ring.Add(logging.AccessEntry{Time: base, Route: "alpha", Method: "GET", Path: "/alpha/a", Status: 200, Transport: "direct"})
	env.ring.Add(logging.AccessEntry{Time: base.Add(time.Second), Route: "beta", Method: "POST", Path: "/beta/b", Status: 502, Transport: "t1", Error: "upstream_connection_failed"})
	env.ring.Add(logging.AccessEntry{Time: base.Add(2 * time.Second), Route: "alpha", Method: "GET", Path: "/alpha/c", Status: 404, Transport: "direct"})

	var lr struct {
		Entries  []logging.AccessEntry `json:"entries"`
		Dropped  uint64                `json:"dropped"`
		Capacity int                   `json:"capacity"`
	}

	resp, body := env.do(t, "GET", "/api/v1/logs", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("logs = %d (%s)", resp.StatusCode, body)
	}
	mustJSON(t, body, &lr)
	if len(lr.Entries) != 3 || lr.Entries[0].Path != "/alpha/c" {
		t.Fatalf("entries = %+v, want 3 newest-first", lr.Entries)
	}
	if lr.Capacity != 64 || lr.Dropped != 0 {
		t.Errorf("capacity/dropped = %d/%d", lr.Capacity, lr.Dropped)
	}

	tests := []struct {
		query string
		want  int
	}{
		{"?limit=1", 1},
		{"?route=alpha", 2},
		{"?status_class=5xx", 1},
		{"?status_class=error", 1},
		{"?status_class=2xx&route=beta", 0},
		{"?limit=99999", 3}, // capped, not rejected
		{"?limit=0", 3},     // non-positive → all buffered (bounded)
	}
	for _, tc := range tests {
		resp, body = env.do(t, "GET", "/api/v1/logs"+tc.query, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("logs%s = %d (%s)", tc.query, resp.StatusCode, body)
		}
		mustJSON(t, body, &lr)
		if len(lr.Entries) != tc.want {
			t.Errorf("logs%s: %d entries, want %d", tc.query, len(lr.Entries), tc.want)
		}
	}

	resp, body = env.do(t, "GET", "/api/v1/logs?status_class=9xx", nil)
	assertError(t, resp, body, http.StatusBadRequest, "invalid_request")
	resp, body = env.do(t, "GET", "/api/v1/logs?limit=abc", nil)
	assertError(t, resp, body, http.StatusBadRequest, "invalid_request")
}

func TestLogsDisabledRing(t *testing.T) {
	env := newTestEnv(t, func(d *api.Deps) { d.Ring = logging.NewRing(0) })
	resp, body := env.do(t, "GET", "/api/v1/logs", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("logs = %d (%s)", resp.StatusCode, body)
	}
	var lr struct {
		Entries  []logging.AccessEntry `json:"entries"`
		Capacity int                   `json:"capacity"`
	}
	mustJSON(t, body, &lr)
	if lr.Entries == nil || len(lr.Entries) != 0 || lr.Capacity != 0 {
		t.Errorf("disabled ring response = %s", body)
	}
}

// --- dispatch: unknown paths, methods, body cap, webui -------------------------

func TestUnknownAPIPathsAndMethods(t *testing.T) {
	env := newTestEnv(t)

	resp, body := env.do(t, "GET", "/api/v1/bogus", nil)
	assertError(t, resp, body, http.StatusNotFound, "not_found")
	resp, body = env.do(t, "GET", "/api/other", nil)
	assertError(t, resp, body, http.StatusNotFound, "not_found")
	resp, body = env.do(t, "GET", "/api", nil)
	assertError(t, resp, body, http.StatusNotFound, "not_found")

	// Method mismatch on a known path → 405 JSON with an Allow header.
	resp, body = env.do(t, "DELETE", "/api/v1/status", nil)
	assertError(t, resp, body, http.StatusMethodNotAllowed, "method_not_allowed")
	if allow := resp.Header.Get("Allow"); !strings.Contains(allow, "GET") {
		t.Errorf("Allow = %q, want it to include GET", allow)
	}
	resp, body = env.do(t, "PATCH", "/api/v1/routes/alpha", nil)
	assertError(t, resp, body, http.StatusMethodNotAllowed, "method_not_allowed")

	// Without a WebUI, non-API paths are JSON 404s too.
	resp, body = env.do(t, "GET", "/somewhere", nil)
	assertError(t, resp, body, http.StatusNotFound, "not_found")
}

func TestOversizedBodyRejected(t *testing.T) {
	env := newTestEnv(t)
	huge := `{"revision":"` + strings.Repeat("a", (1<<20)+1024) + `"}`
	resp, body := env.do(t, "PUT", "/api/v1/config", huge)
	assertError(t, resp, body, http.StatusRequestEntityTooLarge, "invalid_request")
}

func TestWebUIFallback(t *testing.T) {
	ui := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, "ui:"+r.URL.Path)
	})
	env := newTestEnv(t, func(d *api.Deps) { d.WebUI = ui })

	resp, body := env.do(t, "GET", "/dashboard", nil)
	if resp.StatusCode != http.StatusOK || string(body) != "ui:/dashboard" {
		t.Errorf("webui = %d %q", resp.StatusCode, body)
	}
	// API paths never fall through to the UI.
	resp, body = env.do(t, "GET", "/api/v1/bogus", nil)
	assertError(t, resp, body, http.StatusNotFound, "not_found")
}

// --- diagnostics ----------------------------------------------------------------

func TestDiagnosticsEndpoints(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ping" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer upstream.Close()

	env := newTestEnv(t)
	for _, route := range []map[string]any{
		{"name": "up", "prefix": "/up", "options": map[string]any{"target": upstream.URL}},
		{"name": "down", "prefix": "/down", "enabled": false, "options": map[string]any{"target": upstream.URL}},
	} {
		resp, body := env.do(t, "POST", "/api/v1/routes", map[string]any{"route": route})
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("create %v = %d (%s)", route["name"], resp.StatusCode, body)
		}
	}

	var res struct {
		OK         bool    `json:"ok"`
		Route      string  `json:"route"`
		TargetURL  string  `json:"target_url"`
		Transport  string  `json:"transport"`
		Status     int     `json:"status"`
		ErrorStage string  `json:"error_stage"`
		Error      string  `json:"error"`
		HeaderMs   float64 `json:"header_duration_ms"`
		TotalMs    float64 `json:"total_duration_ms"`
	}

	// Request probe: path match like the data plane, then real pipeline.
	resp, body := env.do(t, "POST", "/api/v1/diagnostics/request",
		map[string]any{"path": "/up/ping", "method": "GET"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("diag request = %d (%s)", resp.StatusCode, body)
	}
	mustJSON(t, body, &res)
	if !res.OK || res.Status != 200 || res.Route != "up" || res.TargetURL != upstream.URL+"/ping" || res.Transport != "direct" {
		t.Errorf("diag request result = %+v", res)
	}

	// No matching route → 200 with resolve failure (not HTTP 404).
	resp, body = env.do(t, "POST", "/api/v1/diagnostics/request",
		map[string]any{"path": "/nomatch"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("diag request nomatch = %d (%s)", resp.StatusCode, body)
	}
	mustJSON(t, body, &res)
	if res.OK || res.ErrorStage != "resolve" || !strings.Contains(res.Error, "no route matched") {
		t.Errorf("diag request nomatch = %+v, want resolve failure", res)
	}

	// Invalid path/method → 400.
	resp, body = env.do(t, "POST", "/api/v1/diagnostics/request",
		map[string]any{"path": "relative", "method": "GET"})
	assertError(t, resp, body, http.StatusBadRequest, "invalid_request")
	resp, body = env.do(t, "POST", "/api/v1/diagnostics/request",
		map[string]any{"path": "/", "method": "TRACE"})
	assertError(t, resp, body, http.StatusBadRequest, "invalid_request")

	// Route probe through the real pipeline (named route test).
	resp, body = env.do(t, "POST", "/api/v1/diagnostics/route",
		map[string]any{"route": "up", "path": "/ping"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("diag route = %d (%s)", resp.StatusCode, body)
	}
	mustJSON(t, body, &res)
	if !res.OK || res.Status != 200 || res.Route != "up" || res.TargetURL != upstream.URL+"/ping" || res.Transport != "direct" {
		t.Errorf("diag route result = %+v", res)
	}

	// Disabled route: known to the config → 200 result with a resolve failure.
	resp, body = env.do(t, "POST", "/api/v1/diagnostics/route", map[string]any{"route": "down"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("diag disabled route = %d (%s)", resp.StatusCode, body)
	}
	mustJSON(t, body, &res)
	if res.OK || res.ErrorStage != "resolve" {
		t.Errorf("disabled route result = %+v, want resolve failure", res)
	}

	// Unknown route → 404; invalid method/path → 400.
	resp, body = env.do(t, "POST", "/api/v1/diagnostics/route", map[string]any{"route": "ghost"})
	assertError(t, resp, body, http.StatusNotFound, "not_found")
	resp, body = env.do(t, "POST", "/api/v1/diagnostics/route",
		map[string]any{"route": "up", "method": "TRACE"})
	assertError(t, resp, body, http.StatusBadRequest, "invalid_request")
	resp, body = env.do(t, "POST", "/api/v1/diagnostics/route",
		map[string]any{"route": "up", "path": "ping"})
	assertError(t, resp, body, http.StatusBadRequest, "invalid_request")
	resp, body = env.do(t, "POST", "/api/v1/diagnostics/route", map[string]any{})
	assertError(t, resp, body, http.StatusBadRequest, "invalid_request")

	// Transport probe.
	resp, body = env.do(t, "POST", "/api/v1/diagnostics/transport",
		map[string]any{"transport": "direct", "url": upstream.URL + "/ping"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("diag transport = %d (%s)", resp.StatusCode, body)
	}
	mustJSON(t, body, &res)
	if !res.OK || res.Status != 200 {
		t.Errorf("diag transport result = %+v", res)
	}

	// Unknown transport → 404; non-http(s) URL → 400.
	resp, body = env.do(t, "POST", "/api/v1/diagnostics/transport",
		map[string]any{"transport": "ghost", "url": upstream.URL})
	assertError(t, resp, body, http.StatusNotFound, "not_found")
	resp, body = env.do(t, "POST", "/api/v1/diagnostics/transport",
		map[string]any{"transport": "direct", "url": "ftp://example.com/x"})
	assertError(t, resp, body, http.StatusBadRequest, "invalid_request")
	resp, body = env.do(t, "POST", "/api/v1/diagnostics/transport",
		map[string]any{"transport": "direct", "url": "not a url"})
	assertError(t, resp, body, http.StatusBadRequest, "invalid_request")
}
