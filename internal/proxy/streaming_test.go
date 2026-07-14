package proxy

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestSSEStreamsIncrementally proves events reach the client one by one
// (PRD §10.3, §23.5): the client must receive event 1 BEFORE the upstream
// sends event 3, the SSE gauge must be 1 during the stream, and the access
// entry must be marked streaming=sse.
func TestSSEStreamsIncrementally(t *testing.T) {
	proceed := make(chan struct{})   // closed by the test after event 1 arrives
	sentThird := make(chan struct{}) // closed by the upstream after event 3
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		rc := http.NewResponseController(w)
		io.WriteString(w, "data: one\n\n")
		rc.Flush()
		select {
		case <-proceed:
		case <-r.Context().Done():
			return
		}
		io.WriteString(w, "data: two\n\n")
		rc.Flush()
		io.WriteString(w, "data: three\n\n")
		rc.Flush()
		close(sentThird)
	}))
	t.Cleanup(upstream.Close)

	snap := buildSnapshot(t, fmt.Sprintf(`
version: v0.3
routes:
  - name: api
    prefix: /api
    options:
      target: %s
`, upstream.URL))
	tp := newTestProxy(t, snap)

	resp, err := http.Get(tp.server.URL + "/api/sse")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type = %q", ct)
	}

	// Read lines on a goroutine so every expectation carries a deadline:
	// a buffering proxy would make the first read hang until the response
	// ends, failing the timeout below. Lines and the terminal error travel
	// on ONE channel in read order: with separate channels both are ready
	// once the whole tail has been drained, and select would pick between
	// them at random, misreporting buffered lines as an early EOF.
	type streamEvent struct {
		line string
		err  error
	}
	events := make(chan streamEvent, 16)
	go func() {
		br := bufio.NewReader(resp.Body)
		for {
			line, err := br.ReadString('\n')
			if line != "" {
				events <- streamEvent{line: line}
			}
			if err != nil {
				events <- streamEvent{err: err}
				return
			}
		}
	}()
	readLine := func(want string) {
		t.Helper()
		select {
		case ev := <-events:
			if ev.err != nil {
				t.Fatalf("stream ended early: %v", ev.err)
			}
			if ev.line != want {
				t.Fatalf("line = %q, want %q", ev.line, want)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for line %q (response is being buffered)", want)
		}
	}

	readLine("data: one\n")
	readLine("\n")
	// Event 1 arrived while the upstream is still gated before event 2/3:
	// no aggregation happened.
	select {
	case <-sentThird:
		t.Fatal("upstream already sent event 3 before the client read event 1")
	default:
	}
	// SSE detected via Content-Type → gauge is 1 during the stream.
	if got := tp.reg.Snapshot().ActiveSSE; got != 1 {
		t.Fatalf("ActiveSSE during stream = %d, want 1", got)
	}

	close(proceed)
	readLine("data: two\n")
	readLine("\n")
	readLine("data: three\n")
	readLine("\n")
	select {
	case ev := <-events:
		if ev.err != io.EOF {
			t.Fatalf("stream event = %+v, want EOF", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("stream did not end")
	}

	waitFor(t, 2*time.Second, func() bool {
		s := tp.reg.Snapshot()
		return s.ActiveSSE == 0 && s.ActiveRequests == 0
	}, "SSE and active gauges back to 0")

	e := lastEntry(t, tp.ring, 1)
	if e.Streaming != "sse" || e.Status != 200 || e.Route != "api" || e.Error != "" {
		t.Fatalf("access entry = %+v", e)
	}
}

// TestRequestBodyStreams proves the upstream starts reading the request
// body before the client finished uploading (PRD §10.1, §23.4).
func TestRequestBodyStreams(t *testing.T) {
	gotFirst := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 6)
		if _, err := io.ReadFull(r.Body, buf); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if string(buf) != "chunk1" {
			http.Error(w, "unexpected first chunk", http.StatusBadRequest)
			return
		}
		close(gotFirst)
		rest, _ := io.ReadAll(r.Body)
		fmt.Fprintf(w, "got %d bytes total", len(buf)+len(rest))
	}))
	t.Cleanup(upstream.Close)

	snap := buildSnapshot(t, fmt.Sprintf(`
version: v0.3
routes:
  - name: api
    prefix: /api
    options:
      target: %s
`, upstream.URL))
	tp := newTestProxy(t, snap)

	pr, pw := io.Pipe()
	req, err := http.NewRequest(http.MethodPost, tp.server.URL+"/api/upload", pr)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	respCh := make(chan *http.Response, 1)
	errCh := make(chan error, 1)
	go func() {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			errCh <- err
			return
		}
		respCh <- resp
	}()

	if _, err := pw.Write([]byte("chunk1")); err != nil {
		t.Fatalf("pipe write: %v", err)
	}
	// The pipe is still open: the only way the upstream can have the first
	// chunk is streaming forwarding.
	select {
	case <-gotFirst:
	case err := <-errCh:
		t.Fatalf("request failed early: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("upstream did not receive the first chunk before the upload finished")
	}

	if _, err := pw.Write([]byte("-and-the-rest")); err != nil {
		t.Fatalf("pipe write: %v", err)
	}
	pw.Close()

	select {
	case resp := <-respCh:
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK || string(body) != "got 19 bytes total" {
			t.Fatalf("status = %d, body = %q", resp.StatusCode, body)
		}
	case err := <-errCh:
		t.Fatalf("request failed: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("no response")
	}
}
