package runtime

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/kites262/piperouter/internal/config"
	"github.com/kites262/piperouter/internal/metrics"
)

const validYAML = `version: 1
transports:
  - name: proxy-a
    type: http
    url: http://127.0.0.1:7890
routes:
  - name: api
    prefix: /api
    target: http://127.0.0.1:9000
`

func writeConfigFile(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "piperouter.yaml")
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write config file: %v", err)
	}
	return path
}

func testLogger() *slog.Logger { return slog.New(slog.DiscardHandler) }

// newTestManager builds a Manager over a fresh temp config file.
func newTestManager(t *testing.T) (*Manager, string, *metrics.Registry) {
	t.Helper()
	path := writeConfigFile(t, validYAML)
	reg := metrics.NewRegistry()
	m, err := NewManager(path, testLogger(), reg)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	return m, path, reg
}

// fileRevision loads the config file and returns its content revision.
func fileRevision(t *testing.T, path string) string {
	t.Helper()
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("load %s: %v", path, err)
	}
	rev, err := config.Revision(cfg)
	if err != nil {
		t.Fatalf("revision: %v", err)
	}
	return rev
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func TestNewManagerHappyPath(t *testing.T) {
	m, path, reg := newTestManager(t)

	snap := m.Current()
	if snap == nil {
		t.Fatal("Current() = nil")
	}
	if !strings.HasPrefix(snap.Revision, "sha256:") {
		t.Errorf("Revision = %q, want sha256: prefix", snap.Revision)
	}
	if snap.LoadedAt.IsZero() {
		t.Error("LoadedAt is zero")
	}
	if got := snap.Table.Len(); got != 1 {
		t.Errorf("Table.Len() = %d, want 1", got)
	}
	if got := snap.Pool.Len(); got != 2 { // direct + proxy-a
		t.Errorf("Pool.Len() = %d, want 2", got)
	}
	if _, ok := snap.Pool.Get("proxy-a"); !ok {
		t.Error(`Pool.Get("proxy-a") not found`)
	}
	if m.ConfigPath() != path {
		t.Errorf("ConfigPath() = %q, want %q", m.ConfigPath(), path)
	}

	st := m.Status()
	if !st.Valid || st.LastError != "" {
		t.Errorf("Status() = %+v, want valid with empty LastError", st)
	}
	if st.Revision != snap.Revision || !st.LoadedAt.Equal(snap.LoadedAt) {
		t.Errorf("Status revision/loadedAt = (%q, %v), want (%q, %v)",
			st.Revision, st.LoadedAt, snap.Revision, snap.LoadedAt)
	}

	ms := reg.Snapshot()
	if ms.RouteCount != 1 {
		t.Errorf("metrics RouteCount = %d, want 1", ms.RouteCount)
	}
	if ms.TransportCount != 2 {
		t.Errorf("metrics TransportCount = %d, want 2", ms.TransportCount)
	}
}

func TestNewManagerSkipsDisabledRoutesInMetrics(t *testing.T) {
	yaml := validYAML + `  - name: off
    enabled: false
    prefix: /off
    target: http://127.0.0.1:9001
`
	path := writeConfigFile(t, yaml)
	reg := metrics.NewRegistry()
	m, err := NewManager(path, testLogger(), reg)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	if got := m.Current().Table.Len(); got != 1 {
		t.Errorf("Table.Len() = %d, want 1 (disabled route skipped)", got)
	}
	if got := reg.Snapshot().RouteCount; got != 1 {
		t.Errorf("metrics RouteCount = %d, want 1", got)
	}
	if _, ok := reg.RouteSnapshot("off"); ok {
		t.Error(`disabled route "off" must not get a metrics series`)
	}
}

func TestNewManagerNilRegistry(t *testing.T) {
	path := writeConfigFile(t, validYAML)
	m, err := NewManager(path, testLogger(), nil)
	if err != nil {
		t.Fatalf("NewManager with nil registry: %v", err)
	}
	// Mutations must not panic without a registry either.
	if err := m.ReloadFromFile(); err != nil {
		t.Fatalf("ReloadFromFile: %v", err)
	}
	cfg := m.Current().Config.Clone()
	cfg.Routes[0].Target = "http://127.0.0.1:9001"
	if _, err := m.Apply(cfg, ""); err != nil {
		t.Fatalf("Apply: %v", err)
	}
}

