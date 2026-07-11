package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kites262/piperouter/internal/config"
	"github.com/kites262/piperouter/internal/webui"
)

// newBackend starts an upstream test server that reports the path it saw.
func newBackend(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "backend:%s", r.URL.Path)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// writeTestConfig writes a minimal valid config with one /api route to a
// temp dir and returns the config path.
func writeTestConfig(t *testing.T, target string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "piperouter.yaml")
	yaml := fmt.Sprintf(`version: 1
server:
  proxy:
    listen: "127.0.0.1:0"
  admin:
    enabled: true
    listen: "127.0.0.1:0"
runtime:
  log_level: info
  recent_logs: 64
routes:
  - name: api
    prefix: /api
    target: %s
    strip_prefix: true
`, target)
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

// startApp starts the application and registers a bounded-time shutdown.
func startApp(t *testing.T, opts Options) *App {
	t.Helper()
	a, err := Start(context.Background(), opts)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		start := time.Now()
		if err := a.Shutdown(ctx); err != nil {
			t.Errorf("Shutdown: %v", err)
		}
		if d := time.Since(start); d > 5*time.Second {
			t.Errorf("Shutdown took %v, want < 5s", d)
		}
		select {
		case err := <-a.serveErr:
			t.Errorf("server reported fatal error: %v", err)
		default:
		}
	})
	return a
}

func get(t *testing.T, url string) (*http.Response, string) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatalf("GET %s read body: %v", url, err)
	}
	return resp, string(body)
}

func TestStartServesProxyAdminAndWebUI(t *testing.T) {
	backend := newBackend(t)
	a := startApp(t, Options{
		ConfigPath: writeTestConfig(t, backend.URL),
		Version:    "test",
	})

	if a.ProxyAddr() == "" || strings.HasSuffix(a.ProxyAddr(), ":0") {
		t.Fatalf("ProxyAddr = %q, want a bound port", a.ProxyAddr())
	}
	if a.AdminAddr() == "" || strings.HasSuffix(a.AdminAddr(), ":0") {
		t.Fatalf("AdminAddr = %q, want a bound port", a.AdminAddr())
	}

	t.Run("unmatched path yields route_not_found", func(t *testing.T) {
		resp, body := get(t, "http://"+a.ProxyAddr()+"/nope")
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", resp.StatusCode)
		}
		if !strings.Contains(body, `"error":"route_not_found"`) {
			t.Fatalf("body = %q, want route_not_found error", body)
		}
	})

	t.Run("matched route proxies to backend", func(t *testing.T) {
		resp, body := get(t, "http://"+a.ProxyAddr()+"/api/hello")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body %q)", resp.StatusCode, body)
		}
		if body != "backend:/hello" {
			t.Fatalf("body = %q, want %q (strip_prefix rewrite)", body, "backend:/hello")
		}
	})

	t.Run("admin status endpoint responds", func(t *testing.T) {
		resp, body := get(t, "http://"+a.AdminAddr()+"/api/v1/status")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body %q)", resp.StatusCode, body)
		}
		if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "json") {
			t.Errorf("Content-Type = %q, want JSON", ct)
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(body), &payload); err != nil {
			t.Fatalf("status body is not JSON: %v", err)
		}
	})

	t.Run("webui served at admin root with no-cache", func(t *testing.T) {
		resp, body := get(t, "http://"+a.AdminAddr()+"/")
		if !webui.Available() {
			// dist/ is gitignored; without `make frontend` only .gitkeep is
			// embedded and the admin plane does not mount the SPA.
			if resp.StatusCode != http.StatusNotFound {
				t.Fatalf("status = %d, want 404 when WebUI is not embedded", resp.StatusCode)
			}
			return
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		if cc := resp.Header.Get("Cache-Control"); cc != "no-cache" {
			t.Errorf("Cache-Control = %q, want no-cache", cc)
		}
		if body == "" {
			t.Error("index body is empty")
		}
	})
}

func TestDisableAdmin(t *testing.T) {
	backend := newBackend(t)
	a := startApp(t, Options{
		ConfigPath:   writeTestConfig(t, backend.URL),
		DisableAdmin: true,
	})

	if got := a.AdminAddr(); got != "" {
		t.Fatalf("AdminAddr = %q, want empty when admin disabled", got)
	}
	resp, body := get(t, "http://"+a.ProxyAddr()+"/api/ping")
	if resp.StatusCode != http.StatusOK || body != "backend:/ping" {
		t.Fatalf("proxy with admin disabled: status %d body %q, want 200 backend:/ping", resp.StatusCode, body)
	}
}

