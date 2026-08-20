package proxy

import (
	"net/http"

	"github.com/kites262/piperouter/internal/logging"
)

// responseRecorder wraps the server's ResponseWriter to record the final
// status code and whether anything was written, without interfering with
// streaming. It deliberately does NOT implement Flusher/Hijacker itself:
// it exposes the underlying writer via Unwrap so http.ResponseController
// (used by httputil.ReverseProxy for flushing and hijacking) reaches the
// real implementation (Go 1.20+ mechanism).
type responseRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
	details     *logging.Capture
}

func newResponseRecorder(w http.ResponseWriter) *responseRecorder {
	return &responseRecorder{ResponseWriter: w}
}

func (r *responseRecorder) WriteHeader(code int) {
	if !r.wroteHeader {
		r.status = code
		r.wroteHeader = true
		if r.details != nil {
			r.details.ResponseHeaders(r.Header())
		}
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *responseRecorder) Write(b []byte) (int, error) {
	if !r.wroteHeader {
		r.status = http.StatusOK
		r.wroteHeader = true
		if r.details != nil {
			r.details.ResponseHeaders(r.Header())
		}
	}
	if r.details != nil {
		r.details.ResponseBody(b)
	}
	return r.ResponseWriter.Write(b)
}

// Unwrap exposes the underlying ResponseWriter to http.ResponseController.
func (r *responseRecorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}
