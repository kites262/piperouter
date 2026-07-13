package diagnostics_test

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kites262/piperouter/internal/config"
	"github.com/kites262/piperouter/internal/diagnostics"
	"github.com/kites262/piperouter/internal/router"
	"github.com/kites262/piperouter/internal/runtime"
	"github.com/kites262/piperouter/internal/transport"
)

// buildSnapshot compiles a runtime snapshot from a config the same way
// the manager would, without touching the filesystem.
func buildSnapshot(t *testing.T, cfg *config.Config) *runtime.Snapshot {
	t.Helper()
	cfg.Version = config.SupportedVersion
	cfg.Normalize()
	if err := config.Validate(cfg, ""); err != nil {
		t.Fatalf("test config invalid: %v", err)
	}
	table, err := router.BuildTable(cfg.Routes, "")
	if err != nil {
		t.Fatalf("BuildTable: %v", err)
	}
	pool, err := transport.NewPool(cfg.Transports, cfg.Network)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	rev, err := config.Revision(cfg)
	if err != nil {
		t.Fatalf("Revision: %v", err)
	}
	t.Cleanup(pool.CloseIdleConnections)
	return &runtime.Snapshot{Config: cfg, Table: table, Pool: pool, Revision: rev, LoadedAt: time.Now()}
}

func route(name, prefix, target string, enabled bool) config.RouteConfig {
	e := enabled
	return config.RouteConfig{Name: name, Enabled: &e, Prefix: prefix, Target: target}
}

func TestTestRoute_Success(t *testing.T) {
	var gotPath, gotMethod string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod = r.URL.Path, r.Method
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("pong"))
	}))
	defer upstream.Close()

	snap := buildSnapshot(t, &config.Config{
		Routes: []config.RouteConfig{route("r1", "/api", upstream.URL+"/base", true)},
	})

	res := diagnostics.TestRoute(context.Background(), snap, diagnostics.RouteTest{Route: "r1", Path: "/models"})

	if !res.OK {
		t.Fatalf("OK = false, want true (stage=%q error=%q)", res.ErrorStage, res.Error)
	}
	if res.Status != http.StatusOK {
		t.Errorf("Status = %d, want 200", res.Status)
	}
	if res.Route != "r1" {
		t.Errorf("Route = %q, want r1", res.Route)
	}
	if want := upstream.URL + "/base/models"; res.TargetURL != want {
		t.Errorf("TargetURL = %q, want %q", res.TargetURL, want)
	}
	if res.Transport != "direct" {
		t.Errorf("Transport = %q, want direct", res.Transport)
	}
	if res.ErrorStage != "" || res.Error != "" {
		t.Errorf("unexpected error: stage=%q error=%q", res.ErrorStage, res.Error)
	}
	if gotPath != "/base/models" {
		t.Errorf("upstream saw path %q, want /base/models", gotPath)
	}
	if gotMethod != http.MethodGet {
		t.Errorf("upstream saw method %q, want GET (default)", gotMethod)
	}
	if res.HeaderDurationMs <= 0 || res.TotalDurationMs < res.HeaderDurationMs {
		t.Errorf("durations look wrong: header=%v total=%v", res.HeaderDurationMs, res.TotalDurationMs)
	}
}

// Any upstream HTTP response means the link works — 401/403/404 are OK
// by contract (PRD §16.1).
func TestTestRoute_UpstreamStatusStillOK(t *testing.T) {
	for _, status := range []int{200, 301, 401, 403, 404, 500} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if status == http.StatusMovedPermanently {
					w.Header().Set("Location", "/elsewhere")
				}
				w.WriteHeader(status)
			}))
			defer upstream.Close()

			snap := buildSnapshot(t, &config.Config{
				Routes: []config.RouteConfig{route("r1", "/api", upstream.URL, true)},
			})
			res := diagnostics.TestRoute(context.Background(), snap, diagnostics.RouteTest{Route: "r1"})

			if !res.OK {
				t.Fatalf("OK = false, want true (stage=%q error=%q)", res.ErrorStage, res.Error)
			}
			if res.Status != status {
				// A 301 must be reported, never followed.
				t.Errorf("Status = %d, want %d", res.Status, status)
			}
		})
	}
}

