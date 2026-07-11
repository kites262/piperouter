package proxy

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestResponseRecorderStatusCapture(t *testing.T) {
	t.Run("explicit status, first write wins", func(t *testing.T) {
		inner := httptest.NewRecorder()
		rw := newResponseRecorder(inner)
		rw.WriteHeader(http.StatusTeapot)
		rw.WriteHeader(http.StatusOK) // superfluous, must not overwrite
		if rw.status != http.StatusTeapot || !rw.wroteHeader {
			t.Fatalf("status = %d, wroteHeader = %v", rw.status, rw.wroteHeader)
		}
	})

	t.Run("implicit 200 on first write", func(t *testing.T) {
		inner := httptest.NewRecorder()
		rw := newResponseRecorder(inner)
		if rw.wroteHeader || rw.status != 0 {
			t.Fatalf("fresh recorder: status = %d, wroteHeader = %v", rw.status, rw.wroteHeader)
		}
		if _, err := rw.Write([]byte("x")); err != nil {
			t.Fatalf("write: %v", err)
		}
		if rw.status != http.StatusOK || !rw.wroteHeader {
			t.Fatalf("status = %d, wroteHeader = %v", rw.status, rw.wroteHeader)
		}
		if inner.Body.String() != "x" {
			t.Fatalf("inner body = %q", inner.Body.String())
		}
	})

	t.Run("Unwrap exposes the real writer for ResponseController", func(t *testing.T) {
		inner := httptest.NewRecorder()
		rw := newResponseRecorder(inner)
		if got := rw.Unwrap(); got != http.ResponseWriter(inner) {
			t.Fatalf("Unwrap = %#v, want the inner writer", got)
		}
		// ResponseController must reach the inner Flusher through Unwrap.
		if err := http.NewResponseController(rw).Flush(); err != nil {
			t.Fatalf("Flush through ResponseController: %v", err)
		}
		if !inner.Flushed {
			t.Fatal("inner recorder was not flushed")
		}
	})
}
