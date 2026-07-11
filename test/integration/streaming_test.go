// Streaming acceptance cases (PRD §23.4 big bodies, §23.5 SSE, §23.6
// WebSocket) through the fully assembled application.
package integration_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strconv"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/websocket"
)

const (
	// bigBodySize is transferred once in each direction (§23.4).
	bigBodySize int64 = 256 << 20 // 256 MiB

	// heapDeltaLimit bounds the live-heap growth across both transfers.
	//
	// Threshold rationale: the data plane copies with fixed-size buffers
	// (32 KiB io.Copy / ReverseProxy chunks), so the live heap after a GC
	// is O(1) in body size — measured deltas on this suite are single-digit
	// MiB. Any code path that buffered a full body would keep ≥256 MiB
	// live and overshoot this limit by 4x or more, while 64 MiB still
	// leaves generous headroom above allocator/GC noise (§22.1: memory
	// must not scale with body size).
	heapDeltaLimit int64 = 64 << 20 // 64 MiB
)

// patternChunk is the 64 KiB repeating payload both directions reuse; a
// single shared chunk keeps the generators allocation-free.
var patternChunk = func() []byte {
	b := make([]byte, 64<<10)
	for i := range b {
		b[i] = byte('a' + i%26)
	}
	return b
}()

// patternReader yields exactly `remaining` bytes without ever holding more
// than the caller's buffer. It records when the transport drained the last
// byte (a conservative lower bound for "client finished sending").
type patternReader struct {
	remaining int64

	mu     sync.Mutex
	doneAt time.Time
}

func (p *patternReader) Read(b []byte) (int, error) {
	if p.remaining <= 0 {
		return 0, io.EOF
	}
	n := len(b)
	if int64(n) > p.remaining {
		n = int(p.remaining)
	}
	for filled := 0; filled < n; {
		filled += copy(b[filled:n], patternChunk)
	}
	p.remaining -= int64(n)
	if p.remaining == 0 {
		p.mu.Lock()
		p.doneAt = time.Now()
		p.mu.Unlock()
	}
	return n, nil
}

func (p *patternReader) doneTime() time.Time {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.doneAt
}

// firstByteRecorder stamps the time of the first successful read — used by
// the upstream to prove it started receiving before the client finished
// sending (§23.4: no full request buffering in the proxy).
type firstByteRecorder struct {
	r    io.Reader
	ch   chan<- time.Time
	seen bool
}

func (f *firstByteRecorder) Read(p []byte) (int, error) {
	n, err := f.r.Read(p)
	if n > 0 && !f.seen {
		f.seen = true
		select {
		case f.ch <- time.Now():
		default:
		}
	}
	return n, err
}