func TestTestRoute_ResolveFailures(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer upstream.Close()

	snap := buildSnapshot(t, &config.Config{
		Routes: []config.RouteConfig{
			route("live", "/live", upstream.URL, true),
			route("off", "/off", upstream.URL, false),
		},
	})

	tests := []struct {
		name    string
		req     diagnostics.RouteTest
		errPart string
	}{
		{"unknown route", diagnostics.RouteTest{Route: "ghost"}, "route disabled or not found"},
		{"disabled route", diagnostics.RouteTest{Route: "off"}, "route disabled or not found"},
		{"relative path", diagnostics.RouteTest{Route: "live", Path: "models"}, "start with"},
		{"bad escape", diagnostics.RouteTest{Route: "live", Path: "/%zz"}, "percent escape"},
		{"bad method", diagnostics.RouteTest{Route: "live", Method: "TRACE"}, "unsupported method"},
		{"bad method delete", diagnostics.RouteTest{Route: "live", Method: "DELETE"}, "unsupported method"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res := diagnostics.TestRoute(context.Background(), snap, tc.req)
			if res.OK {
				t.Fatal("OK = true, want false")
			}
			if res.ErrorStage != diagnostics.StageResolve {
				t.Errorf("ErrorStage = %q, want resolve", res.ErrorStage)
			}
			if res.Status != 0 {
				t.Errorf("Status = %d, want 0", res.Status)
			}
			if !strings.Contains(res.Error, tc.errPart) {
				t.Errorf("Error = %q, want it to contain %q", res.Error, tc.errPart)
			}
		})
	}
}

func TestTestRequest_MatchAndRewrite(t *testing.T) {
	var gotPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	// Longer prefix wins; strip_prefix true → /api/v1/models → /models on target.
	trueVal := true
	snap := buildSnapshot(t, &config.Config{
		Routes: []config.RouteConfig{
			{Name: "short", Enabled: &trueVal, Prefix: "/api", Target: upstream.URL + "/short", StripPrefix: &trueVal},
			{Name: "long", Enabled: &trueVal, Prefix: "/api/v1", Target: upstream.URL + "/v1base", StripPrefix: &trueVal},
		},
	})

	res := diagnostics.TestRequest(context.Background(), snap,
		diagnostics.RequestTest{Path: "/api/v1/models", Method: "GET"})
	if !res.OK {
		t.Fatalf("OK = false: stage=%q error=%q", res.ErrorStage, res.Error)
	}
	if res.Route != "long" {
		t.Errorf("Route = %q, want long (longest-prefix match)", res.Route)
	}
	if want := upstream.URL + "/v1base/models"; res.TargetURL != want {
		t.Errorf("TargetURL = %q, want %q", res.TargetURL, want)
	}
	if gotPath != "/v1base/models" {
		t.Errorf("upstream path = %q, want /v1base/models", gotPath)
	}
}

func TestTestRequest_NoMatch(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer upstream.Close()

	snap := buildSnapshot(t, &config.Config{
		Routes: []config.RouteConfig{route("only", "/api", upstream.URL, true)},
	})

	res := diagnostics.TestRequest(context.Background(), snap,
		diagnostics.RequestTest{Path: "/other/path"})
	if res.OK || res.ErrorStage != diagnostics.StageResolve {
		t.Fatalf("result = %+v, want resolve failure", res)
	}
	if !strings.Contains(res.Error, "no route matched") {
		t.Errorf("Error = %q, want no route matched", res.Error)
	}
	if res.Route != "" {
		t.Errorf("Route = %q, want empty on no match", res.Route)
	}
}

