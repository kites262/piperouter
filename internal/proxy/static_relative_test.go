package proxy

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kites262/piperouter/internal/config"
	"github.com/kites262/piperouter/internal/router"
	"github.com/kites262/piperouter/internal/runtime"
	"github.com/kites262/piperouter/internal/transport"
)

// Relative target is resolved once in BuildTable into Route.File; ServeHTTP
// only reads that absolute path (no per-request Join/Abs).
func TestServeStaticRelativeE2E(t *testing.T) {
	dir := t.TempDir()
	rel := "site/index.html"
	abs := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	const body = "relative-static-body"
	if err := os.WriteFile(abs, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Parse([]byte(`
version: v0.3
routes:
  - name: landing
    type: static
    prefix: /
    options:
      file: ` + rel + `
`))
	if err != nil {
		t.Fatal(err)
	}
	if err := config.Validate(cfg, dir); err != nil {
		t.Fatal(err)
	}
	if cfg.Routes[0].Static.File != rel {
		t.Fatalf("config file rewritten to %q, want relative %q", cfg.Routes[0].Static.File, rel)
	}
	table, err := router.BuildTable(cfg.Routes, dir)
	if err != nil {
		t.Fatal(err)
	}
	if f := table.Match("/").File; f != filepath.Clean(abs) {
		t.Fatalf("compiled File = %q, want %q", f, filepath.Clean(abs))
	}
	pool, err := transport.NewPool(nil, cfg.Network)
	if err != nil {
		t.Fatal(err)
	}
	snap := &runtime.Snapshot{
		Config:   cfg,
		Table:    table,
		Pool:     pool,
		Revision: "sha256:test",
		LoadedAt: time.Now(),
	}
	tp := newTestProxy(t, snap)
	resp, err := http.Get(tp.server.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	got, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || string(got) != body {
		t.Fatalf("status=%d body=%q, want 200 %q", resp.StatusCode, got, body)
	}
}
