package proxy

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kites262/piperouter/internal/logging"
	"github.com/kites262/piperouter/internal/metrics"
	"github.com/kites262/piperouter/internal/overwrite"
)

func newOverwriteHandler(t *testing.T, target, replacement string, details ...*logging.DetailsStore) *handler {
	t.Helper()
	snap := buildSnapshot(t, fmt.Sprintf(`
version: v0.3
overwrite:
  enabled: true
  rules:
    - name: model
      enabled: true
      field: model
      from: a
      to: b
routes:
  - name: api
    prefix: /api
    options:
      target: %s
      overwrites: [model]
`, target))
	t.Cleanup(snap.Pool.CloseIdleConnections)
	snap.Config.Overwrite.Rules[0].Action.Value = replacement
	for _, route := range snap.Table.Routes() {
		route.Overwrite = overwrite.Compile(snap.Config.Overwrite, []string{"model"})
	}
	reg := metrics.NewRegistry()
	reg.SetRoutes([]string{"api"})
	return NewHandler(&staticProvider{snap}, reg, logging.NewRing(128), nil, details...).(*handler)
}

func TestOverwriteRequestErrorsDoNotContactUpstream(t *testing.T) {
	var calls atomic.Int64
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		io.Copy(w, r.Body) //nolint:errcheck
	}))
	t.Cleanup(up.Close)
	h := newOverwriteHandler(t, up.URL, "b")
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	client := srv.Client()
	client.Timeout = 5 * time.Second

	for _, tc := range []struct {
		name, body, media, encoding, code string
		status                            int
		chunked                           bool
	}{
		{name: "invalid JSON", body: `{"model":"a",`, media: "application/json", status: 400, code: "overwrite_invalid_json"},
		{name: "non-object JSON", body: `["a"]`, media: "application/json", status: 400, code: "overwrite_invalid_json"},
		{name: "duplicate top-level key", body: `{"model":"a","\u006dodel":"a"}`, media: "application/json", status: 400, code: "overwrite_invalid_json"},
		{name: "non-JSON", body: "model=a", media: "text/plain", status: 415, code: "overwrite_unsupported_media_type"},
		{name: "compressed JSON", body: `{"model":"a"}`, media: "application/json", encoding: "gzip", status: 415, code: "overwrite_unsupported_media_type"},
		{name: "known-length limit", body: strings.Repeat(" ", overwrite.MaxBodyBytes+1), media: "application/json", status: 413, code: "overwrite_body_too_large"},
		{name: "unknown-length limit", body: strings.Repeat(" ", overwrite.MaxBodyBytes+1), media: "application/json", status: 413, code: "overwrite_body_too_large", chunked: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var body io.Reader = strings.NewReader(tc.body)
			if tc.chunked {
				// Hide strings.Reader's known size so net/http uses chunked transfer.
				body = struct{ io.Reader }{body}
			}
			req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, srv.URL+"/api/chat", body)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Content-Type", tc.media)
			if tc.encoding != "" {
				req.Header.Set("Content-Encoding", tc.encoding)
			}
			resp, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if got := decodeErrorBody(t, resp.Body); got != tc.code || resp.StatusCode != tc.status {
				t.Fatalf("status=%d code=%q, want %d %q", resp.StatusCode, got, tc.status, tc.code)
			}
			waitFor(t, time.Second, func() bool { return len(h.overwriteSlots) == 0 }, "error retained an Overwrite slot")
		})
	}
	if got := calls.Load(); got != 0 {
		t.Fatalf("upstream received %d invalid requests", got)
	}
}

