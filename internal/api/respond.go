package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/kites262/piperouter/internal/config"
	"github.com/kites262/piperouter/internal/runtime"
)

// apiError is the client-visible error envelope (ApiError in openapi.yaml).
type apiError struct {
	Error  string   `json:"error"`
	Detail string   `json:"detail,omitempty"`
	Issues []string `json:"issues,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, detail string) {
	writeJSON(w, status, apiError{Error: code, Detail: detail})
}

func writeValidationFailed(w http.ResponseWriter, issues []string) {
	if issues == nil {
		issues = []string{}
	}
	writeJSON(w, http.StatusBadRequest, apiError{Error: "validation_failed", Issues: issues})
}

// decodeJSON strictly decodes a required JSON body into dst; on failure it
// writes the error response and returns false. Unknown fields are rejected
// (PRD §6.3 strict validation, §22.4) — a typo like "routez" or
// "recent_log" is a 400 validation_failed, never silent data loss.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	err := dec.Decode(dst)
	if err == nil {
		return true
	}
	writeBodyError(w, err)
	return false
}

// decodeOptionalRevision reads the OPTIONAL DELETE body {"revision":...};
// an absent or empty body means "no conflict check".
func decodeOptionalRevision(w http.ResponseWriter, r *http.Request) (string, bool) {
	var req struct {
		Revision string `json:"revision"`
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	err := dec.Decode(&req)
	if err == nil || errors.Is(err, io.EOF) {
		return req.Revision, true
	}
	writeBodyError(w, err)
	return "", false
}

func writeBodyError(w http.ResponseWriter, err error) {
	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) {
		writeError(w, http.StatusRequestEntityTooLarge, "invalid_request", "request body exceeds 1 MiB")
		return
	}
	// A rejected unknown field is a strict-validation failure, not a
	// malformed body: surface the offending field so the caller can fix it.
	if msg := err.Error(); strings.Contains(msg, "unknown field") {
		writeValidationFailed(w, []string{msg})
		return
	}
	writeError(w, http.StatusBadRequest, "invalid_request", "malformed JSON request body")
}

// mutationError is a domain error a mutate closure returns to Manager.Update
// so applyUpdate can map it to a specific wire status (not-found, conflict).
type mutationError struct {
	status int
	code   string
	detail string
}

func (e *mutationError) Error() string { return e.code }

// applyUpdate runs an atomic read-modify-write through the manager and maps
// failures to wire errors: revision conflict → 409, validation → 400 with
// issues, a mutationError → its status/code, anything else → 500.
func (s *server) applyUpdate(w http.ResponseWriter, revision string, mutate func(*config.Config) error) (*runtime.Snapshot, bool) {
	next, err := s.deps.Manager.Update(revision, mutate)
	if err == nil {
		return next, true
	}
	if errors.Is(err, runtime.ErrRevisionMismatch) {
		writeError(w, http.StatusConflict, "revision_conflict",
			"configuration was modified by another operation; reload and retry")
		return nil, false
	}
	var ve *config.ValidationError
	if errors.As(err, &ve) {
		writeValidationFailed(w, ve.Issues)
		return nil, false
	}
	var me *mutationError
	if errors.As(err, &me) {
		writeError(w, me.status, me.code, me.detail)
		return nil, false
	}
	s.deps.Logger.Error("api: configuration update failed", "error", err.Error())
	writeError(w, http.StatusInternalServerError, "internal_error", "failed to apply configuration")
	return nil, false
}

// apply runs one configuration mutation through the manager and maps
// failures to wire errors: revision conflict → 409, validation → 400
// with issues, anything else → 500 (detail kept generic; the real error
// goes to the app log only).
func (s *server) apply(w http.ResponseWriter, cfg *config.Config, expectedRevision string) (*runtime.Snapshot, bool) {
	next, err := s.deps.Manager.Apply(cfg, expectedRevision)
	if err == nil {
		return next, true
	}
	if errors.Is(err, runtime.ErrRevisionMismatch) {
		writeError(w, http.StatusConflict, "revision_conflict",
			"configuration was modified by another operation; reload and retry")
		return nil, false
	}
	var ve *config.ValidationError
	if errors.As(err, &ve) {
		writeValidationFailed(w, ve.Issues)
		return nil, false
	}
	s.deps.Logger.Error("api: configuration apply failed", "error", err.Error())
	writeError(w, http.StatusInternalServerError, "internal_error", "failed to apply configuration")
	return nil, false
}

func findRouteIndex(routes []config.RouteConfig, name string) int {
	for i := range routes {
		if routes[i].Name == name {
			return i
		}
	}
	return -1
}

func findTransportIndex(transports []config.TransportConfig, name string) int {
	for i := range transports {
		if transports[i].Name == name {
			return i
		}
	}
	return -1
}