func TestNewManagerErrors(t *testing.T) {
	tests := []struct {
		name           string
		path           func(t *testing.T) string
		logger         *slog.Logger
		wantValidation bool
	}{
		{
			name:   "missing file",
			path:   func(t *testing.T) string { return filepath.Join(t.TempDir(), "absent.yaml") },
			logger: testLogger(),
		},
		{
			name:   "invalid yaml",
			path:   func(t *testing.T) string { return writeConfigFile(t, "version: [not\n  closed") },
			logger: testLogger(),
		},
		{
			name:   "unknown field",
			path:   func(t *testing.T) string { return writeConfigFile(t, "version: 1\nbogus_field: true\n") },
			logger: testLogger(),
		},
		{
			name: "validation failure",
			path: func(t *testing.T) string {
				return writeConfigFile(t, `version: 1
routes:
  - name: bad
    prefix: no-slash
    target: http://127.0.0.1:9000
`)
			},
			logger:         testLogger(),
			wantValidation: true,
		},
		{
			name:   "nil logger",
			path:   func(t *testing.T) string { return writeConfigFile(t, validYAML) },
			logger: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := NewManager(tt.path(t), tt.logger, metrics.NewRegistry())
			if err == nil {
				t.Fatal("NewManager succeeded, want error")
			}
			if m != nil {
				t.Errorf("NewManager returned non-nil manager with error %v", err)
			}
			var ve *config.ValidationError
			if got := errors.As(err, &ve); got != tt.wantValidation {
				t.Errorf("errors.As(*config.ValidationError) = %v, want %v (err: %v)", got, tt.wantValidation, err)
			}
		})
	}
}

func TestApplyHappyPath(t *testing.T) {
	m, path, reg := newTestManager(t)
	old := m.Current()

	newCfg := old.Config.Clone()
	newCfg.Routes = append(newCfg.Routes, config.RouteConfig{
		Name:   "extra",
		Prefix: "/extra",
		Target: "http://127.0.0.1:9100",
	})
	// Leave pointers nil on the added route to prove Apply normalizes a
	// clone, not the caller's config.
	snap, err := m.Apply(newCfg, old.Revision)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if snap == old {
		t.Error("Apply returned the old snapshot")
	}
	if m.Current() != snap {
		t.Error("Current() != snapshot returned by Apply")
	}
	if snap.Config == newCfg {
		t.Error("snapshot config must be a clone, not the caller's config")
	}
	if newCfg.Routes[1].Enabled != nil || newCfg.Routes[1].StripPrefix != nil {
		t.Error("Apply normalized the caller's config in place")
	}
	if got := snap.Table.Len(); got != 2 {
		t.Errorf("Table.Len() = %d, want 2", got)
	}
	if snap.Revision == old.Revision {
		t.Error("revision did not change")
	}

	// File rewritten atomically: parse it back, revision must match.
	if got := fileRevision(t, path); got != snap.Revision {
		t.Errorf("file revision = %q, want %q", got, snap.Revision)
	}
	// Backup of the previous content exists and matches the old revision.
	if got := fileRevision(t, path+".bak"); got != old.Revision {
		t.Errorf(".bak revision = %q, want old %q", got, old.Revision)
	}

	st := m.Status()
	if !st.Valid || st.LastError != "" || st.Revision != snap.Revision {
		t.Errorf("Status() = %+v, want valid with revision %q", st, snap.Revision)
	}
	ms := reg.Snapshot()
	if ms.RouteCount != 2 {
		t.Errorf("metrics RouteCount = %d, want 2", ms.RouteCount)
	}
}

func TestApplyRevisionMismatch(t *testing.T) {
	m, path, _ := newTestManager(t)
	old := m.Current()
	before := readFile(t, path)

	newCfg := old.Config.Clone()
	newCfg.Routes[0].Target = "http://127.0.0.1:9500"
	_, err := m.Apply(newCfg, "sha256:deadbeef")
	if !errors.Is(err, ErrRevisionMismatch) {
		t.Fatalf("Apply error = %v, want ErrRevisionMismatch", err)
	}
	if m.Current() != old {
		t.Error("snapshot changed on revision mismatch")
	}
	if got := readFile(t, path); got != before {
		t.Error("config file changed on revision mismatch")
	}
	if st := m.Status(); !st.Valid || st.Revision != old.Revision {
		t.Errorf("Status() = %+v, want still valid at old revision", st)
	}
}

