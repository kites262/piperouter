// Package logging provides slog setup, access-log entries and the recent
// logs ring buffer (PRD §14). It never records bodies, query strings or
// header values (§14.3, §23.12) — with one deliberate exception: the
// client's forward headers (Forwarded, Via, X-Forwarded-*) are kept in the
// in-memory ring for the WebUI. They identify the original client and
// proxy chain; they are never credentials, and they never reach stdout.
package logging

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"
)

// New builds the process logger: a JSON handler writing to stdout. The
// returned LevelVar allows hot log-level changes on config reload.
func New(level slog.Level) (*slog.Logger, *slog.LevelVar) {
	lv := new(slog.LevelVar)
	lv.Set(level)
	h := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lv})
	return slog.New(h), lv
}

// ParseLevel maps a config log_level string (case-insensitive) to a
// slog.Level. Accepted values: debug, info, warn, error.
func ParseLevel(s string) (slog.Level, error) {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("invalid log level %q (want debug|info|warn|error)", s)
	}
}

// ForwardHeader is one captured proxy-metadata request header
// (Forwarded, Via or X-Forwarded-*). Name is canonical; Value is the
// inbound value, length-capped by the proxy before it gets here.
type ForwardHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// AccessEntry is one access-log record (PRD §14.3). Path never contains a
// query string; no header or body values are ever stored, except the
// forward headers described on ForwardHeaders.
type AccessEntry struct {
	Time       time.Time `json:"time"`
	Route      string    `json:"route"` // "" if unmatched
	Method     string    `json:"method"`
	Path       string    `json:"path"` // NO query string (§14.3)
	Status     int       `json:"status"`
	DurationMs float64   `json:"duration_ms"`
	Transport  string    `json:"transport"`
	Streaming  string    `json:"streaming"` // ""|"sse"|"websocket"
	Error      string    `json:"error"`     // classification code, "" if none
	// ForwardHeaders holds the inbound forward headers when the client sent
	// any — captured before strip_forward_headers removes them from the
	// outbound request. Ring/WebUI only; LogAccess never emits them.
	ForwardHeaders []ForwardHeader `json:"forward_headers,omitempty"`
}

// LogAccess emits exactly one Info record with msg "access" and structured
// fields mirroring the AccessEntry JSON tags. The record's own timestamp is
// provided by slog; only sanitized fields from e are attached.
func LogAccess(l *slog.Logger, e AccessEntry) {
	if l == nil {
		return
	}
	l.Info("access",
		slog.String("route", e.Route),
		slog.String("method", e.Method),
		slog.String("path", e.Path),
		slog.Int("status", e.Status),
		slog.Float64("duration_ms", e.DurationMs),
		slog.String("transport", e.Transport),
		slog.String("streaming", e.Streaming),
		slog.String("error", e.Error),
	)
}
