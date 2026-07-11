// Hot-reload acceptance cases: PRD §23.9 (config change during a live SSE
// stream) and §23.10 (invalid config fallback + recovery), driven through
// the real config file, fsnotify watcher and admin status endpoint.
package integration_test

import (
	"context"
	"net/http"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kites262/piperouter/internal/config"
)

// TestHotReloadDuringSSE covers PRD §23.9: while an SSE stream is live,
// an external atomic config edit adds a route; the revision changes, the
// new route serves immediately, and the SSE stream keeps delivering
// events without interruption.
func TestHotReloadDuringSSE(t *testing.T) {
	sseUp, _ := sseUpstream(t, 100*time.Millisecond, -1) // endless paced stream
	lateUp := pathEcho(t, "late")
	ta := startApp(t, baseConfig(route("sse", "/sse", sseUp.URL)))
	client := newClient(t)

	// Live SSE stream; a goroutine counts delivered events.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, _, err := openSSE(ctx, client, ta.proxyURL()+"/sse/stream")
	if err != nil {
		t.Fatalf("open SSE stream: %v", err)
	}
	var delivered atomic.Int64
	streamClosed := make(chan struct{})
	go func() {
		defer close(streamClosed)
		for range events {
			delivered.Add(1)
		}
	}()
	eventually(t, 3*time.Second, "SSE stream not delivering before the reload", func() bool {
		return delivered.Load() >= 2
	})

	rev0 := adminStatus(t, client, ta).Config.Revision

	// The new route must be unknown before the reload.
	if resp, _ := get(t, client, ta.proxyURL()+"/late/ping"); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("pre-reload /late status = %d, want 404", resp.StatusCode)
	}

	// External edit (PRD §6.6): load → add route → atomic write.
	cfg, err := config.Load(ta.ConfigPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	cfg.Routes = append(cfg.Routes, route("late", "/late", lateUp.URL))
	writeConfig(t, ta.ConfigPath, cfg)

	// Watcher fires (200ms debounce) → revision changes.
	eventually(t, 5*time.Second, "config revision did not change after the file edit", func() bool {
		st := adminStatus(t, client, ta)
		return st.Config.Valid && st.Config.Revision != rev0
	})

	// New requests use the new configuration immediately.
	resp, body := get(t, client, ta.proxyURL()+"/late/ping")
	if resp.StatusCode != http.StatusOK || body != "late:/ping" {
		t.Errorf("post-reload /late: status=%d body=%q, want 200 %q", resp.StatusCode, body, "late:/ping")
	}

	// The pre-existing SSE stream is untouched: still open, still
	// delivering fresh events after the swap (PRD §12.2).
	select {
	case <-streamClosed:
		t.Fatal("SSE stream was closed by the config reload (§23.9: existing streams must survive)")
	default:
	}
	base := delivered.Load()
	eventually(t, 3*time.Second, "SSE stream stopped delivering events after the reload", func() bool {
		return delivered.Load() >= base+2
	})

	cancel()
	waitClosed(t, streamClosed, 3*time.Second, "SSE reader did not finish after cancel")
}

// TestInvalidConfigFallback covers PRD §23.10: a syntactically broken
// config file must not kill or degrade the process — status reports
// valid:false with a reason, old routes keep serving on the old revision,
// and a valid rewrite recovers.
func TestInvalidConfigFallback(t *testing.T) {
	up := pathEcho(t, "keep")
	extraUp := pathEcho(t, "extra")
	ta := startApp(t, baseConfig(route("keep", "/keep", up.URL)))
	client := newClient(t)

	if resp, _ := get(t, client, ta.proxyURL()+"/keep/a"); resp.StatusCode != http.StatusOK {
		t.Fatalf("baseline /keep status = %d, want 200", resp.StatusCode)
	}
	st0 := adminStatus(t, client, ta)
	if !st0.Config.Valid {
		t.Fatalf("baseline status reports valid=false: %q", st0.Config.LastError)
	}
	rev0 := st0.Config.Revision

	// Break the file with plain YAML garbage (non-atomic external write).
	if err := os.WriteFile(ta.ConfigPath, []byte("version: [1\nroutes: {{{ not yaml\n"), 0o644); err != nil {
		t.Fatalf("write broken config: %v", err)
	}
	eventually(t, 5*time.Second, "status never reported the invalid config", func() bool {
		st := adminStatus(t, client, ta)
		return !st.Config.Valid && st.Config.LastError != ""
	})

	// Old snapshot keeps serving on the old revision (PRD §12, §23.10).
	resp, body := get(t, client, ta.proxyURL()+"/keep/a")
	if resp.StatusCode != http.StatusOK || body != "keep:/a" {
		t.Errorf("/keep during invalid config: status=%d body=%q, want 200 %q", resp.StatusCode, body, "keep:/a")
	}
	if st := adminStatus(t, client, ta); st.Config.Revision != rev0 {
		t.Errorf("revision changed to %q while the file was invalid, want %q", st.Config.Revision, rev0)
	}

	// Restore a valid config (with one extra route so the revision must
	// move) → the manager recovers without a restart.
	restored := baseConfig(
		route("keep", "/keep", up.URL),
		route("extra", "/extra", extraUp.URL),
	)
	writeConfig(t, ta.ConfigPath, restored)
	eventually(t, 5*time.Second, "status did not recover after the valid rewrite", func() bool {
		st := adminStatus(t, client, ta)
		return st.Config.Valid && st.Config.LastError == "" && st.Config.Revision != rev0
	})

	resp, body = get(t, client, ta.proxyURL()+"/extra/b")
	if resp.StatusCode != http.StatusOK || body != "extra:/b" {
		t.Errorf("/extra after recovery: status=%d body=%q, want 200 %q", resp.StatusCode, body, "extra:/b")
	}
	if resp, _ := get(t, client, ta.proxyURL()+"/keep/a"); resp.StatusCode != http.StatusOK {
		t.Errorf("/keep after recovery: status = %d, want 200", resp.StatusCode)
	}
}
