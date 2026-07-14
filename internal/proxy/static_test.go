package proxy

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestServeStaticFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "index.html")
	const body = "<html><body>hello static</body></html>"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	yaml := `
version: v0.3
routes:
  - name: landing
    type: static
    prefix: /
    options:
      file: ` + path + `
  - name: api
    prefix: /v1
    options:
      target: http://127.0.0.1:9
`
	snap := buildSnapshot(t, yaml)
	tp := newTestProxy(t, snap)

	// Any path under / that is not a longer prefix hits the static file.
	for _, p := range []string{"/", "/index.html", "/anything"} {
		resp, err := http.Get(tp.server.URL + p)
		if err != nil {
			t.Fatalf("GET %s: %v", p, err)
		}
		got, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s: status = %d, want 200", p, resp.StatusCode)
		}
		if string(got) != body {
			t.Fatalf("GET %s: body = %q, want %q", p, got, body)
		}
		if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/html") {
			t.Errorf("GET %s: Content-Type = %q, want text/html", p, ct)
		}
	}

	// Longer prefix /v1 still wins over root static.
	resp, err := http.Get(tp.server.URL + "/v1/models")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	// Upstream is dead → 502, not the static file.
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("GET /v1/models: status = %d, want 502 (proxy path)", resp.StatusCode)
	}
}

// match: exact turns a root static route from a catch-all into a single
// page: only "/" serves the file, scanner-style paths fall through to the
// anonymous unmatched 404.
func TestServeStaticExactMatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "index.html")
	const body = "<html><body>home</body></html>"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	yaml := `
version: v0.3
routes:
  - name: landing
    type: static
    prefix: /
    match: exact
    options:
      file: ` + path + `
  - name: api
    prefix: /v1
    options:
      target: http://127.0.0.1:9
`
	tp := newTestProxy(t, buildSnapshot(t, yaml))

	resp, err := http.Get(tp.server.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(got) != body {
		t.Fatalf("GET /: status = %d body = %q, want 200 with the file", resp.StatusCode, got)
	}

	for _, p := range []string{"/index.html", "/wp-admin/install.php", "/.env", "/anything"} {
		resp, err := http.Get(tp.server.URL + p)
		if err != nil {
			t.Fatalf("GET %s: %v", p, err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("GET %s: status = %d, want 404", p, resp.StatusCode)
		}
		if strings.Contains(string(b), "route_not_found") {
			t.Fatalf("GET %s: body %q leaks the internal error code", p, b)
		}
	}

	// The longer /v1 prefix route is unaffected by the exact root route.
	resp, err = http.Get(tp.server.URL + "/v1/models")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("GET /v1/models: status = %d, want 502 (proxy path)", resp.StatusCode)
	}
}

func TestServeStaticMethodNotAllowed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "page.txt")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	yaml := `
version: v0.3
routes:
  - name: landing
    type: static
    prefix: /
    options:
      file: ` + path + `
`
	tp := newTestProxy(t, buildSnapshot(t, yaml))
	req, err := http.NewRequest(http.MethodPost, tp.server.URL+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", resp.StatusCode)
	}
	if allow := resp.Header.Get("Allow"); !strings.Contains(allow, "GET") {
		t.Errorf("Allow = %q, want GET", allow)
	}
}

func TestServeStaticMissingFile(t *testing.T) {
	// Path is absolute and valid at config shape level; file does not exist.
	// Validate allows missing files; ServeFile returns 404.
	path := filepath.Join(t.TempDir(), "gone.html")
	yaml := `
version: v0.3
routes:
  - name: landing
    type: static
    prefix: /home
    options:
      file: ` + path + `
`
	// Validate is called inside buildSnapshot — missing file must be accepted.
	tp := newTestProxy(t, buildSnapshot(t, yaml))
	resp, err := http.Get(tp.server.URL + "/home")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}
