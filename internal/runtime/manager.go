package runtime

import (
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kites262/piperouter/internal/config"
	"github.com/kites262/piperouter/internal/metrics"
	"github.com/kites262/piperouter/internal/router"
	"github.com/kites262/piperouter/internal/transport"
)

// Manager owns the active runtime Snapshot and serializes every
// configuration mutation (PRD §12). The data plane reads the snapshot
// lock-free through Current; Apply and ReloadFromFile are serialized by a
// single mutex so concurrent updates can never interleave (PRD §12.3).
//
// The configuration file remains the single source of truth (PRD §6.1):
// Apply persists via config.WriteAtomic before swapping, ReloadFromFile
// adopts external edits without writing back (PRD §6.5, §6.6).
type Manager struct {
	configPath string
	logger     *slog.Logger      // never nil (enforced by NewManager)
	reg        *metrics.Registry // may be nil: metrics updates are skipped

	current atomic.Pointer[Snapshot] // never nil after NewManager

	// mu serializes all mutations: Apply and ReloadFromFile. It is NOT
	// held by Current/Status/OnSwap, so OnSwap callbacks may safely read
	// the manager — but must never call Apply/ReloadFromFile themselves.
	mu sync.Mutex

	// statusMu guards valid/lastError only, so Status never contends
	// with (or deadlocks under) an in-progress mutation.
	statusMu  sync.Mutex
	valid     bool
	lastError string

	cbMu   sync.Mutex
	onSwap []func(*Snapshot)
}

// NewManager loads, validates and builds the initial snapshot from
// configPath. Any failure (missing file, parse error, validation error,
// build error) is returned so the process does not start with a broken
// configuration (PRD §4.2 startup semantics).
//
// logger must be non-nil. reg may be nil, in which case metric label
// updates (SetRoutes/SetTransportCount) are skipped.
func NewManager(configPath string, logger *slog.Logger, reg *metrics.Registry) (*Manager, error) {
	if logger == nil {
		return nil, errors.New("runtime: logger must not be nil")
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, err
	}
	// Parse already normalized; Normalize is idempotent and guards against
	// future callers handing in a non-normalized config.
	cfg.Normalize()
	if err := config.Validate(cfg); err != nil {
		return nil, err
	}
	snap, err := buildSnapshot(cfg)
	if err != nil {
		return nil, err
	}

	m := &Manager{configPath: configPath, logger: logger, reg: reg, valid: true}
	m.current.Store(snap)
	m.updateMetrics(snap)
	logger.Info("configuration loaded",
		"revision", snap.Revision,
		"routes", snap.Table.Len(),
		"transports", snap.Pool.Len(),
	)
	return m, nil
}

// Current returns the active snapshot. It is an atomic load: safe for
// concurrent use on the request hot path and never nil (PRD §12.1).
func (m *Manager) Current() *Snapshot { return m.current.Load() }

// ConfigPath returns the path of the configuration file this manager
// loads from and persists to.
func (m *Manager) ConfigPath() string { return m.configPath }

// Status reports the configuration state for the Admin API/WebUI
// (PRD §6.6, §19.9). Valid=false with LastError set after a failed
// ReloadFromFile; Revision and LoadedAt always describe the ACTIVE
// snapshot, also while Valid is false.
func (m *Manager) Status() Status {
	snap := m.current.Load()
	m.statusMu.Lock()
	defer m.statusMu.Unlock()
	return Status{
		Valid:     m.valid,
		LastError: m.lastError,
		Revision:  snap.Revision,
		LoadedAt:  snap.LoadedAt,
	}
}

// OnSwap registers fn to be called synchronously with the new snapshot
// after every successful swap (both Apply and ReloadFromFile). Callbacks
// run while the mutation lock is held: they must be fast and must not
// call Apply or ReloadFromFile.
func (m *Manager) OnSwap(fn func(*Snapshot)) {
	m.cbMu.Lock()
	defer m.cbMu.Unlock()
	m.onSwap = append(m.onSwap, fn)
}

// Apply validates, persists and activates newCfg following the PRD §6.5
// write flow: clone → normalize → validate → build table → build pool →
// atomic file write → snapshot swap. newCfg is never mutated.
//
// If expectedRevision is non-empty and does not match the active
// snapshot's revision, ErrRevisionMismatch is returned (PRD §12.3);
// an empty expectedRevision skips the check.
//
// A failure at any step leaves the configuration file untouched
// (config.WriteAtomic guarantees this) and the active snapshot unchanged;
// validation failures are returned as *config.ValidationError.
func (m *Manager) Apply(newCfg *config.Config, expectedRevision string) (*Snapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	old := m.current.Load()
	if expectedRevision != "" && expectedRevision != old.Revision {
		return nil, ErrRevisionMismatch
	}

	clone := newCfg.Clone()
	clone.Normalize()
	if err := config.Validate(clone); err != nil {
		return nil, err
	}
	next, err := buildSnapshot(clone)
	if err != nil {
		return nil, err
	}
	if err := config.WriteAtomic(m.configPath, clone); err != nil {
		next.Pool.CloseIdleConnections()
		return nil, fmt.Errorf("write configuration: %w", err)
	}

	m.swap(old, next)
	m.logger.Info("configuration applied",
		"old_revision", old.Revision,
		"new_revision", next.Revision,
		"routes", next.Table.Len(),
		"transports", next.Pool.Len(),
	)
	return next, nil
}

