package proxy

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kites262/piperouter/internal/overwrite"
	"github.com/kites262/piperouter/internal/router"
)

const (
	maxConcurrentOverwrites = 8
	overwriteReadTimeout    = 30 * time.Second
)

// prepareOverwrite runs before contacting an upstream. Its semaphore is held
// until the buffered outbound body is closed, rather than for the SSE lifetime.
func (h *handler) prepareOverwrite(w *responseRecorder, r *http.Request, route *router.Route, st *requestState) (*http.Request, func(), bool) {
	fail := func(status int, code string) (*http.Request, func(), bool) {
		st.errClass = code
		if r.ProtoMajor == 1 {
			// An unread slow body must not make net/http drain indefinitely
			// before flushing the error, or later while closing the request.
			w.Header().Set("Connection", "close")
			_ = http.NewResponseController(w).SetReadDeadline(time.Now())
		}
		writeJSONError(w, status, code)
		return nil, nil, false
	}
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	media = strings.ToLower(media)
	encoding := strings.TrimSpace(r.Header.Get("Content-Encoding"))
	if err != nil || !(media == "application/json" || (strings.HasPrefix(media, "application/") && strings.HasSuffix(media, "+json"))) || (encoding != "" && !strings.EqualFold(encoding, "identity")) {
		return fail(http.StatusUnsupportedMediaType, "overwrite_unsupported_media_type")
	}
	if r.ContentLength > overwrite.MaxBodyBytes {
		return fail(http.StatusRequestEntityTooLarge, "overwrite_body_too_large")
	}
	select {
	case h.overwriteSlots <- struct{}{}:
	default:
		return fail(http.StatusServiceUnavailable, "overwrite_busy")
	}
	var once sync.Once
	release := func() { once.Do(func() { <-h.overwriteSlots }) }
	success := false
	defer func() {
		if !success {
			release()
		}
	}()

	controller := http.NewResponseController(w)
	var timedOut atomic.Bool
	var timer *time.Timer
	if err := controller.SetReadDeadline(time.Now().Add(overwriteReadTimeout)); err == nil {
		defer func() {
			if success {
				_ = controller.SetReadDeadline(time.Time{})
			}
		}()
	} else if errors.Is(err, http.ErrNotSupported) {
		// Non-network ResponseWriters (e.g. test harnesses) have no deadline;
		// close the reader to interrupt a blocking read instead.
		timer = time.AfterFunc(overwriteReadTimeout, func() {
			timedOut.Store(true)
			_ = r.Body.Close()
		})
		defer timer.Stop()
	} else {
		return fail(http.StatusInternalServerError, "overwrite_read_failed")
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, overwrite.MaxBodyBytes+1))
	if err != nil || timedOut.Load() {
		var timeout net.Error
		if timedOut.Load() || (errors.As(err, &timeout) && timeout.Timeout()) {
			return fail(http.StatusRequestTimeout, "overwrite_read_timeout")
		}
		if errors.Is(r.Context().Err(), context.Canceled) {
			st.errClass, st.skipObserve = errClientCanceled, true
			return nil, nil, false
		}
		return fail(http.StatusBadRequest, "overwrite_read_failed")
	}
	if len(body) > overwrite.MaxBodyBytes {
		return fail(http.StatusRequestEntityTooLarge, "overwrite_body_too_large")
	}
	// A chunked empty body is equivalent to an ordinary bodyless request.
	result := body
	if len(body) > 0 {
		result, err = route.Overwrite.Replace(body)
		if errors.Is(err, overwrite.ErrTooLarge) {
			return fail(http.StatusRequestEntityTooLarge, "overwrite_body_too_large")
		}
		if err != nil {
			return fail(http.StatusBadRequest, "overwrite_invalid_json")
		}
	}
	if len(result) > overwrite.MaxBodyBytes {
		return fail(http.StatusRequestEntityTooLarge, "overwrite_body_too_large")
	}
	_ = r.Body.Close()
	prepared := new(http.Request)
	*prepared = *r
	prepared.Body = &overwriteBody{reader: bytes.NewReader(result), release: release}
	prepared.ContentLength = int64(len(result))
	prepared.TransferEncoding = nil
	prepared.Trailer = nil
	prepared.GetBody = nil // Do not introduce request replay/retry semantics.
	st.overwriteBuffered = true
	st.overwriteChanged = len(result) > 0 && (len(result) != len(body) || &result[0] != &body[0])
	success = true
	return prepared, release, true
}

type overwriteBody struct {
	mu      sync.Mutex
	reader  *bytes.Reader
	release func()
}

func (b *overwriteBody) Read(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.reader == nil {
		return 0, io.ErrClosedPipe
	}
	return b.reader.Read(p)
}

func (b *overwriteBody) Close() error {
	b.mu.Lock()
	b.reader = nil // Long SSE responses must not retain the uploaded JSON.
	b.mu.Unlock()
	b.release()
	return nil
}