func TestApplyValidationError(t *testing.T) {
	m, path, _ := newTestManager(t)
	old := m.Current()
	before := readFile(t, path)

	newCfg := old.Config.Clone()
	newCfg.Routes = append(newCfg.Routes, config.RouteConfig{
		Name:   "bad",
		Prefix: "no-slash",
		Target: "http://127.0.0.1:9100",
	})
	_, err := m.Apply(newCfg, old.Revision)
	var ve *config.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("Apply error = %v (%T), want *config.ValidationError", err, err)
	}
	if m.Current() != old {
		t.Error("snapshot changed on validation failure")
	}
	if got := readFile(t, path); got != before {
		t.Error("config file changed on validation failure")
	}
	if _, err := os.Stat(path + ".bak"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf(".bak stat = %v, want not-exist (write never attempted)", err)
	}
	if st := m.Status(); !st.Valid {
		t.Errorf("Status() = %+v, want still valid (active config unaffected)", st)
	}
}

func TestApplyEmptyExpectedRevisionSkipsCheck(t *testing.T) {
	m, path, _ := newTestManager(t)
	old := m.Current()

	newCfg := old.Config.Clone()
	newCfg.Routes[0].Target = "http://127.0.0.1:9600"
	snap, err := m.Apply(newCfg, "")
	if err != nil {
		t.Fatalf("Apply with empty expectedRevision: %v", err)
	}
	if snap == old || m.Current() != snap {
		t.Error("Apply did not swap the snapshot")
	}
	if got := fileRevision(t, path); got != snap.Revision {
		t.Errorf("file revision = %q, want %q", got, snap.Revision)
	}
}

func TestReloadFromFileExternalEdit(t *testing.T) {
	m, path, reg := newTestManager(t)
	old := m.Current()

	edited := strings.ReplaceAll(validYAML, "http://127.0.0.1:9000", "http://127.0.0.1:9700")
	if err := os.WriteFile(path, []byte(edited), 0o644); err != nil {
		t.Fatalf("external edit: %v", err)
	}
	if err := m.ReloadFromFile(); err != nil {
		t.Fatalf("ReloadFromFile: %v", err)
	}

	snap := m.Current()
	if snap == old {
		t.Fatal("snapshot pointer unchanged after external edit")
	}
	if snap.Revision == old.Revision {
		t.Error("revision unchanged after external edit")
	}
	if got := fileRevision(t, path); got != snap.Revision {
		t.Errorf("file revision = %q, want %q", got, snap.Revision)
	}
	// The old snapshot's Config is untouched.
	if got := old.Config.Routes[0].Target; got != "http://127.0.0.1:9000" {
		t.Errorf("old snapshot config mutated: target = %q", got)
	}
	if got := snap.Config.Routes[0].Target; got != "http://127.0.0.1:9700" {
		t.Errorf("new snapshot target = %q, want edited value", got)
	}
	st := m.Status()
	if !st.Valid || st.LastError != "" || st.Revision != snap.Revision {
		t.Errorf("Status() = %+v, want valid at new revision", st)
	}
	if got := reg.Snapshot().RouteCount; got != 1 {
		t.Errorf("metrics RouteCount = %d, want 1", got)
	}
}

