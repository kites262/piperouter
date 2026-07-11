// Package webui serves the embedded WebUI static files (PRD §18, §20.2).
//
// Build pipeline (single source of truth — no manual hash patching):
//
//	web/  ──vite build──▶  internal/webui/dist/  ──go:embed──▶  binary
//
// Vite writes directly into dist/ with stable asset names (assets/app.js,
// assets/app.css). The dist/ directory is fully gitignored.
//
// For `go test` / `go build` without a frontend build, `make ensure-embed`
// (wired into `make test` / `make backend`) creates dist/.gitkeep so
// //go:embed has at least one file. Available() is false until a real
// index.html is present (after `make frontend`).
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
// exists. When false the admin server mounts no UI at all (e.g. only a
// throwaway .gitkeep was embedded for compile-time //go:embed).
func Available() bool {
	info, err := fs.Stat(distFS, distRoot+"/index.html")
	return err == nil && !info.IsDir()
}

// Handler serves the embedded frontend:
//
//   - an exact file match is served with its correct Content-Type;
//   - a path under assets/ that matches no embedded file → 404
//     (a missing asset must never receive HTML);
//   - anything else (SPA routes like /routes/foo) falls back to index.html.
//
// Caching: the binary is the deployment unit. Both the SPA shell and the
// stable-named assets are served with Cache-Control: no-cache so a replaced
// binary is visible immediately (content hashes are intentionally not used).
func Handler() http.Handler {
	sub, err := fs.Sub(distFS, distRoot)
	if err != nil {
		// Degrade if the embed root is somehow missing.
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
	// shell. Real assets live under assets/, so only that prefix gets the
	// strict treatment. Everything else falls back to index.html — including
	// SPA deep links whose last segment contains a dot, e.g. a route named
	// "api.v1" at /routes/api.v1 (config.NamePattern allows dots).
	if strings.HasPrefix(name, "assets/") {
		http.NotFound(w, r)
		return
	}

	// SPA route (e.g. /routes/foo): serve the shell, client router takes over.
	h.serveFile(w, r, "index.html")
}

// serveFile writes one embedded file. Content-Type comes from the file
// extension via http.ServeContent.
func (h *uiHandler) serveFile(w http.ResponseWriter, r *http.Request, name string) {
	f, err := h.fsys.Open(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()

	// Stable asset names (no content hash) → no immutable long-cache.
	// The next binary deploy is the cache-bust signal.
	w.Header().Set("Cache-Control", "no-cache")

	if rs, ok := f.(io.ReadSeeker); ok {
		// embed.FS regular files implement io.ReadSeeker; ServeContent sets
		// Content-Type from the extension and handles HEAD/Range.
		http.ServeContent(w, r, name, time.Time{}, rs)
		return
	}
	// Unreachable for embed.FS; conservative fallback.
	http.NotFound(w, r)
}
