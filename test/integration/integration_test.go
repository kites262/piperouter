// Core acceptance cases through the real proxy listener:
//
//	§23.1  prefix + boundary matching        TestPrefixMatching
//	§23.2  longest prefix wins               TestLongestPrefixWins
//	§23.3  rewrite + query preservation      TestRewritePreservesQuery
//	§7.4   unmatched → JSON 404              TestUnmatchedRouteJSON404
//	§22.3  graceful shutdown                 TestGracefulShutdown
//	§23.11 WebUI-save-equivalent route POST  TestRouteCreateViaAdminAPI
//	§23.12 log security                      TestLogSecurity
package integration_test

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kites262/piperouter/internal/config"
)

// TestPrefixMatching covers PRD §23.1: /openai matches /openai, /openai/
// and /openai/test but never /openai2 (path-boundary matching).
func TestPrefixMatching(t *testing.T) {
	up := pathEcho(t, "up")
	ta := startApp(t, baseConfig(route("openai", "/openai", up.URL)))
	client := newClient(t)

	tests := []struct {
		path      string
		wantMatch bool
	}{
		{"/openai", true},
		{"/openai/", true},
		{"/openai/test", true},
		{"/openai2", false},
	}
	for _, tc := range tests {
		resp, body := get(t, client, ta.proxyURL()+tc.path)
		if tc.wantMatch {
			if resp.StatusCode != http.StatusOK {
				t.Errorf("GET %s: status = %d (body %q), want 200 (match)", tc.path, resp.StatusCode, body)
			}
		} else {
			if resp.StatusCode != http.StatusNotFound {
				t.Errorf("GET %s: status = %d, want 404 (no match)", tc.path, resp.StatusCode)
			}
			if strings.Contains(body, "route_not_found") {
				t.Errorf("GET %s: body = %q, must not leak the internal error code", tc.path, body)
			}
		}
	}
}

// TestLongestPrefixWins covers PRD §23.2: with /api and /api/openai
// configured, /api/openai/models must hit /api/openai.
func TestLongestPrefixWins(t *testing.T) {
	upShort := pathEcho(t, "api")
	upLong := pathEcho(t, "api-openai")
	ta := startApp(t, baseConfig(
		route("api", "/api", upShort.URL),
		route("api-openai", "/api/openai", upLong.URL),
	))
	client := newClient(t)

	resp, body := get(t, client, ta.proxyURL()+"/api/openai/models")
	if resp.StatusCode != http.StatusOK || body != "api-openai:/models" {
		t.Errorf("GET /api/openai/models: status=%d body=%q, want 200 %q (longest prefix)",
			resp.StatusCode, body, "api-openai:/models")
	}

	// The shorter prefix still serves everything else under /api.
	resp, body = get(t, client, ta.proxyURL()+"/api/other")
	if resp.StatusCode != http.StatusOK || body != "api:/other" {
		t.Errorf("GET /api/other: status=%d body=%q, want 200 %q", resp.StatusCode, body, "api:/other")
	}
}

// TestRewritePreservesQuery covers PRD §23.3 and §22.6 (query preservation,
// raw path): the upstream asserts the exact RequestURI it received.
func TestRewritePreservesQuery(t *testing.T) {
	var mu sync.Mutex
	var lastURI string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		lastURI = r.RequestURI
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(up.Close)

	keep := route("keep", "/keep", up.URL)
	keep.Proxy.StripPrefix = bp(false)
	ta := startApp(t, baseConfig(
		route("openai", "/openai", up.URL+"/v1"), // strip_prefix defaults to true
		keep,
	))
	client := newClient(t)

	tests := []struct {
		name    string
		reqPath string
		wantURI string
	}{
		{"strip + query", "/openai/chat?stream=true", "/v1/chat?stream=true"},
		{"raw path + escaped query preserved", "/openai/we%20ird?q=a%2Fb", "/v1/we%20ird?q=a%2Fb"},
		{"no strip keeps full path and query", "/keep/x?a=1&b=2", "/keep/x?a=1&b=2"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp, body := get(t, client, ta.proxyURL()+tc.reqPath)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d (body %q), want 200", resp.StatusCode, body)
			}
			mu.Lock()
			got := lastURI
			mu.Unlock()
			if got != tc.wantURI {
				t.Errorf("upstream RequestURI = %q, want %q", got, tc.wantURI)
			}
		})
	}
}

