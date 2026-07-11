// Package runtime builds immutable configuration snapshots and swaps them
// atomically so the data plane never observes a partially-applied config
// (PRD §12).
package runtime

import (
	"errors"
	"time"

	"github.com/kites262/piperouter/internal/config"
	"github.com/kites262/piperouter/internal/router"
	"github.com/kites262/piperouter/internal/transport"
)

// ErrRevisionMismatch is returned by Manager.Apply when the caller's
// expected revision no longer matches the active configuration (PRD §12.3).
var ErrRevisionMismatch = errors.New("configuration revision mismatch")

// Snapshot is an immutable, fully-built runtime configuration. Requests
// capture one snapshot at start and use it for their whole lifetime; a
// config swap never affects in-flight requests (PRD §12.2).
type Snapshot struct {
	Config   *config.Config // normalized and validated
	Table    *router.Table
	Pool     *transport.Pool
	Revision string
	LoadedAt time.Time
}

// Status describes the configuration state as shown to the Admin API and
// WebUI (PRD §6.6, §19.9). Revision and LoadedAt always describe the
// ACTIVE snapshot, also while Valid is false.
type Status struct {
	Valid     bool      `json:"valid"`
	LastError string    `json:"last_error"`
	Revision  string    `json:"revision"`
	LoadedAt  time.Time `json:"loaded_at"`
}
