package router

import (
	"strings"
	"testing"

	"github.com/kites262/piperouter/internal/config"
)

func boolp(b bool) *bool { return &b }

func route(name, prefix, target string) config.RouteConfig {
	return config.RouteConfig{
		Name:   name,
		Prefix: prefix,
		Proxy: &config.ProxyOptions{
			Target:    target,
			Transport: config.DirectName,
		},
	}
}

func mustTable(t *testing.T, routes ...config.RouteConfig) *Table {
	t.Helper()
	tbl, err := BuildTable(routes, "")
	if err != nil {
		t.Fatalf("BuildTable: %v", err)
	}
	return tbl
}

// PRD §23.1 / §7.2: prefix matches on path-segment boundaries only.
func TestMatchBoundary(t *testing.T) {
	tbl := mustTable(t, route("openai", "/openai", "https://api.example.com/v1"))

	tests := []struct {
		path string
		want bool
	}{
		{"/openai", true},
		{"/openai/", true},
		{"/openai/x", true},
		{"/openai/test", true},
		{"/openai/models", true},
		{"/openai/chat/completions", true},
		{"/openai//x", true}, // "/openai"+"/" is a prefix of "/openai//x"
		{"/openai2", false},
		{"/openai-test", false},
		{"/openais", false},
		{"/openai.", false},
		{"/open", false},
		{"/", false},
		{"/OPENAI", false},     // matching is case-sensitive
		{"/open%61i/x", false}, // matching is on the ESCAPED path, no decoding
		{"/openai%2Fmodels", false},
	}
	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			got := tbl.Match(tc.path)
			if tc.want && got == nil {
				t.Fatalf("Match(%q) = nil, want route openai", tc.path)
			}
			if !tc.want && got != nil {
				t.Fatalf("Match(%q) = %q, want nil", tc.path, got.Name)
			}
		})
	}
}

// PRD §23.2 / §7.3: longest prefix wins, YAML declaration order irrelevant.
func TestMatchLongestPrefix(t *testing.T) {
	prefixes := map[string]string{
		"api":           "/api",
		"api-openai":    "/api/openai",
		"api-openai-v1": "/api/openai/v1",
	}
	perms := [][]string{
		{"api", "api-openai", "api-openai-v1"},
		{"api", "api-openai-v1", "api-openai"},
		{"api-openai", "api", "api-openai-v1"},
		{"api-openai", "api-openai-v1", "api"},
		{"api-openai-v1", "api", "api-openai"},
		{"api-openai-v1", "api-openai", "api"},
	}
	tests := []struct {
		path string
		want string // route name, "" for no match
	}{
		{"/api/openai/v1/models", "api-openai-v1"},
		{"/api/openai/v1", "api-openai-v1"},
		{"/api/openai/models", "api-openai"},
		{"/api/openai", "api-openai"},
		{"/api/openai2", "api"}, // boundary: does not reach the longer routes
		{"/api/other", "api"},
		{"/api", "api"},
		{"/apix", ""},
		{"/", ""},
	}
	for _, perm := range perms {
		var routes []config.RouteConfig
		for _, n := range perm {
			routes = append(routes, route(n, prefixes[n], "https://upstream.example"))
		}
		tbl := mustTable(t, routes...)
		for _, tc := range tests {
			got := tbl.Match(tc.path)
			gotName := ""
			if got != nil {
				gotName = got.Name
			}
			if gotName != tc.want {
				t.Errorf("order %v: Match(%q) = %q, want %q",
					perm, tc.path, gotName, tc.want)
			}
		}
	}
}

// PRD §7.2: root prefix "/" matches everything, but longer prefixes win.
func TestMatchRoot(t *testing.T) {
	tbl := mustTable(t,
		route("catchall", "/", "https://fallback.example"),
		route("openai", "/openai", "https://api.example.com/v1"),
	)
	tests := []struct {
		path string
		want string
	}{
		{"/", "catchall"},
		{"/anything", "catchall"},
		{"/a/b/c", "catchall"},
		{"/openai2", "catchall"},
		{"/openai", "openai"},
		{"/openai/models", "openai"},
	}
	for _, tc := range tests {
		got := tbl.Match(tc.path)
		if got == nil {
			t.Fatalf("Match(%q) = nil, want %q", tc.path, tc.want)
		}
		if got.Name != tc.want {
			t.Errorf("Match(%q) = %q, want %q", tc.path, got.Name, tc.want)
		}
	}
}

// match: exact — the route matches only its literal prefix; anything else
// (including paths below it) matches no route at all when nothing broader
// exists.
func TestMatchExact(t *testing.T) {
	home := route("home", "/", "https://home.example")
	home.Match = config.MatchExact
	api := route("api", "/api", "https://api.example")
	tbl := mustTable(t, home, api)

	tests := []struct {
		path string
		want string // route name, "" for no match
	}{
		{"/", "home"},
		{"/api", "api"},
		{"/api/models", "api"},
		{"/index.html", ""},
		{"/wp-admin/install.php", ""},
		{"/favicon.ico", ""},
		{"//", ""},
	}
	for _, tc := range tests {
		got := tbl.Match(tc.path)
		gotName := ""
		if got != nil {
			gotName = got.Name
		}
		if gotName != tc.want {
			t.Errorf("Match(%q) = %q, want %q", tc.path, gotName, tc.want)
		}
	}
}

