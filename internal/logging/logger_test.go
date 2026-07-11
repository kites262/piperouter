package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestParseLevel(t *testing.T) {
	tests := []struct {
		in      string
		want    slog.Level
		wantErr bool
	}{
		{in: "debug", want: slog.LevelDebug},
		{in: "info", want: slog.LevelInfo},
		{in: "warn", want: slog.LevelWarn},
		{in: "error", want: slog.LevelError},
		{in: "DEBUG", want: slog.LevelDebug},
		{in: "Info", want: slog.LevelInfo},
		{in: "WARN", want: slog.LevelWarn},
		{in: "ErRoR", want: slog.LevelError},
		{in: "trace", wantErr: true},
		{in: "warning", wantErr: true},
		{in: "", wantErr: true},
	}
	for _, tt := range tests {
		t.Run("level_"+tt.in, func(t *testing.T) {
			got, err := ParseLevel(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseLevel(%q) = %v, want error", tt.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseLevel(%q) error: %v", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("ParseLevel(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestNewLevelVarControlsLevel(t *testing.T) {
	l, lv := New(slog.LevelInfo)
	if l == nil || lv == nil {
		t.Fatal("New returned nil logger or level var")
	}
	if lv.Level() != slog.LevelInfo {
		t.Errorf("initial LevelVar = %v, want info", lv.Level())
	}
	ctx := context.Background()
	if l.Handler().Enabled(ctx, slog.LevelDebug) {
		t.Error("debug enabled at info level")
	}
	if !l.Handler().Enabled(ctx, slog.LevelInfo) {
		t.Error("info disabled at info level")
	}
	lv.Set(slog.LevelDebug) // hot log-level change
	if !l.Handler().Enabled(ctx, slog.LevelDebug) {
		t.Error("debug still disabled after LevelVar.Set(debug)")
	}
	lv.Set(slog.LevelError)
	if l.Handler().Enabled(ctx, slog.LevelWarn) {
		t.Error("warn enabled after LevelVar.Set(error)")
	}
}

func TestLogAccessFields(t *testing.T) {
	var buf bytes.Buffer
	l := slog.New(slog.NewJSONHandler(&buf, nil))

	// The caller is responsible for stripping the query string; the path
	// value must be recorded exactly as passed, with nothing appended.
	e := AccessEntry{
		Time:       time.Now(),
		Route:      "openai",
		Method:     "POST",
		Path:       "/openai/v1/chat/completions",
		Status:     200,
		DurationMs: 12.5,
		Transport:  "direct",
		Streaming:  "sse",
		Error:      "",
	}
	LogAccess(l, e)

	line := buf.String()
	if n := strings.Count(strings.TrimSpace(line), "\n"); n != 0 {
		t.Fatalf("LogAccess emitted %d lines, want exactly 1", n+1)
	}
	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, line)
	}

	checks := map[string]any{
		"msg":         "access",
		"level":       "INFO",
		"route":       "openai",
		"method":      "POST",
		"path":        "/openai/v1/chat/completions",
		"status":      float64(200),
		"duration_ms": 12.5,
		"transport":   "direct",
		"streaming":   "sse",
		"error":       "",
	}
	for key, want := range checks {
		got, ok := rec[key]
		if !ok {
			t.Errorf("field %q missing from record: %s", key, line)
			continue
		}
		if got != want {
			t.Errorf("field %q = %#v, want %#v", key, got, want)
		}
	}
	if _, ok := rec["time"]; !ok {
		t.Error("field \"time\" missing from record")
	}
	if strings.Contains(line, "?") {
		t.Errorf("record contains a query separator: %s", line)
	}
}

func TestLogAccessNilLoggerNoPanic(t *testing.T) {
	LogAccess(nil, AccessEntry{Method: "GET", Path: "/x"})
}