// TestUnmatchedRouteAnonymous404: unmatched requests get a bare "404"
// (text/plain) — never the JSON error envelope or any wording, so path
// scanners cannot fingerprint PipeRouter. The request is still logged
// internally as route_not_found.
func TestUnmatchedRouteAnonymous404(t *testing.T) {
	up := pathEcho(t, "up")
	ta := startApp(t, baseConfig(route("api", "/api", up.URL)))
	client := newClient(t)

	resp, body := get(t, client, ta.proxyURL()+"/definitely-not-registered")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("Content-Type = %q, want text/plain (bare 404)", ct)
	}
	if body != "404" {
		t.Errorf("body = %q, want exactly %q", body, "404")
	}
}

// TestGracefulShutdown covers PRD §22.3: an in-flight slow request
// completes during Shutdown, and new connections are refused afterwards.
func TestGracefulShutdown(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered) // exactly one in-flight request hits this upstream
		select {
		case <-release:
		case <-time.After(3 * time.Second): // safety valve, normally released early
		}
		io.WriteString(w, "slow-done") //nolint:errcheck
	}))
	t.Cleanup(up.Close)
	ta := startApp(t, baseConfig(route("slow", "/slow", up.URL)))
	client := newClient(t)

	type result struct {
		status int
		body   string
		err    error
	}
	resCh := make(chan result, 1)
	go func() {
		resp, err := client.Get(ta.proxyURL() + "/slow/hold")
		if err != nil {
			resCh <- result{err: err}
			return
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		resCh <- result{status: resp.StatusCode, body: string(b)}
	}()
	waitClosed(t, entered, 5*time.Second, "upstream never saw the in-flight request")

	shutErr := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		shutErr <- ta.App.Shutdown(ctx)
	}()

	// While the request is still draining, the listener must already be
	// closed: fresh TCP connections are refused.
	eventually(t, 3*time.Second, "proxy listener still accepting during shutdown", func() bool {
		conn, err := net.DialTimeout("tcp", ta.App.ProxyAddr(), 250*time.Millisecond)
		if err != nil {
			return true
		}
		conn.Close()
		return false
	})

	close(release)
	select {
	case res := <-resCh:
		if res.err != nil {
			t.Fatalf("in-flight request failed during graceful shutdown: %v", res.err)
		}
		if res.status != http.StatusOK || res.body != "slow-done" {
			t.Fatalf("in-flight request: status=%d body=%q, want 200 %q", res.status, res.body, "slow-done")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("in-flight request did not complete within 10s of release")
	}
	select {
	case err := <-shutErr:
		if err != nil {
			t.Fatalf("Shutdown: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Shutdown did not return within 10s")
	}

	// After shutdown completes, new requests must fail outright.
	if _, err := client.Get(ta.proxyURL() + "/slow/late"); err == nil {
		t.Error("request after shutdown succeeded, want connection error")
	}
}

// TestRouteCreateViaAdminAPI covers PRD §23.11 (the WebUI save path): POST
// /api/v1/routes → 201, the route serves immediately, the config FILE
// contains it, and GET /api/v1/routes reflects it — no database anywhere.
func TestRouteCreateViaAdminAPI(t *testing.T) {
	upA := pathEcho(t, "base")
	upB := pathEcho(t, "created")
	ta := startApp(t, baseConfig(route("base", "/base", upA.URL)))
	client := newClient(t)

	// Concurrency-safe write: fetch the current revision first (PRD §12.3).
	var env struct {
		Revision string `json:"revision"`
	}
	getJSON(t, client, ta.adminURL()+"/api/v1/config", &env)
	if env.Revision == "" {
		t.Fatal("GET /api/v1/config returned an empty revision")
	}

	payload, err := json.Marshal(map[string]any{
		"revision": env.Revision,
		"route": map[string]any{
			"name":    "created",
			"enabled": true,
			"prefix":  "/created",
			"options": map[string]any{
				"target":       upB.URL,
				"strip_prefix": true,
				"transport":    "direct",
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	resp, body := postJSON(t, client, ta.adminURL()+"/api/v1/routes", string(payload))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /api/v1/routes: status = %d, want 201 (body %q)", resp.StatusCode, body)
	}

	// 1. The route serves immediately — no restart, no watcher round trip.
	presp, pbody := get(t, client, ta.proxyURL()+"/created/ping")
	if presp.StatusCode != http.StatusOK || pbody != "created:/ping" {
		t.Errorf("new route: status=%d body=%q, want 200 %q", presp.StatusCode, pbody, "created:/ping")
	}

	// 2. The configuration file was atomically rewritten to contain it.
	cfg, err := config.Load(ta.ConfigPath)
	if err != nil {
		t.Fatalf("re-read config file: %v", err)
	}
	var found *config.RouteConfig
	for i := range cfg.Routes {
		if cfg.Routes[i].Name == "created" {
			found = &cfg.Routes[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("config file %s does not contain route %q after POST", ta.ConfigPath, "created")
	}
	if found.Prefix != "/created" || found.Proxy == nil || found.Proxy.Target != upB.URL {
		t.Errorf("persisted route = %+v, want prefix /created target %s", *found, upB.URL)
	}

	// 3. The routes listing reflects it.
	var list struct {
		Routes []config.RouteConfig `json:"routes"`
	}
	getJSON(t, client, ta.adminURL()+"/api/v1/routes", &list)
	names := make([]string, 0, len(list.Routes))
	for _, r := range list.Routes {
		names = append(names, r.Name)
	}
	if !strings.Contains(strings.Join(names, ","), "created") {
		t.Errorf("GET /api/v1/routes = %v, want it to include %q", names, "created")
	}
}

// TestLogSecurity covers PRD §23.12/§14.3: requests carrying Authorization
// and Cookie headers plus a secret query string are proxied, and the logs
// endpoint must expose neither header values nor query strings anywhere in
// its JSON output.
func TestLogSecurity(t *testing.T) {
	const (
		secretBearer = "secret-bearer-token-a1b2c3"
		secretCookie = "secret-cookie-value-d4e5f6"
		secretQuery  = "secret-query-value-g7h8i9"
	)
	up := pathEcho(t, "up")
	ta := startApp(t, baseConfig(route("api", "/api", up.URL)))
	client := newClient(t)

	req, err := http.NewRequest(http.MethodGet, ta.proxyURL()+"/api/leak-check?api_key="+secretQuery, nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+secretBearer)
	req.Header.Set("Cookie", "session="+secretCookie)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("proxied request: %v", err)
	}
	io.Copy(io.Discard, resp.Body) //nolint:errcheck
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("proxied request status = %d, want 200", resp.StatusCode)
	}

	// The raw logs JSON is exactly the marshaled entries: scan the whole
	// document for every secret and for the query string.
	lresp, lbody := get(t, client, ta.adminURL()+"/api/v1/logs?limit=1000")
	if lresp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/v1/logs: status = %d", lresp.StatusCode)
	}
	var logs struct {
		Entries []struct {
			Path string `json:"path"`
		} `json:"entries"`
	}
	if err := json.Unmarshal([]byte(lbody), &logs); err != nil {
		t.Fatalf("decode logs: %v", err)
	}
	sawEntry := false
	for _, e := range logs.Entries {
		if e.Path == "/api/leak-check" {
			sawEntry = true
		}
	}
	if !sawEntry {
		t.Fatalf("no access entry with path /api/leak-check in %d entries — cannot prove sanitization", len(logs.Entries))
	}
	for _, secret := range []string{secretBearer, secretCookie, secretQuery, "api_key", "leak-check?"} {
		if strings.Contains(lbody, secret) {
			t.Errorf("logs endpoint JSON leaks %q", secret)
		}
	}
}