// An exact route never captures deeper paths: they fall through to a broader
// prefix route when one exists.
func TestMatchExactFallsThrough(t *testing.T) {
	health := route("health", "/health", "https://h.example")
	health.Match = config.MatchExact
	catchall := route("catchall", "/", "https://fallback.example")
	tbl := mustTable(t, health, catchall)

	tests := []struct {
		path string
		want string
	}{
		{"/health", "health"},
		{"/health/live", "catchall"},
		{"/health/", "catchall"},
		{"/healthz", "catchall"},
		{"/", "catchall"},
	}
	for _, tc := range tests {
		got := tbl.Match(tc.path)
		if got == nil {
			t.Fatalf("Match(%q) = nil, want %q", tc.path, tc.want)
		}
		if got.Name != tc.want {
			t.Errorf("Match(%q) = %q, want %q", tc.path, got.Name, tc.want)
		}
	}
}

func TestBuildTableExactField(t *testing.T) {
	exact := route("e", "/e", "https://e.example")
	exact.Match = config.MatchExact
	tbl := mustTable(t, exact, route("p", "/p", "https://p.example"))
	if r := tbl.Match("/e"); r == nil || !r.Exact {
		t.Fatalf("Match(/e) = %+v, want Exact route", r)
	}
	// Default (empty / "prefix") compiles to Exact == false.
	if r := tbl.Match("/p"); r == nil || r.Exact {
		t.Fatalf("Match(/p) = %+v, want non-Exact route", r)
	}
}

func TestBuildTableSkipsDisabled(t *testing.T) {
	on := route("on", "/on", "https://on.example")
	off := route("off", "/off", "https://off.example")
	off.Enabled = boolp(false)
	explicitOn := route("explicit", "/explicit", "https://explicit.example")
	explicitOn.Enabled = boolp(true)

	tbl := mustTable(t, on, off, explicitOn)
	if tbl.Len() != 2 {
		t.Fatalf("Len() = %d, want 2", tbl.Len())
	}
	if got := tbl.Match("/off/x"); got != nil {
		t.Errorf("Match(/off/x) = %q, want nil (route disabled)", got.Name)
	}
	if got := tbl.Match("/on/x"); got == nil || got.Name != "on" {
		t.Errorf("Match(/on/x) = %v, want route on", got)
	}
	for _, r := range tbl.Routes() {
		if r.Name == "off" {
			t.Error("Routes() contains disabled route")
		}
	}
}

func TestBuildTableFields(t *testing.T) {
	rc := config.RouteConfig{
		Name:   "openai",
		Prefix: "/openai",
		Proxy: &config.ProxyOptions{
			Target:      "https://api.example.com:8443/v1",
			StripPrefix: boolp(false),
			Transport:   "wireproxy",
		},
	}
	tbl := mustTable(t, rc)
	r := tbl.Match("/openai")
	if r == nil {
		t.Fatal("Match(/openai) = nil")
	}
	if r.Name != "openai" || r.Prefix != "/openai" || r.TransportName != "wireproxy" {
		t.Errorf("route fields = %+v", r)
	}
	if r.StripPrefix {
		t.Error("StripPrefix = true, want false")
	}
	if r.Target.Scheme != "https" || r.Target.Host != "api.example.com:8443" {
		t.Errorf("Target = %q", r.Target.String())
	}
	// nil StripPrefix defaults to true (config.StripsPrefix).
	tbl2 := mustTable(t, route("d", "/d", "https://d.example"))
	if r2 := tbl2.Match("/d"); !r2.StripPrefix {
		t.Error("default StripPrefix = false, want true")
	}
}

func TestBuildTableInvalidTarget(t *testing.T) {
	_, err := BuildTable([]config.RouteConfig{
		route("bad", "/bad", "https://exa mple.com/v1"),
	}, "")
	if err == nil {
		t.Fatal("BuildTable with unparsable target: err = nil, want error")
	}
	if !strings.Contains(err.Error(), "bad") {
		t.Errorf("error %q does not name the route", err)
	}
	// Disabled routes are skipped before parsing, so a disabled bad
	// target must not fail the build.
	bad := route("bad", "/bad", "https://exa mple.com/v1")
	bad.Enabled = boolp(false)
	if _, err := BuildTable([]config.RouteConfig{bad}, ""); err != nil {
		t.Errorf("BuildTable skipping disabled bad target: %v", err)
	}
}

func TestRoutesSortedByName(t *testing.T) {
	tbl := mustTable(t,
		route("zebra", "/z", "https://z.example"),
		route("alpha", "/a", "https://a.example"),
		route("mid", "/m", "https://m.example"),
	)
	got := tbl.Routes()
	want := []string{"alpha", "mid", "zebra"}
	if len(got) != len(want) {
		t.Fatalf("Routes() len = %d, want %d", len(got), len(want))
	}
	for i, n := range want {
		if got[i].Name != n {
			t.Errorf("Routes()[%d] = %q, want %q", i, got[i].Name, n)
		}
	}
	// Returned slice is a copy: mutating it must not affect the table.
	got[0] = nil
	if again := tbl.Routes(); again[0] == nil || again[0].Name != "alpha" {
		t.Error("Routes() does not return a fresh slice")
	}
}

func TestEmptyTable(t *testing.T) {
	tbl := mustTable(t)
	if tbl.Len() != 0 {
		t.Errorf("Len() = %d, want 0", tbl.Len())
	}
	if got := tbl.Match("/anything"); got != nil {
		t.Errorf("Match on empty table = %q, want nil", got.Name)
	}
	if got := tbl.Routes(); len(got) != 0 {
		t.Errorf("Routes() on empty table has %d entries", len(got))
	}
}
