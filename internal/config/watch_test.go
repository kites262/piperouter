package config

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// waitFor polls cond until it returns true or timeout elapses.
func waitFor(timeout time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return cond()
}

func TestWatcherAtomicReplaceAndUnrelatedFiles(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "piperouter.yaml")
	mustWriteFile(t, cfgPath, "version: 1\n")

	var count atomic.Int32
	w, err := NewWatcher(cfgPath, discardLogger(), func() { count.Add(1) })
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	defer w.Close()

	// Unrelated files in the same directory must never fire onChange.
	mustWriteFile(t, filepath.Join(dir, "unrelated.txt"), "x")
	mustWriteFile(t, filepath.Join(dir, "unrelated.txt"), "xy")
	if err := os.Rename(filepath.Join(dir, "unrelated.txt"), filepath.Join(dir, "unrelated2.txt")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(3 * debounceInterval)
	if got := count.Load(); got != 0 {
		t.Fatalf("onChange fired %d times for unrelated files", got)
	}

	// Atomic replace: write a temp file next to the config, then rename it
	// over the config file (the WriteAtomic pattern and editor save pattern).
	tmp := filepath.Join(dir, "piperouter.yaml.tmp-1")
	mustWriteFile(t, tmp, "version: 1\nruntime:\n  log_level: debug\n")
	if err := os.Rename(tmp, cfgPath); err != nil {
		t.Fatal(err)
	}
	if !waitFor(5*time.Second, func() bool { return count.Load() >= 1 }) {
		t.Fatal("onChange did not fire after atomic replace")
	}
}

func TestWatcherFiresOnDirectWriteAndRecreate(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "piperouter.yaml")
	mustWriteFile(t, cfgPath, "version: 1\n")

	var count atomic.Int32
	w, err := NewWatcher(cfgPath, discardLogger(), func() { count.Add(1) })
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	defer w.Close()

	// In-place write.
	mustWriteFile(t, cfgPath, "version: 1\nruntime:\n  log_level: warn\n")
	if !waitFor(5*time.Second, func() bool { return count.Load() >= 1 }) {
		t.Fatal("onChange did not fire after direct write")
	}

	// Editor pattern: remove then re-create.
	before := count.Load()
	if err := os.Remove(cfgPath); err != nil {
		t.Fatal(err)
	}
	mustWriteFile(t, cfgPath, "version: 1\nruntime:\n  log_level: error\n")
	if !waitFor(5*time.Second, func() bool { return count.Load() > before }) {
		t.Fatal("onChange did not fire after remove+recreate")
	}
}

func TestWatcherFileNotYetExisting(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "piperouter.yaml")

	var count atomic.Int32
	w, err := NewWatcher(cfgPath, discardLogger(), func() { count.Add(1) })
	if err != nil {
		t.Fatalf("NewWatcher on missing file: %v", err)
	}
	defer w.Close()

	mustWriteFile(t, cfgPath, "version: 1\n")
	if !waitFor(5*time.Second, func() bool { return count.Load() >= 1 }) {
		t.Fatal("onChange did not fire when the file was created")
	}
}

func TestWatcherDebounceCoalescesBursts(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "piperouter.yaml")
	mustWriteFile(t, cfgPath, "version: 1\n")

	var count atomic.Int32
	w, err := NewWatcher(cfgPath, discardLogger(), func() { count.Add(1) })
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	defer w.Close()

	// A rapid burst of writes should produce far fewer callbacks than writes.
	const writes = 5
	for i := 0; i < writes; i++ {
		mustWriteFile(t, cfgPath, "version: 1\n# burst\n")
		time.Sleep(20 * time.Millisecond)
	}
	if !waitFor(5*time.Second, func() bool { return count.Load() >= 1 }) {
		t.Fatal("onChange never fired after burst")
	}
	time.Sleep(3 * debounceInterval)
	if got := count.Load(); got >= writes {
		t.Errorf("debounce ineffective: %d callbacks for %d writes", got, writes)
	}
}

func TestWatcherErrors(t *testing.T) {
	missingDir := filepath.Join(t.TempDir(), "does-not-exist", "cfg.yaml")
	if _, err := NewWatcher(missingDir, discardLogger(), func() {}); err == nil {
		t.Error("NewWatcher succeeded for a missing directory")
	}
	if _, err := NewWatcher(filepath.Join(t.TempDir(), "cfg.yaml"), discardLogger(), nil); err == nil {
		t.Error("NewWatcher succeeded with nil onChange")
	}
}

func TestWatcherCloseIdempotent(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "piperouter.yaml")
	mustWriteFile(t, cfgPath, "version: 1\n")

	var count atomic.Int32
	w, err := NewWatcher(cfgPath, discardLogger(), func() { count.Add(1) })
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Errorf("Close (first) = %v", err)
	}
	if err := w.Close(); err != nil {
		t.Errorf("Close (second) = %v", err)
	}

	// After Close, changes must not fire.
	closed := count.Load()
	mustWriteFile(t, cfgPath, "version: 1\nruntime:\n  log_level: debug\n")
	time.Sleep(3 * debounceInterval)
	if got := count.Load(); got != closed {
		t.Errorf("onChange fired after Close: %d -> %d", closed, got)
	}
}
