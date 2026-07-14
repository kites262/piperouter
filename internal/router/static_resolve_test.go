package router

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kites262/piperouter/internal/config"
)

// Relative static targets are resolved once in BuildTable into Route.File.
// The data plane only reads that absolute path — never joins per request.
func TestBuildTableResolvesRelativeStaticOnce(t *testing.T) {
	dir := t.TempDir()
	rel := "pages/home.html"
	abs := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte("body"), 0o644); err != nil {
		t.Fatal(err)
	}

	tbl, err := BuildTable([]config.RouteConfig{{
		Name:   "home",
		Type:   config.RouteTypeStatic,
		Prefix: "/",
		Static: &config.StaticOptions{File: rel},
	}}, dir)
	if err != nil {
		t.Fatal(err)
	}
	r := tbl.Match("/")
	if r == nil || !r.IsStatic() {
		t.Fatal("expected static route")
	}
	want, _ := filepath.Abs(abs)
	// BuildTable uses Clean(Join); normalize want the same way.
	want = filepath.Clean(abs)
	if r.File != want {
		t.Fatalf("Route.File = %q, want %q (resolved at build, not hot path)", r.File, want)
	}
	// Config-facing target string is not stored on Route; only File is.
	if r.Target != nil {
		t.Fatal("static route must not set proxy Target URL")
	}
}