// Update atomically applies an in-place mutation to the active
// configuration under the mutation lock. Unlike Current()+Apply(), the
// read-modify-write is serialized end to end, so concurrent revision-less
// item mutations can never silently erase each other (PRD §12.3).
//
// mutate receives a deep clone of the active config and must mutate it in
// place; a non-nil error it returns is passed through unchanged (callers
// use typed errors to signal not-found/conflict). expectedRevision "" skips
// the conflict check. On any failure the file and active snapshot are left
// untouched (PRD §6.5).
func (m *Manager) Update(expectedRevision string, mutate func(*config.Config) error) (*Snapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	old := m.current.Load()
	if expectedRevision != "" && expectedRevision != old.Revision {
		return nil, ErrRevisionMismatch
	}

	clone := old.Config.Clone()
	if err := mutate(clone); err != nil {
		return nil, err
	}
	clone.Normalize()
	if err := config.Validate(clone); err != nil {
		return nil, err
	}
	next, err := buildSnapshot(clone)
	if err != nil {
		return nil, err
	}
	if err := config.WriteAtomic(m.configPath, clone); err != nil {
		next.Pool.CloseIdleConnections()
		return nil, fmt.Errorf("write configuration: %w", err)
	}

	m.swap(old, next)
	m.logger.Info("configuration updated",
		"old_revision", old.Revision,
		"new_revision", next.Revision,
		"routes", next.Table.Len(),
		"transports", next.Pool.Len(),
	)
	return next, nil
}

// ReloadFromFile adopts an external edit of the configuration file
// (PRD §6.6). The file is the source of truth, so nothing is written
// back. If the file content's revision equals the active revision the
// call is a no-op returning nil — this also suppresses the watcher echo
// of our own Apply writes.
//
// On any error the active snapshot keeps serving, Status turns
// {Valid:false, LastError:...}, the error is logged and returned; the
// process never exits over an invalid file.
func (m *Manager) ReloadFromFile() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	fail := func(err error) error {
		m.setStatus(false, err.Error())
		m.logger.Error("configuration reload failed",
			"path", m.configPath,
			"error", err.Error(),
		)
		return err
	}

	cfg, err := config.Load(m.configPath)
	if err != nil {
		return fail(err)
	}
	rev, err := config.Revision(cfg)
	if err != nil {
		return fail(err)
	}
	old := m.current.Load()
	if rev == old.Revision {
		// File matches the active snapshot (watcher echo of our own
		// write, or a manual revert): nothing to swap, config is valid.
		m.setStatus(true, "")
		return nil
	}
	if err := config.Validate(cfg); err != nil {
		return fail(err)
	}
	next, err := buildSnapshot(cfg)
	if err != nil {
		return fail(err)
	}

	m.swap(old, next)
	m.logger.Info("configuration reloaded",
		"old_revision", old.Revision,
		"new_revision", next.Revision,
		"routes", next.Table.Len(),
		"transports", next.Pool.Len(),
	)
	return nil
}

// swap activates next: store the pointer (new requests see it
// immediately), close only the old pool's idle connections (in-flight
// requests keep their snapshot and connections, PRD §12.2), update metric
// labels, notify OnSwap callbacks and mark the status valid.
func (m *Manager) swap(old, next *Snapshot) {
	m.current.Store(next)
	old.Pool.CloseIdleConnections()
	m.updateMetrics(next)
	m.notifySwap(next)
	m.setStatus(true, "")
}

// buildSnapshot compiles a normalized, validated config into an immutable
// snapshot: route table, transport pool and content revision (PRD §12.1).
func buildSnapshot(cfg *config.Config) (*Snapshot, error) {
	table, err := router.BuildTable(cfg.Routes)
	if err != nil {
		return nil, fmt.Errorf("build route table: %w", err)
	}
	pool, err := transport.NewPool(cfg.Transports, cfg.Network)
	if err != nil {
		return nil, fmt.Errorf("build transport pool: %w", err)
	}
	rev, err := config.Revision(cfg)
	if err != nil {
		pool.CloseIdleConnections()
		return nil, fmt.Errorf("compute configuration revision: %w", err)
	}
	return &Snapshot{
		Config:   cfg,
		Table:    table,
		Pool:     pool,
		Revision: rev,
		LoadedAt: time.Now(),
	}, nil
}

// updateMetrics swaps the bounded metric label set to the enabled routes
// of the new snapshot and records the transport count (§13, §22.2).
func (m *Manager) updateMetrics(snap *Snapshot) {
	if m.reg == nil {
		return
	}
	routes := snap.Table.Routes()
	names := make([]string, len(routes))
	for i, r := range routes {
		names[i] = r.Name
	}
	m.reg.SetRoutes(names)
	m.reg.SetTransportCount(snap.Pool.Len())
}

// notifySwap calls every registered OnSwap callback synchronously with
// the newly activated snapshot, in registration order.
func (m *Manager) notifySwap(snap *Snapshot) {
	m.cbMu.Lock()
	cbs := make([]func(*Snapshot), len(m.onSwap))
	copy(cbs, m.onSwap)
	m.cbMu.Unlock()
	for _, fn := range cbs {
		fn(snap)
	}
}

func (m *Manager) setStatus(valid bool, lastError string) {
	m.statusMu.Lock()
	m.valid = valid
	m.lastError = lastError
	m.statusMu.Unlock()
}