func TestTestRequest_ResolveFailures(t *testing.T) {
	snap := buildSnapshot(t, &config.Config{
		Routes: []config.RouteConfig{route("r", "/r", "http://127.0.0.1:9", true)},
	})
	tests := []struct {
		name    string
		req     diagnostics.RequestTest
		errPart string
	}{
		{"relative path", diagnostics.RequestTest{Path: "models"}, "start with"},
		{"bad escape", diagnostics.RequestTest{Path: "/%zz"}, "percent escape"},
		{"bad method", diagnostics.RequestTest{Path: "/", Method: "DELETE"}, "unsupported method"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res := diagnostics.TestRequest(context.Background(), snap, tc.req)
			if res.OK || res.ErrorStage != diagnostics.StageResolve {
				t.Fatalf("result = %+v, want resolve failure", res)
			}
			if !strings.Contains(res.Error, tc.errPart) {
				t.Errorf("Error = %q, want it to contain %q", res.Error, tc.errPart)
			}
		})
	}
}

func TestTestRoute_RootPrefixAndMethods(t *testing.T) {
	var gotPath, gotMethod string
	var gotLen int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod, gotLen = r.URL.Path, r.Method, r.ContentLength
	}))
	defer upstream.Close()

	snap := buildSnapshot(t, &config.Config{
		Routes: []config.RouteConfig{route("root", "/", upstream.URL, true)},
	})

	res := diagnostics.TestRoute(context.Background(), snap,
		diagnostics.RouteTest{Route: "root", Path: "/x/y", Method: "post"})
	if !res.OK {
		t.Fatalf("OK = false: stage=%q error=%q", res.ErrorStage, res.Error)
	}
	if gotPath != "/x/y" {
		t.Errorf("upstream saw path %q, want /x/y (no duplicate slash from the root prefix)", gotPath)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("upstream saw method %q, want POST (case-insensitive)", gotMethod)
	}
	if gotLen != 0 {
		t.Errorf("probe carried a body (Content-Length %d), want none", gotLen)
	}

	// Empty extra path on the root prefix probes "/".
	res = diagnostics.TestRoute(context.Background(), snap, diagnostics.RouteTest{Route: "root"})
	if !res.OK || gotPath != "/" {
		t.Errorf("empty path: OK=%v upstream path %q, want OK on /", res.OK, gotPath)
	}
}

func TestTestTransport_Success(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized) // 401 still means the link works
	}))
	defer upstream.Close()

	snap := buildSnapshot(t, &config.Config{})
	res := diagnostics.TestTransport(context.Background(), snap,
		diagnostics.TransportTest{Transport: "direct", URL: upstream.URL, Method: "HEAD"})

	if !res.OK {
		t.Fatalf("OK = false: stage=%q error=%q", res.ErrorStage, res.Error)
	}
	if res.Status != http.StatusUnauthorized {
		t.Errorf("Status = %d, want 401", res.Status)
	}
	if res.Transport != "direct" || res.TargetURL != upstream.URL {
		t.Errorf("Transport/TargetURL = %q/%q", res.Transport, res.TargetURL)
	}
}

func TestTestTransport_ResolveFailures(t *testing.T) {
	snap := buildSnapshot(t, &config.Config{})

	tests := []struct {
		name string
		req  diagnostics.TransportTest
	}{
		{"unknown transport", diagnostics.TransportTest{Transport: "ghost", URL: "http://example.com"}},
		{"relative url", diagnostics.TransportTest{Transport: "direct", URL: "/just/a/path"}},
		{"bad scheme", diagnostics.TransportTest{Transport: "direct", URL: "ftp://example.com/x"}},
		{"no host", diagnostics.TransportTest{Transport: "direct", URL: "http://"}},
		{"userinfo", diagnostics.TransportTest{Transport: "direct", URL: "http://user:pw@example.com"}},
		{"bad method", diagnostics.TransportTest{Transport: "direct", URL: "http://example.com", Method: "PUT"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res := diagnostics.TestTransport(context.Background(), snap, tc.req)
			if res.OK || res.ErrorStage != diagnostics.StageResolve {
				t.Errorf("got OK=%v stage=%q, want failed resolve (error=%q)", res.OK, res.ErrorStage, res.Error)
			}
			// A rejected URL must never be echoed back unvalidated.
			if res.TargetURL != "" {
				t.Errorf("TargetURL = %q, want empty on resolve failure", res.TargetURL)
			}
		})
	}
}