func TestReloadFromFileInvalidThenRecover(t *testing.T) {
	tests := []struct {
		name           string
		badContent     string
		wantValidation bool
	}{
		{name: "unparsable yaml", badContent: "::: definitely not yaml {{{"},
		{
			name: "validation failure",
			badContent: `version: 1
routes:
  - name: bad
    prefix: no-slash
    target: http://127.0.0.1:9000
`,
			wantValidation: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, path, _ := newTestManager(t)
			old := m.Current()

			if err := os.WriteFile(path, []byte(tt.badContent), 0o644); err != nil {
				t.Fatalf("write bad config: %v", err)
			}
			err := m.ReloadFromFile()
			if err == nil {
				t.Fatal("ReloadFromFile succeeded on invalid file")
			}
			var ve *config.ValidationError
			if got := errors.As(err, &ve); got != tt.wantValidation {
				t.Errorf("errors.As(*config.ValidationError) = %v, want %v (err: %v)", got, tt.wantValidation, err)
			}
			if m.Current() != old {
				t.Error("snapshot changed after failed reload")
			}
			st := m.Status()
			if st.Valid {
				t.Error("Status().Valid = true after failed reload")
			}
			if st.LastError == "" {
				t.Error("Status().LastError empty after failed reload")
			}
			if st.Revision != old.Revision || !st.LoadedAt.Equal(old.LoadedAt) {
				t.Errorf("Status revision/loadedAt = (%q, %v), want active snapshot's (%q, %v)",
					st.Revision, st.LoadedAt, old.Revision, old.LoadedAt)
			}

			// A valid edit afterwards recovers.
			edited := strings.ReplaceAll(validYAML, "http://127.0.0.1:9000", "http://127.0.0.1:9800")
			if err := os.WriteFile(path, []byte(edited), 0o644); err != nil {
				t.Fatalf("write recovery config: %v", err)
			}
			if err := m.ReloadFromFile(); err != nil {
				t.Fatalf("ReloadFromFile after recovery: %v", err)
			}
			if m.Current() == old {
				t.Error("snapshot unchanged after recovery")
			}
			st = m.Status()
			if !st.Valid || st.LastError != "" {
				t.Errorf("Status() = %+v after recovery, want valid with empty LastError", st)
			}
		})
	}
}

func TestReloadFromFileNoOp(t *testing.T) {
	m, _, _ := newTestManager(t)
	old := m.Current()

	// File untouched since startup: same revision, no swap.
	if err := m.ReloadFromFile(); err != nil {
		t.Fatalf("ReloadFromFile: %v", err)
	}
	if m.Current() != old {
		t.Error("no-op reload swapped the snapshot")
	}

	// Watcher echo of our own Apply write: no second swap.
	newCfg := old.Config.Clone()
	newCfg.Routes[0].Target = "http://127.0.0.1:9900"
	snap, err := m.Apply(newCfg, "")
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if err := m.ReloadFromFile(); err != nil {
		t.Fatalf("ReloadFromFile after Apply: %v", err)
	}
	if m.Current() != snap {
		t.Error("reload after Apply swapped again; want echo suppression no-op")
	}
}

