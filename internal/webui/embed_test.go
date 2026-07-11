package webui

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Tests run against whatever is currently embedded in dist/:
//   - after `make ensure-embed` only: dist/.gitkeep → Available() false
//   - after `make frontend`: full Vite build → Available() true
// Assertions adapt so both modes pass.

func TestAvailable(t *testing.T) {
	_, err := fs.Stat(distFS, distRoot+"/index.html")
	want := err == nil
	if Available() != want {
		t.Fatalf("Available() = %v, want %v (index.html present=%v)", Available(), want, want)
	}
}

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestHandler(t *testing.T) {
	h := Handler()

	if !Available() {
		// Compile-only embed (.gitkeep): no SPA shell — index routes 404,
		// missing assets still 404, and we never mount this handler in app
		// when Available() is false.
		if rec := get(t, h, "/"); rec.Code != http.StatusNotFound {
			t.Fatalf("GET / without UI status = %d, want 404", rec.Code)
		}
		if rec := get(t, h, "/assets/nope.js"); rec.Code != http.StatusNotFound {
			t.Fatalf("GET missing asset status = %d, want 404", rec.Code)
		}
		// .gitkeep itself is embed fodder, not a public route of interest.
		return
	}

	indexBody := get(t, h, "/").Body.String()
	if indexBody == "" {
		t.Fatal("GET / returned an empty body")
	}

	tests := []struct {
		name         string
		path         string
		wantStatus   int
		wantCache    string
		wantHTMLType bool
		wantBody     string // exact expected body, "" = don't check
	}{
		{
			name:         "index served at root",
			path:         "/",
			wantStatus:   http.StatusOK,
			wantCache:    "no-cache",
			wantHTMLType: true,
			wantBody:     indexBody,
		},
		{
			name:         "index served at explicit path",
			path:         "/index.html",
			wantStatus:   http.StatusOK,
			wantCache:    "no-cache",
			wantHTMLType: true,
			wantBody:     indexBody,
		},
		{
			name:         "SPA fallback for client route",
			path:         "/routes/x",
			wantStatus:   http.StatusOK,
			wantCache:    "no-cache",
			wantHTMLType: true,
			wantBody:     indexBody,
		},
		{
			name:         "SPA fallback for nested client route",
			path:         "/logs/deep/nested",
			wantStatus:   http.StatusOK,
			wantCache:    "no-cache",
			wantHTMLType: true,
			wantBody:     indexBody,
		},
		{
			name:       "missing asset with extension is 404",
			path:       "/assets/nope-12345.js",
			wantStatus: http.StatusNotFound,
		},
		{
			name:         "SPA deep link with a dotted last segment",
			path:         "/routes/api.v1",
			wantStatus:   http.StatusOK,
			wantCache:    "no-cache",
			wantHTMLType: true,
			wantBody:     indexBody,
		},
		{
			name:         "dot-dot cannot escape the embedded root",
			path:         "/../../etc/passwd",
			wantStatus:   http.StatusOK,
			wantCache:    "no-cache",
			wantHTMLType: true,
			wantBody:     indexBody,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := get(t, h, tt.path)
			if rec.Code != tt.wantStatus {
				t.Fatalf("GET %s status = %d, want %d", tt.path, rec.Code, tt.wantStatus)
			}
			if tt.wantCache != "" {
				if got := rec.Header().Get("Cache-Control"); got != tt.wantCache {
					t.Errorf("GET %s Cache-Control = %q, want %q", tt.path, got, tt.wantCache)
				}
			}
			if tt.wantHTMLType {
				if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
					t.Errorf("GET %s Content-Type = %q, want text/html", tt.path, ct)
				}
			}
			if tt.wantBody != "" && rec.Body.String() != tt.wantBody {
				t.Errorf("GET %s body differs from index.html", tt.path)
			}
		})
	}
}

// TestAssetCacheHeaders asserts no-cache for every real file under assets/
// (stable names, binary is the version unit). Skips when no assets embedded.
func TestAssetCacheHeaders(t *testing.T) {
	h := Handler()
	entries, err := fs.ReadDir(distFS, distRoot+"/assets")
	if err != nil {
		t.Skip("no embedded assets directory (run `make frontend` for a full embed)")
	}
	for _, e := range entries {
		if e.IsDir() {
			sub, err := fs.ReadDir(distFS, distRoot+"/assets/"+e.Name())
			if err != nil {
				continue
			}
			for _, child := range sub {
				if child.IsDir() {
					continue
				}
				path := "/assets/" + e.Name() + "/" + child.Name()
				rec := get(t, h, path)
				if rec.Code != http.StatusOK {
					t.Errorf("GET %s status = %d, want 200", path, rec.Code)
					continue
				}
				if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
					t.Errorf("GET %s Cache-Control = %q, want no-cache", path, got)
				}
			}
			continue
		}
		rec := get(t, h, "/assets/"+e.Name())
		if rec.Code != http.StatusOK {
			t.Errorf("GET /assets/%s status = %d, want 200", e.Name(), rec.Code)
			continue
		}
		if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
			t.Errorf("GET /assets/%s Cache-Control = %q, want no-cache", e.Name(), got)
		}
	}
}

// TestStableAssetNames documents the production embed contract: after a real
// frontend build, the shell must reference the stable entrypoints.
func TestStableAssetNames(t *testing.T) {
	if !Available() {
		t.Skip("no embedded UI — run `make frontend` for a full embed")
	}
	body := get(t, Handler(), "/").Body.String()
	if !strings.Contains(body, "/assets/app.js") {
		t.Errorf("index.html must reference /assets/app.js; got shell without stable entry")
	}
	if !strings.Contains(body, "/assets/app.css") && !strings.Contains(body, "assets/app.css") {
		t.Errorf("index.html must reference assets/app.css; got shell without stable stylesheet")
	}
	for _, part := range strings.FieldsFunc(body, func(r rune) bool {
		return r == '"' || r == '\'' || r == ' ' || r == '>' || r == '<'
	}) {
		if strings.HasPrefix(part, "/assets/index-") || strings.Contains(part, "assets/index-") {
			t.Errorf("hashed entry asset leaked into index.html: %s", part)
		}
	}
}
