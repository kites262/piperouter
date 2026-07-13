package proxy

import (
	"net/http"

	"github.com/kites262/piperouter/internal/router"
)

// serveStatic serves the single file configured on a static route for every
// matching request. Only GET and HEAD are allowed (405 otherwise). Directories
// are rejected at config validation time; a missing file yields the normal
// http.ServeFile 404.
//
// Performance: route.File is already an absolute path resolved once in
// router.BuildTable (config load / hot-reload). This hot path must not call
// filepath.Join, filepath.Abs, or touch the raw config target string.
func (h *handler) serveStatic(rw *responseRecorder, r *http.Request, route *router.Route, st *requestState) {
	switch r.Method {
	case http.MethodGet, http.MethodHead:
	default:
		rw.Header().Set("Allow", "GET, HEAD")
		writeJSONError(rw, http.StatusMethodNotAllowed, errMethodNotAllowed)
		st.errClass = errMethodNotAllowed
		return
	}
	// ServeFile sets Content-Type from extension, supports Range, and
	// handles If-Modified-Since. Path is absolute from BuildTable.
	http.ServeFile(rw, r, route.File)
}