// TestBigBodyStreaming covers PRD §23.4 in both directions with 256 MiB
// bodies: byte counts match, the upstream sees the first request bytes
// before the client finishes sending, and the proxy's live heap stays flat
// (streaming, not buffering). Skipped under `go test -short`.
func TestBigBodyStreaming(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping 2x256MiB body streaming test in -short mode")
	}

	firstByteAt := make(chan time.Time, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /upload", func(w http.ResponseWriter, r *http.Request) {
		n, err := io.Copy(io.Discard, &firstByteRecorder{r: r.Body, ch: firstByteAt})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		fmt.Fprintf(w, "%d", n)
	})
	mux.HandleFunc("GET /download", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.FormatInt(bigBodySize, 10))
		var sent int64
		for sent < bigBodySize {
			chunk := patternChunk
			if rest := bigBodySize - sent; rest < int64(len(chunk)) {
				chunk = chunk[:rest]
			}
			n, err := w.Write(chunk)
			sent += int64(n)
			if err != nil {
				return
			}
		}
	})
	up := httptest.NewServer(mux)
	t.Cleanup(up.Close)
	ta := startApp(t, baseConfig(route("big", "/big", up.URL)))
	client := newClient(t)

	// Baseline live heap. Both readings happen after a forced GC so the
	// delta reflects retained memory, not transient garbage.
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	// --- request direction: 256 MiB from a plain io.Reader ---------------
	body := &patternReader{remaining: bigBodySize}
	req, err := http.NewRequest(http.MethodPost, ta.proxyURL()+"/big/upload", body)
	if err != nil {
		t.Fatalf("build upload request: %v", err)
	}
	req.ContentLength = bigBodySize // identity framing, still fully streamed
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("upload through proxy: %v", err)
	}
	got, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("upload response: status=%d err=%v body=%q", resp.StatusCode, err, got)
	}
	if want := strconv.FormatInt(bigBodySize, 10); string(got) != want {
		t.Errorf("upstream counted %q request bytes, want %s", got, want)
	}
	clientDone := body.doneTime()
	select {
	case fb := <-firstByteAt:
		if !fb.Before(clientDone) {
			t.Errorf("upstream first byte at %v, client finished sending at %v — upstream must start reading before the upload completes (§23.4)", fb, clientDone)
		} else {
			t.Logf("upstream started reading %v before the client finished sending", clientDone.Sub(fb))
		}
	default:
		t.Error("upstream never reported a first request byte")
	}

	// --- response direction: 256 MiB generated stream --------------------
	dresp, err := client.Get(ta.proxyURL() + "/big/download")
	if err != nil {
		t.Fatalf("download through proxy: %v", err)
	}
	n, err := io.Copy(io.Discard, dresp.Body)
	dresp.Body.Close()
	if err != nil || dresp.StatusCode != http.StatusOK {
		t.Fatalf("download response: status=%d err=%v", dresp.StatusCode, err)
	}
	if n != bigBodySize {
		t.Errorf("client received %d response bytes, want %d", n, bigBodySize)
	}

	// --- memory: live heap must not scale with body size -----------------
	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	delta := int64(after.HeapAlloc) - int64(before.HeapAlloc)
	t.Logf("HeapAlloc before=%.1fMiB after=%.1fMiB delta=%.1fMiB (limit %dMiB)",
		float64(before.HeapAlloc)/(1<<20), float64(after.HeapAlloc)/(1<<20),
		float64(delta)/(1<<20), heapDeltaLimit>>20)
	if delta > heapDeltaLimit {
		t.Errorf("HeapAlloc grew by %d bytes (> %d) across 2x%d streamed bytes — the proxy is buffering bodies", delta, heapDeltaLimit, bigBodySize)
	}
}

