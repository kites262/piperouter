// Package webui serves the embedded WebUI static files (PRD §18, §20.2).
//
// The frontend build output is copied to dist/ by `make embed` and compiled
// into the binary via go:embed, so the final release depends on no Node
// runtime or external static server. A placeholder dist/index.html stays
// committed so the package always builds even without a frontend build.
package webui

import (
	"embed"
	"io"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"
)

//go:embed all:dist
var distFS embed.FS

// distRoot is the embedded subdirectory holding the built frontend.
const distRoot = "dist"

// Available reports whether an embedded UI entry point (dist/index.html)
// exists. When false the admin server mounts no UI at all.
func Available() bool {
	info, err := fs.Stat(distFS, distRoot+"/index.html")
	return err == nil && !info.IsDir()
}

// Handler serves the embedded frontend:
//
//   - an exact file match is served with its correct Content-Type;
//   - a path that has a file extension but matches no embedded file → 404
//     (a missing asset must never receive HTML);
//   - anything else (SPA routes like /routes/foo) falls back to index.html.
//
// Caching (PRD §18): index.html is served with Cache-Control: no-cache so a
// new binary is picked up immediately; hashed files under /assets/ are
// immutable for one year.
func Handler() http.Handler {
	sub, err := fs.Sub(distFS, distRoot)
	if err != nil {
		// Cannot happen with the committed placeholder; degrade to 404s.
		return http.NotFoundHandler()
	}
	return &uiHandler{fsys: sub}
}

type uiHandler struct {
	fsys fs.FS
}

func (h *uiHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Clean the decoded path with a leading slash so ".." can never escape
	// the embedded root, then convert to an fs.FS-relative name.
	name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if name == "" {
		name = "index.html"
	}

	if info, err := fs.Stat(h.fsys, name); err == nil && !info.IsDir() {
		h.serveFile(w, r, name)
		return
	}

	// A MISSING build asset must 404 rather than silently returning the SPA
	// shell. Real hashed assets all live under assets/, so only that prefix
	// gets the strict treatment. Everything else falls back to index.html —
	// including SPA deep links whose last segment contains a dot, e.g. a
	// route named "api.v1" at /routes/api.v1 (config.NamePattern allows dots).
	if strings.HasPrefix(name, "assets/") {
		http.NotFound(w, r)
		return
	}

	// SPA route (e.g. /routes/foo): serve the shell, client router takes over.
	h.serveFile(w, r, "index.html")
}

// serveFile writes one embedded file with the cache policy derived from its
// path. Content-Type comes from the file extension via http.ServeContent.
func (h *uiHandler) serveFile(w http.ResponseWriter, r *http.Request, name string) {
	f, err := h.fsys.Open(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()

	switch {
	case name == "index.html":
		w.Header().Set("Cache-Control", "no-cache")
	case strings.HasPrefix(name, "assets/"):
		// Vite emits content-hashed filenames under assets/: safe to cache
		// forever (PRD §18).
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}

	if rs, ok := f.(io.ReadSeeker); ok {
		// embed.FS regular files implement io.ReadSeeker; ServeContent sets
		// Content-Type from the extension and handles HEAD/Range.
		http.ServeContent(w, r, name, time.Time{}, rs)
		return
	}
	// Unreachable for embed.FS; conservative fallback.
	http.NotFound(w, r)
}