func TestOnSwapFires(t *testing.T) {
	m, path, _ := newTestManager(t)

	var got []*Snapshot
	m.OnSwap(func(s *Snapshot) { got = append(got, s) })

	// No-op reload must not fire.
	if err := m.ReloadFromFile(); err != nil {
		t.Fatalf("ReloadFromFile: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("OnSwap fired %d times after no-op reload, want 0", len(got))
	}

	// Apply fires with the new snapshot.
	newCfg := m.Current().Config.Clone()
	newCfg.Routes[0].Target = "http://127.0.0.1:9910"
	snap, err := m.Apply(newCfg, "")
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(got) != 1 || got[0] != snap {
		t.Fatalf("OnSwap after Apply: calls=%d, want 1 with the applied snapshot", len(got))
	}

	// ReloadFromFile fires with the new snapshot.
	edited := strings.ReplaceAll(validYAML, "http://127.0.0.1:9000", "http://127.0.0.1:9920")
	if err := os.WriteFile(path, []byte(edited), 0o644); err != nil {
		t.Fatalf("external edit: %v", err)
	}
	if err := m.ReloadFromFile(); err != nil {
		t.Fatalf("ReloadFromFile: %v", err)
	}
	if len(got) != 2 || got[1] != m.Current() {
		t.Fatalf("OnSwap after reload: calls=%d, want 2 with the current snapshot", len(got))
	}

	// Failed apply must not fire.
	bad := m.Current().Config.Clone()
	bad.Routes[0].Prefix = "no-slash"
	if _, err := m.Apply(bad, ""); err == nil {
		t.Fatal("Apply with invalid config succeeded")
	}
	if len(got) != 2 {
		t.Fatalf("OnSwap fired on failed Apply: calls=%d, want 2", len(got))
	}
}

func TestInFlightSnapshotSurvivesSwap(t *testing.T) {
	m, _, _ := newTestManager(t)
	inflight := m.Current()

	// Swap to a config that removes the api route and the proxy-a transport.
	newCfg := inflight.Config.Clone()
	newCfg.Routes = []config.RouteConfig{{
		Name:   "other",
		Prefix: "/other",
		Target: "http://127.0.0.1:9200",
	}}
	newCfg.Transports = nil
	if _, err := m.Apply(newCfg, inflight.Revision); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// The captured snapshot keeps working exactly as before the swap.
	r := inflight.Table.Match("/api/v1/things")
	if r == nil || r.Name != "api" {
		t.Fatalf("old Table.Match = %v, want route api", r)
	}
	entry, ok := inflight.Pool.Get("proxy-a")
	if !ok {
		t.Fatal(`old Pool.Get("proxy-a") gone after swap`)
	}
	if entry.RoundTripper == nil || entry.DialContext == nil {
		t.Error("old pool entry no longer usable")
	}
	if _, ok := inflight.Pool.Get("direct"); !ok {
		t.Error(`old Pool.Get("direct") gone after swap`)
	}
	if got := inflight.Config.Routes[0].Name; got != "api" {
		t.Errorf("old snapshot config mutated: routes[0].Name = %q", got)
	}

	// New requests see the new world.
	cur := m.Current()
	if cur.Table.Match("/api/v1/things") != nil {
		t.Error("new table still matches removed route")
	}
	if _, ok := cur.Pool.Get("proxy-a"); ok {
		t.Error("new pool still has removed transport")
	}
}

func TestConcurrentApplyReloadCurrent(t *testing.T) {
	m, path, _ := newTestManager(t)
	base := m.Current().Config

	const (
		appliers    = 4
		appliesEach = 8
		readers     = 4
		reloaders   = 2
	)

	stop := make(chan struct{})
	var bg sync.WaitGroup

	for i := 0; i < readers; i++ {
		bg.Add(1)
		go func() {
			defer bg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				snap := m.Current()
				if snap == nil {
					t.Error("Current() = nil")
					return
				}
				// Every observed snapshot must be fully usable.
				snap.Table.Match("/api/x")
				if _, ok := snap.Pool.Get("direct"); !ok {
					t.Error(`snapshot pool missing "direct"`)
					return
				}
				if st := m.Status(); st.Revision == "" {
					t.Error("Status().Revision empty")
					return
				}
			}
		}()
	}

	for i := 0; i < reloaders; i++ {
		bg.Add(1)
		go func() {
			defer bg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				// The file is always valid here (only Apply writes it),
				// so reloads must never fail.
				if err := m.ReloadFromFile(); err != nil {
					t.Errorf("ReloadFromFile: %v", err)
					return
				}
			}
		}()
	}

	inflight := m.Current() // captured before any concurrent swap

	var apply sync.WaitGroup
	for i := 0; i < appliers; i++ {
		apply.Add(1)
		go func(id int) {
			defer apply.Done()
			for j := 0; j < appliesEach; j++ {
				cfg := base.Clone()
				cfg.Routes = append(cfg.Routes, config.RouteConfig{
					Name:   fmt.Sprintf("r-%d-%d", id, j),
					Prefix: fmt.Sprintf("/r-%d-%d", id, j),
					Target: "http://127.0.0.1:9999",
				})
				if _, err := m.Apply(cfg, ""); err != nil {
					t.Errorf("Apply(%d,%d): %v", id, j, err)
					return
				}
			}
		}(i)
	}
	apply.Wait()
	close(stop)
	bg.Wait()

	// Serializability: the final file parses as valid and matches the
	// final snapshot's revision exactly.
	final := m.Current()
	if got := fileRevision(t, path); got != final.Revision {
		t.Errorf("final file revision = %q, want %q", got, final.Revision)
	}
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("final file load: %v", err)
	}
	if err := config.Validate(loaded); err != nil {
		t.Errorf("final file invalid: %v", err)
	}
	if st := m.Status(); !st.Valid || st.Revision != final.Revision {
		t.Errorf("final Status() = %+v, want valid at %q", st, final.Revision)
	}

	// The snapshot captured before the churn is still fully usable.
	if r := inflight.Table.Match("/api/x"); r == nil || r.Name != "api" {
		t.Errorf("in-flight Table.Match = %v, want route api", r)
	}
	if _, ok := inflight.Pool.Get("proxy-a"); !ok {
		t.Error(`in-flight Pool.Get("proxy-a") gone`)
	}
}