// TestSSEPacing covers PRD §23.5: events emitted every 150ms arrive one by
// one (first event well before the stream ends, arrivals spread over time)
// instead of being delivered in one burst at response end.
func TestSSEPacing(t *testing.T) {
	const (
		interval = 150 * time.Millisecond
		count    = 5
	)
	up, _ := sseUpstream(t, interval, count)
	ta := startApp(t, baseConfig(route("sse", "/sse", up.URL)))
	client := newClient(t)

	start := time.Now()
	events, errc, err := openSSE(t.Context(), client, ta.proxyURL()+"/sse/stream")
	if err != nil {
		t.Fatalf("open SSE stream: %v", err)
	}
	var arrivals []time.Time
	for i := 0; i < count; i++ {
		ev := recvEvent(t, events, 3*time.Second, fmt.Sprintf("event %d", i))
		if want := fmt.Sprintf("event-%d", i); ev.data != want {
			t.Errorf("event %d = %q, want %q", i, ev.data, want)
		}
		arrivals = append(arrivals, ev.at)
	}

	// First event must arrive immediately (< 400ms), i.e. long before the
	// upstream finishes the ~600ms stream — proves per-event flushing.
	if d := arrivals[0].Sub(start); d >= 400*time.Millisecond {
		t.Errorf("first event arrived after %v, want < 400ms (stream must not be buffered)", d)
	}
	// Arrivals must be spread out: the upstream spaces the 5 events over
	// 4x150ms = 600ms; buffered delivery would collapse the span to ~0.
	if span := arrivals[count-1].Sub(arrivals[0]); span < 300*time.Millisecond {
		t.Errorf("events arrived within %v, want >= 300ms spread (inter-arrival gaps not preserved)", span)
	}

	// Clean end of stream.
	select {
	case _, ok := <-events:
		if ok {
			t.Error("received more events than the upstream sent")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("SSE stream did not close after the last event")
	}
	if err := <-errc; err != nil {
		t.Errorf("stream read error: %v", err)
	}
}

// TestSSEClientCancelPropagates covers PRD §23.5: canceling the client
// request cancels the upstream handler context within 2s.
func TestSSEClientCancelPropagates(t *testing.T) {
	up, canceled := sseUpstream(t, 100*time.Millisecond, -1) // endless stream
	ta := startApp(t, baseConfig(route("sse", "/sse", up.URL)))
	client := newClient(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, _, err := openSSE(ctx, client, ta.proxyURL()+"/sse/stream")
	if err != nil {
		t.Fatalf("open SSE stream: %v", err)
	}
	recvEvent(t, events, 3*time.Second, "first event before cancel")

	cancel()
	waitClosed(t, canceled, 2*time.Second, "upstream handler context was not canceled after the client went away (§23.5)")
}

// TestWebSocketThroughProxy covers PRD §23.6: upgrade through the proxy,
// three echo round trips, and close propagation in both directions.
func TestWebSocketThroughProxy(t *testing.T) {
	echoExited := make(chan struct{})
	var once sync.Once
	mux := http.NewServeMux()
	mux.Handle("/echo", websocket.Handler(func(ws *websocket.Conn) {
		io.Copy(ws, ws) //nolint:errcheck // echo until the peer closes
		once.Do(func() { close(echoExited) })
	}))
	mux.Handle("/oneshot", websocket.Handler(func(ws *websocket.Conn) {
		// Send one message, then return: x/net/websocket closes the
		// connection, which must propagate to the client.
		websocket.Message.Send(ws, "server-close") //nolint:errcheck
	}))
	up := httptest.NewServer(mux)
	t.Cleanup(up.Close)
	ta := startApp(t, baseConfig(route("ws", "/ws", up.URL)))

	origin := "http://" + ta.App.ProxyAddr() + "/"

	// Upgrade + 3 round trips.
	conn, err := websocket.Dial("ws://"+ta.App.ProxyAddr()+"/ws/echo", "", origin)
	if err != nil {
		t.Fatalf("websocket dial through proxy: %v", err)
	}
	for i := 0; i < 3; i++ {
		msg := fmt.Sprintf("ping-%d", i)
		if err := websocket.Message.Send(conn, msg); err != nil {
			t.Fatalf("round trip %d send: %v", i, err)
		}
		if err := conn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
			t.Fatalf("set read deadline: %v", err)
		}
		var got string
		if err := websocket.Message.Receive(conn, &got); err != nil {
			t.Fatalf("round trip %d receive: %v", i, err)
		}
		if got != msg {
			t.Errorf("round trip %d: got %q, want %q", i, got, msg)
		}
	}

	// Client-side close must reach the upstream handler.
	conn.Close()
	waitClosed(t, echoExited, 2*time.Second, "upstream echo handler did not observe the client close (§23.6)")

	// Upstream-side close must reach the client.
	conn2, err := websocket.Dial("ws://"+ta.App.ProxyAddr()+"/ws/oneshot", "", origin)
	if err != nil {
		t.Fatalf("websocket dial (oneshot): %v", err)
	}
	defer conn2.Close()
	if err := conn2.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	var first string
	if err := websocket.Message.Receive(conn2, &first); err != nil {
		t.Fatalf("oneshot receive: %v", err)
	}
	if first != "server-close" {
		t.Errorf("oneshot message = %q, want %q", first, "server-close")
	}
	var extra string
	err = websocket.Message.Receive(conn2, &extra)
	if err == nil {
		t.Fatalf("receive after upstream close succeeded with %q, want EOF (close not propagated)", extra)
	}
	if !errors.Is(err, io.EOF) {
		t.Logf("close surfaced as %v (any terminal error accepted, EOF preferred)", err)
	}
}
