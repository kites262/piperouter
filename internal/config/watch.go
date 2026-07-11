package config

import (
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// debounceInterval is how long the Watcher waits after the last relevant
// filesystem event before invoking onChange, coalescing editor save bursts
// and temp-file/rename sequences into a single notification.
const debounceInterval = 200 * time.Millisecond

// relevantOps are the operations that can indicate the config file changed,
// including editor patterns like remove+create and atomic rename-over.
const relevantOps = fsnotify.Create | fsnotify.Write | fsnotify.Rename | fsnotify.Remove

// Watcher watches a configuration file for external modification (PRD §6.6).
//
// It watches the parent directory rather than the file itself so that atomic
// replaces (write temp file + rename) and editor remove/re-create cycles keep
// being observed; events are filtered by the config file's base name.
type Watcher struct {
	base     string
	logger   *slog.Logger
	onChange func()
	debounce time.Duration

	fw        *fsnotify.Watcher
	wg        sync.WaitGroup
	closeOnce sync.Once
	closeErr  error
}

// NewWatcher starts watching the directory containing path and invokes
// onChange (from the watcher goroutine) after events for the config file
// settle. onChange must not call Close.
func NewWatcher(path string, logger *slog.Logger, onChange func()) (*Watcher, error) {
	if onChange == nil {
		return nil, fmt.Errorf("config watcher: onChange must not be nil")
	}
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("config watcher: resolve path: %w", err)
	}
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("config watcher: %w", err)
	}
	dir := filepath.Dir(abs)
	if err := fw.Add(dir); err != nil {
		_ = fw.Close()
		return nil, fmt.Errorf("config watcher: watch directory %q: %w", dir, err)
	}
	w := &Watcher{
		base:     filepath.Base(abs),
		logger:   logger,
		onChange: onChange,
		debounce: debounceInterval,
		fw:       fw,
	}
	w.wg.Add(1)
	go w.run()
	return w, nil
}

func (w *Watcher) run() {
	defer w.wg.Done()
	var timer *time.Timer
	var fire <-chan time.Time
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()
	for {
		select {
		case ev, ok := <-w.fw.Events:
			if !ok {
				return
			}
			if filepath.Base(ev.Name) != w.base || ev.Op&relevantOps == 0 {
				continue
			}
			// Never log paths beyond the op: file content is not touched here.
			w.logger.Debug("config file event", "op", ev.Op.String())
			if timer == nil {
				timer = time.NewTimer(w.debounce)
				fire = timer.C
			} else {
				timer.Stop()
				timer.Reset(w.debounce)
			}
		case <-fire:
			timer = nil
			fire = nil
			w.onChange()
		case err, ok := <-w.fw.Errors:
			if !ok {
				return
			}
			w.logger.Warn("config watcher error", "error", err)
		}
	}
}

// Close stops the watcher and waits for its goroutine to exit. It is
// idempotent and safe for concurrent use.
func (w *Watcher) Close() error {
	w.closeOnce.Do(func() {
		w.closeErr = w.fw.Close()
		w.wg.Wait()
	})
	return w.closeErr
}
