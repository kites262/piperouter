// Benchmarks and the scaled-down concurrency target from PRD §22.1.
//
// Reference numbers from this machine (darwin/arm64, Apple M1, loopback
// upstreams, `go test ./test/integration -bench . -benchtime 2s -run '^$'`,
// race detector OFF — see comments on each benchmark for the exact
// figures). The PRD target "added p99 < 5ms for local upstreams" is
// reported, not asserted: benchmarks document, tests enforce.
package integration_test

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// benchGet performs one GET and fully drains the body.
func benchGet(b *testing.B, client *http.Client, url string, wantLen int64) {
	b.Helper()
	resp, err := client.Get(url)
	if err != nil {
		b.Fatalf("GET %s: %v", url, err)
	}
	n, err := io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if err != nil || resp.StatusCode != http.StatusOK {
		b.Fatalf("GET %s: status=%d err=%v", url, resp.StatusCode, err)
	}
	if n != wantLen {
		b.Fatalf("GET %s: got %d bytes, want %d", url, n, wantLen)
	}
}

// BenchmarkProxyOverhead compares a 1 KiB GET straight to the upstream
// against the same GET through the running proxy (PRD §22.1: added p99
// < 5ms for local upstreams — report only, no assertion).
//
// Reference run (Apple M1, -benchtime 2s, keep-alive, no -race):
//
//	direct-8    63640 iters   35,341 ns/req   (~35µs,  5,110 B/op,  62 allocs/op)
//	proxied-8   29218 iters   82,302 ns/req   (~82µs, 46,647 B/op, 164 allocs/op)
//	→ mean added latency ≈ 47µs, roughly 100x under the 5ms p99 target.
func BenchmarkProxyOverhead(b *testing.B) {
	payload := bytes.Repeat([]byte("x"), 1024)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(payload) //nolint:errcheck
	}))
	b.Cleanup(up.Close)
	ta := startApp(b, baseConfig(route("bench", "/bench", up.URL)))
	client := newClient(b)

	// Warm both paths: TCP + pooled upstream connections established.
	benchGet(b, client, up.URL+"/payload", int64(len(payload)))
	benchGet(b, client, ta.proxyURL()+"/bench/payload", int64(len(payload)))

	run := func(url string) func(b *testing.B) {
		return func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				benchGet(b, client, url, int64(len(payload)))
			}
			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N), "ns/req")
		}
	}
	b.Run("direct", run(up.URL+"/payload"))
	b.Run("proxied", run(ta.proxyURL()+"/bench/payload"))
}

// BenchmarkThroughput1MB streams 1 MiB responses through the proxy and
// reports MB/s (PRD §22.1: response size must not drive proxy memory;
// throughput is bounded by loopback + copy cost only).
//
// Reference run (Apple M1, -benchtime 2s, no -race): 2,392 MB/s
// (5743 iters, 417,977 ns/op ≈ 0.42ms per 1 MiB response).
func BenchmarkThroughput1MB(b *testing.B) {
	const size int64 = 1 << 20
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var sent int64
		for sent < size {
			chunk := patternChunk
			if rest := size - sent; rest < int64(len(chunk)) {
				chunk = chunk[:rest]
			}
			n, err := w.Write(chunk)
			sent += int64(n)
			if err != nil {
				return
			}
		}
	}))
	b.Cleanup(up.Close)
	ta := startApp(b, baseConfig(route("mb", "/mb", up.URL)))
	client := newClient(b)
	benchGet(b, client, ta.proxyURL()+"/mb/warm", size) // warm-up

	b.SetBytes(size)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchGet(b, client, ta.proxyURL()+"/mb/stream", size)
	}
	b.StopTimer()
	if secs := b.Elapsed().Seconds(); secs > 0 {
		b.ReportMetric(float64(b.N)*float64(size)/(1<<20)/secs, "MB/s")
	}
}

// TestConcurrentSSEStreams is the scaled-down §22.1 concurrency target
// (1000 SSE streams in production, 200 here to keep the suite fast): 200
// concurrent SSE streams held open ~2s must all deliver every event, and
// after full teardown the goroutine count must return near the baseline
// (no per-stream goroutine leaks).
func TestConcurrentSSEStreams(t *testing.T) {
	const (
		streams         = 200
		eventsPerStream = 8                      // paced below → stream lives ~1.75s
		interval        = 250 * time.Millisecond // 7 gaps ≈ 1.75s held open
	)

	baseline := runtime.NumGoroutine()

	up, _ := sseUpstream(t, interval, eventsPerStream)
	ta := startApp(t, baseConfig(route("sse", "/sse", up.URL)))
	tr := &http.Transport{MaxIdleConnsPerHost: streams}
	client := &http.Client{Transport: tr}

	counts := make([]int64, streams) // one slot per goroutine, read after Wait
	errs := make(chan error, streams)
	var wg sync.WaitGroup
	for i := 0; i < streams; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			resp, err := client.Get(ta.proxyURL() + "/sse/stream")
			if err != nil {
				errs <- fmt.Errorf("stream %d: %w", idx, err)
				return
			}
			defer resp.Body.Close()
			sc := bufio.NewScanner(resp.Body)
			for sc.Scan() {
				if strings.HasPrefix(sc.Text(), "data: ") {
					counts[idx]++
				}
			}
			if err := sc.Err(); err != nil {
				errs <- fmt.Errorf("stream %d read: %w", idx, err)
			}
		}(i)
	}
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	waitClosed(t, done, 30*time.Second, "not all SSE streams finished")
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	short := 0
	for i, n := range counts {
		if n != eventsPerStream {
			short++
			if short <= 5 { // don't spam 200 failures
				t.Errorf("stream %d delivered %d events, want %d", i, n, eventsPerStream)
			}
		}
	}
	if short > 5 {
		t.Errorf("... and %d more incomplete streams", short-5)
	}

	// Full teardown, then the goroutine count must decay to ~baseline:
	// leaked per-stream goroutines (200+) would dwarf the +15 slack.
	shutdownApp(t, ta)
	up.Close()
	tr.CloseIdleConnections()
	eventually(t, 10*time.Second, "goroutine count did not return near baseline (leak)", func() bool {
		return runtime.NumGoroutine() <= baseline+15
	})
	t.Logf("goroutines: baseline=%d final=%d (slack 15)", baseline, runtime.NumGoroutine())
}