func TestOverwriteExpandedOutputLimit(t *testing.T) {
	var calls atomic.Int64
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	t.Cleanup(up.Close)
	h := newOverwriteHandler(t, up.URL, strings.Repeat("b", overwrite.MaxBodyBytes))
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	resp, err := srv.Client().Post(srv.URL+"/api/chat", "application/json", strings.NewReader(`{"model":"a"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if got := decodeErrorBody(t, resp.Body); resp.StatusCode != http.StatusRequestEntityTooLarge || got != "overwrite_body_too_large" {
		t.Fatalf("status=%d code=%q, want 413 overwrite_body_too_large", resp.StatusCode, got)
	}
	if calls.Load() != 0 {
		t.Fatal("oversized replacement was forwarded upstream")
	}
	waitFor(t, time.Second, func() bool { return len(h.overwriteSlots) == 0 }, "output limit retained an Overwrite slot")
}

func TestOverwriteNoBodyWithDetailsCapture(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "models")
	}))
	t.Cleanup(up.Close)
	details := logging.NewDetailsStore(1<<20, 1<<20, 1<<16)
	details.SetEnabled(true)
	h := newOverwriteHandler(t, up.URL, "b", details)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	resp, err := srv.Client().Get(srv.URL + "/api/models")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	got, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != http.StatusOK || string(got) != "models" {
		t.Fatalf("status=%d body=%q err=%v, want untouched bodyless GET", resp.StatusCode, got, err)
	}
}

func TestOverwriteBodyConcurrentCloseReleasesStorageAndSlot(t *testing.T) {
	h := newOverwriteHandler(t, "http://127.0.0.1:1", "b")
	route := h.sp.Current().Table.Routes()[0]
	req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(`{"model":"a","messages":[]}`))
	req.Header.Set("Content-Type", "application/json")
	prepared, cleanup, ok := h.prepareOverwrite(newResponseRecorder(httptest.NewRecorder()), req, route, &requestState{})
	if !ok {
		t.Fatal("prepareOverwrite failed")
	}
	defer cleanup()
	body := prepared.Body.(*overwriteBody)
	if len(h.overwriteSlots) != 1 {
		t.Fatal("prepared body did not reserve a processing slot")
	}
	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Go(func() {
			buf := make([]byte, 2)
			for j := 0; j < 10; j++ {
				_, _ = body.Read(buf)
				_ = body.Close()
				cleanup()
			}
		})
	}
	wg.Wait()
	body.mu.Lock()
	hasReader := body.reader != nil
	body.mu.Unlock()
	if hasReader {
		t.Fatal("closed body still retains the uploaded JSON reader")
	}
	if got := len(h.overwriteSlots); got != 0 {
		t.Fatalf("concurrent closes retained %d slots", got)
	}
	if _, err := body.Read(make([]byte, 1)); err != io.ErrClosedPipe {
		t.Fatalf("Read after Close=%v, want io.ErrClosedPipe", err)
	}
}

// Start a real HTTP request and stop midway through its declared body. This
// exercises socket deadlines and semaphore capacity without test-only readers.
func slowOverwriteUpload(t *testing.T, addr string) net.Conn {
	t.Helper()
	return slowOverwriteUploadWithMedia(t, addr, "application/json")
}

func slowOverwriteUploadWithMedia(t *testing.T, addr, media string) net.Conn {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if _, err := fmt.Fprintf(conn, "POST /api/chat HTTP/1.1\r\nHost: %s\r\nContent-Type: %s\r\nContent-Length: 100\r\n\r\n{\"model\":", addr, media); err != nil {
		t.Fatal(err)
	}
	return conn
}

func TestOverwriteBusySlotsRejectQuicklyAndRecover(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(w, r.Body) //nolint:errcheck
	}))
	t.Cleanup(up.Close)
	h := newOverwriteHandler(t, up.URL, "b")
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	var conns []net.Conn
	defer func() {
		for _, conn := range conns {
			conn.Close()
		}
	}()
	for range maxConcurrentOverwrites {
		conns = append(conns, slowOverwriteUpload(t, srv.Listener.Addr().String()))
	}
	waitFor(t, time.Second, func() bool { return len(h.overwriteSlots) == maxConcurrentOverwrites }, "slow uploads did not fill processing slots")
	// The rejected ninth upload also stops mid-Body: a full-body client would
	// hide net/http's automatic draining before writing the error response.
	rejected := slowOverwriteUpload(t, srv.Listener.Addr().String())
	defer rejected.Close()
	if err := rejected.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(rejected), nil)
	if err != nil {
		t.Fatalf("busy request did not fail promptly: %v", err)
	}
	if code := decodeErrorBody(t, resp.Body); resp.StatusCode != http.StatusServiceUnavailable || code != "overwrite_busy" {
		t.Fatalf("status=%d code=%q, want 503 overwrite_busy", resp.StatusCode, code)
	}
	resp.Body.Close()
	for _, conn := range conns {
		conn.Close()
	}
	waitFor(t, time.Second, func() bool { return len(h.overwriteSlots) == 0 }, "canceled uploads retained processing slots")
	resp, err = srv.Client().Post(srv.URL+"/api/chat", "application/json", strings.NewReader(`{"model":"a"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	got, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != http.StatusOK || string(got) != `{"model":"b"}` {
		t.Fatalf("recovered request status=%d body=%q err=%v", resp.StatusCode, got, err)
	}
}

// Preserve ResponseController's unwrap chain but shorten the production read
// timeout so the test exercises the same TCP failure path in milliseconds.
type shortOverwriteDeadlineWriter struct{ http.ResponseWriter }

func (w shortOverwriteDeadlineWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w shortOverwriteDeadlineWriter) SetReadDeadline(deadline time.Time) error {
	if !deadline.IsZero() {
		deadline = time.Now().Add(20 * time.Millisecond)
	}
	return http.NewResponseController(w.ResponseWriter).SetReadDeadline(deadline)
}

func TestOverwriteSlowUploadTimeoutAndRecovery(t *testing.T) {
	var calls atomic.Int64
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		io.Copy(w, r.Body) //nolint:errcheck
	}))
	t.Cleanup(up.Close)
	h := newOverwriteHandler(t, up.URL, "b")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(shortOverwriteDeadlineWriter{w}, r)
	}))
	t.Cleanup(srv.Close)
	conn := slowOverwriteUpload(t, srv.Listener.Addr().String())
	defer conn.Close()
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("timeout response was not delivered: %v", err)
	}
	if code := decodeErrorBody(t, resp.Body); resp.StatusCode != http.StatusRequestTimeout || code != "overwrite_read_timeout" {
		t.Fatalf("status=%d code=%q, want 408 overwrite_read_timeout", resp.StatusCode, code)
	}
	resp.Body.Close()
	waitFor(t, time.Second, func() bool { return len(h.overwriteSlots) == 0 }, "read timeout retained processing slot")
	if calls.Load() != 0 {
		t.Fatal("timed-out upload reached upstream")
	}
	resp, err = srv.Client().Post(srv.URL+"/api/chat", "application/json", strings.NewReader(`{"model":"a"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	got, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != http.StatusOK || string(got) != `{"model":"b"}` {
		t.Fatalf("post-timeout request status=%d body=%q err=%v", resp.StatusCode, got, err)
	}
}

func TestOverwriteSSEDoesNotHoldProcessingSlots(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: first\n\n")
		if err := http.NewResponseController(w).Flush(); err != nil {
			return
		}
		<-r.Context().Done()
	}))
	t.Cleanup(up.Close)
	h := newOverwriteHandler(t, up.URL, "b")
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	var responses []*http.Response
	defer func() {
		for _, resp := range responses {
			resp.Body.Close()
		}
	}()
	// More live streams than Body-processing slots must remain possible.
	for range maxConcurrentOverwrites + 1 {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/api/chat", strings.NewReader(`{"model":"a","stream":true}`))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		responses = append(responses, resp)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("stream %d status=%d, want 200", len(responses), resp.StatusCode)
		}
		waitFor(t, time.Second, func() bool { return len(h.overwriteSlots) == 0 }, "open SSE stream retained a processing slot")
	}
}

func TestOverwriteUnsupportedSlowUploadRejectsQuickly(t *testing.T) {
	var calls atomic.Int64
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	t.Cleanup(up.Close)
	h := newOverwriteHandler(t, up.URL, "b")
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	conn := slowOverwriteUploadWithMedia(t, srv.Listener.Addr().String(), "text/plain")
	defer conn.Close()
	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("unsupported slow upload was not rejected promptly: %v", err)
	}
	defer resp.Body.Close()
	if code := decodeErrorBody(t, resp.Body); resp.StatusCode != http.StatusUnsupportedMediaType || code != "overwrite_unsupported_media_type" {
		t.Fatalf("status=%d code=%q, want 415 overwrite_unsupported_media_type", resp.StatusCode, code)
	}
	if calls.Load() != 0 || len(h.overwriteSlots) != 0 {
		t.Fatal("unsupported upload contacted upstream or occupied processing capacity")
	}
}