func TestDisableWeb(t *testing.T) {
	backend := newBackend(t)
	a := startApp(t, Options{
		ConfigPath: writeTestConfig(t, backend.URL),
		DisableWeb: true,
	})

	resp, body := get(t, "http://"+a.AdminAddr()+"/")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET / with web disabled: status %d, want 404", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "json") {
		t.Errorf("GET / with web disabled: Content-Type = %q, want JSON error", ct)
	}
	if !strings.Contains(body, "error") {
		t.Errorf("GET / with web disabled: body = %q, want JSON error", body)
	}

	resp, _ = get(t, "http://"+a.AdminAddr()+"/api/v1/status")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("API with web disabled: status %d, want 200", resp.StatusCode)
	}
}

func TestHotReloadAddsRoute(t *testing.T) {
	backend := newBackend(t)
	cfgPath := writeTestConfig(t, backend.URL)
	a := startApp(t, Options{ConfigPath: cfgPath})

	oldRev := a.manager.Current().Revision

	// New route must be unknown before the reload.
	resp, _ := get(t, "http://"+a.ProxyAddr()+"/extra/ping")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("pre-reload /extra status = %d, want 404", resp.StatusCode)
	}

	// External edit: load, add a route, write atomically (PRD §6.6).
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	cfg.Routes = append(cfg.Routes, config.RouteConfig{
		Name:   "extra",
		Prefix: "/extra",
		Target: backend.URL,
	})
	cfg.Normalize()
	if err := config.WriteAtomic(cfgPath, cfg); err != nil {
		t.Fatalf("write config: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for a.manager.Current().Revision == oldRev {
		if time.Now().After(deadline) {
			t.Fatal("configuration revision did not change within 3s of the file edit")
		}
		time.Sleep(25 * time.Millisecond)
	}

	resp, body := get(t, "http://"+a.ProxyAddr()+"/extra/ping")
	if resp.StatusCode != http.StatusOK || body != "backend:/ping" {
		t.Fatalf("post-reload /extra: status %d body %q, want 200 backend:/ping", resp.StatusCode, body)
	}
}

func TestStartErrors(t *testing.T) {
	backend := newBackend(t)

	tests := []struct {
		name string
		opts func(t *testing.T) Options
		want string // substring of the error
	}{
		{
			name: "missing config file",
			opts: func(t *testing.T) Options {
				return Options{ConfigPath: filepath.Join(t.TempDir(), "absent.yaml")}
			},
			want: "invalid configuration",
		},
		{
			name: "invalid config content",
			opts: func(t *testing.T) Options {
				path := filepath.Join(t.TempDir(), "bad.yaml")
				if err := os.WriteFile(path, []byte("version: 99\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				return Options{ConfigPath: path}
			},
			want: "invalid configuration",
		},
		{
			name: "invalid CLI log level",
			opts: func(t *testing.T) Options {
				return Options{ConfigPath: writeTestConfig(t, backend.URL), LogLevel: "loud"}
			},
			want: "log level",
		},
		{
			name: "unbindable proxy listen override",
			opts: func(t *testing.T) Options {
				// Occupy a port so the proxy bind fails immediately.
				ln, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { ln.Close() })
				return Options{ConfigPath: writeTestConfig(t, backend.URL), ProxyListen: ln.Addr().String()}
			},
			want: "bind proxy listener",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, err := Start(context.Background(), tt.opts(t))
			if err == nil {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_ = a.Shutdown(ctx)
				t.Fatal("Start succeeded, want error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %q, want substring %q", err, tt.want)
			}
		})
	}
}

func TestIsLoopbackListen(t *testing.T) {
	tests := []struct {
		addr string
		want bool
	}{
		{"127.0.0.1:9090", true},
		{"127.0.0.2:9090", true},
		{"localhost:9090", true},
		{"[::1]:9090", true},
		{":9090", false},
		{"0.0.0.0:9090", false},
		{"192.168.1.10:9090", false},
		{"example.com:9090", false},
		{"garbage", false},
	}
	for _, tt := range tests {
		if got := isLoopbackListen(tt.addr); got != tt.want {
			t.Errorf("isLoopbackListen(%q) = %v, want %v", tt.addr, got, tt.want)
		}
	}
}