func TestTestTransport_ConnectRefused(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := lis.Addr().String()
	_ = lis.Close() // nothing listens here anymore

	snap := buildSnapshot(t, &config.Config{})
	res := diagnostics.TestTransport(context.Background(), snap,
		diagnostics.TransportTest{Transport: "direct", URL: "http://" + addr})

	if res.OK {
		t.Fatal("OK = true, want false")
	}
	if res.ErrorStage != diagnostics.StageConnect {
		t.Errorf("ErrorStage = %q, want connect (error=%q)", res.ErrorStage, res.Error)
	}
	if res.Status != 0 {
		t.Errorf("Status = %d, want 0", res.Status)
	}
	if res.Error == "" {
		t.Error("Error is empty, want a sanitized message")
	}
}

func TestTestTransport_TLSFailures(t *testing.T) {
	t.Run("untrusted certificate", func(t *testing.T) {
		upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		defer upstream.Close()

		snap := buildSnapshot(t, &config.Config{})
		res := diagnostics.TestTransport(context.Background(), snap,
			diagnostics.TransportTest{Transport: "direct", URL: upstream.URL})

		if res.OK || res.ErrorStage != diagnostics.StageTLS {
			t.Errorf("got OK=%v stage=%q error=%q, want tls failure", res.OK, res.ErrorStage, res.Error)
		}
	})

	t.Run("plain server behind https url", func(t *testing.T) {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		defer upstream.Close()

		snap := buildSnapshot(t, &config.Config{})
		res := diagnostics.TestTransport(context.Background(), snap,
			diagnostics.TransportTest{Transport: "direct", URL: "https://" + upstream.Listener.Addr().String()})

		if res.OK || res.ErrorStage != diagnostics.StageTLS {
			t.Errorf("got OK=%v stage=%q error=%q, want tls failure", res.OK, res.ErrorStage, res.Error)
		}
	})
}

func TestTestTransport_Timeout(t *testing.T) {
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release // hold headers until the probe gave up
	}))
	defer upstream.Close()
	defer close(release)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	snap := buildSnapshot(t, &config.Config{})
	start := time.Now()
	res := diagnostics.TestTransport(ctx, snap,
		diagnostics.TransportTest{Transport: "direct", URL: upstream.URL})

	if res.OK {
		t.Fatal("OK = true, want false")
	}
	if res.ErrorStage != diagnostics.StageResponse {
		t.Errorf("ErrorStage = %q, want response (connected, no headers)", res.ErrorStage)
	}
	if !strings.Contains(res.Error, "timed out") {
		t.Errorf("Error = %q, want a timeout message", res.Error)
	}
	if elapsed := time.Since(start); elapsed < 90*time.Millisecond {
		t.Errorf("probe returned after %v, before the deadline could fire", elapsed)
	}
	if res.TotalDurationMs <= 0 {
		t.Errorf("TotalDurationMs = %v, want > 0", res.TotalDurationMs)
	}
}

// The proxy URL must never leak into client-visible error text (§23.12).
func TestErrorsRedactProxyURL(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	proxyAddr := lis.Addr().String()
	_ = lis.Close() // proxy is unreachable → dial error mentions its address

	snap := buildSnapshot(t, &config.Config{
		Transports: []config.TransportConfig{{Name: "px", Type: "http", URL: "http://" + proxyAddr}},
	})
	res := diagnostics.TestTransport(context.Background(), snap,
		diagnostics.TransportTest{Transport: "px", URL: "http://example.invalid/"})

	if res.OK {
		t.Fatal("OK = true, want false")
	}
	if strings.Contains(res.Error, proxyAddr) {
		t.Errorf("Error %q leaks the proxy address %q", res.Error, proxyAddr)
	}
}

func TestAllowedMethod(t *testing.T) {
	for m, want := range map[string]bool{
		"": true, "GET": true, "get": true, "HEAD": true, "POST": true, " post ": true,
		"PUT": false, "DELETE": false, "TRACE": false, "CONNECT": false,
	} {
		if got := diagnostics.AllowedMethod(m); got != want {
			t.Errorf("AllowedMethod(%q) = %v, want %v", m, got, want)
		}
	}
}
