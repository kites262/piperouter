package webui

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The tests run against the real embedded dist/. In a source checkout that
// is the committed placeholder index.html; after `make embed` it is the full
// frontend build. Assertions therefore target contract behavior (status,
// cache headers, fallback) rather than specific file contents.

func TestAvailable(t *testing.T) {
	if !Available() {
		t.Fatal("Available() = false; the committed placeholder dist/index.html must make the UI available")
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
			// Only missing ASSETS (under assets/) 404; other dotted paths
			// are SPA deep links and must get the shell.
			name:       "missing asset with extension is 404",
			path:       "/assets/nope-12345.js",
			wantStatus: http.StatusNotFound,
		},
		{
			// A route name may contain a dot (config.NamePattern), so its
			// detail deep link ends in ".something"; it must serve the SPA
			// shell, not 404 (regression: dotted SPA routes 404'd before).
			name:         "SPA deep link with a dotted last segment",
			path:         "/routes/api.v1",
			wantStatus:   http.StatusOK,
			wantCache:    "no-cache",
			wantHTMLType: true,
			wantBody:     indexBody,
		},
		{
			// The cleaned path stays inside the embedded root; a
			// traversal attempt gets the SPA shell, never a real file.
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

// TestAssetCacheHeaders asserts the immutable cache policy for every real
// file under assets/ in the embed. With only the placeholder committed there
// are no assets and the loop body simply never runs (the 404-for-missing-
// asset case is covered in TestHandler).
func TestAssetCacheHeaders(t *testing.T) {
	h := Handler()
	entries, err := fs.ReadDir(distFS, distRoot+"/assets")
	if err != nil {
		t.Skip("no embedded assets directory (placeholder build)")
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		rec := get(t, h, "/assets/"+e.Name())
		if rec.Code != http.StatusOK {
			t.Errorf("GET /assets/%s status = %d, want 200", e.Name(), rec.Code)
			continue
		}
		const want = "public, max-age=31536000, immutable"
		if got := rec.Header().Get("Cache-Control"); got != want {
			t.Errorf("GET /assets/%s Cache-Control = %q, want %q", e.Name(), got, want)
		}
	}
}
