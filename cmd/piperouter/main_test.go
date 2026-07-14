package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The serve command is never started in unit tests; only flag parsing,
// validate and version are exercised (§22.6).

func writeFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

const validYAML = `version: v0.3
routes:
  - name: api
    prefix: /api
    options:
      target: https://api.example.com
`

const invalidYAML = `version: v0.3
transports:
  - name: bad
    type: http
    url: socks5://127.0.0.1:1080
routes:
  - name: api
    prefix: api
    options:
      target: https://api.example.com
      transport: nope
`

func TestRun(t *testing.T) {
	validPath := writeFile(t, "valid.yaml", validYAML)
	invalidPath := writeFile(t, "invalid.yaml", invalidYAML)
	missingPath := filepath.Join(t.TempDir(), "absent.yaml")

	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout []string // substrings
		wantStderr []string // substrings
	}{
		{
			name:       "version subcommand",
			args:       []string{"version"},
			wantCode:   0,
			wantStdout: []string{"piperouter", "0.3.2-dev", "go1."},
		},
		{
			name:       "validate valid config",
			args:       []string{"validate", "--config", validPath},
			wantCode:   0,
			wantStdout: []string{"configuration valid"},
		},
		{
			name:       "validate invalid config lists every issue on stderr",
			args:       []string{"validate", "--config", invalidPath},
			wantCode:   1,
			wantStderr: []string{"prefix", "transport"},
		},
		{
			name:       "validate missing file",
			args:       []string{"validate", "--config", missingPath},
			wantCode:   1,
			wantStderr: []string{"absent.yaml"},
		},
		{
			name:     "validate rejects positional arguments",
			args:     []string{"validate", "extra"},
			wantCode: 2,
		},
		{
			name:       "unknown command",
			args:       []string{"frobnicate"},
			wantCode:   2,
			wantStderr: []string{"unknown command", "Usage"},
		},
		{
			name:       "help",
			args:       []string{"help"},
			wantCode:   0,
			wantStdout: []string{"Usage", "validate", "--proxy-listen"},
		},
		{
			name:       "serve with unknown flag fails before starting",
			args:       []string{"serve", "--bogus"},
			wantCode:   2,
			wantStderr: []string{"bogus"},
		},
		{
			name:     "default serve with unknown flag fails before starting",
			args:     []string{"--bogus"},
			wantCode: 2,
		},
		{
			name:       "serve rejects positional arguments",
			args:       []string{"serve", "positional"},
			wantCode:   2,
			wantStderr: []string{"unexpected argument"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr strings.Builder
			code := run(tt.args, &stdout, &stderr)
			if code != tt.wantCode {
				t.Fatalf("run(%v) = %d, want %d\nstdout: %s\nstderr: %s",
					tt.args, code, tt.wantCode, stdout.String(), stderr.String())
			}
			for _, want := range tt.wantStdout {
				if !strings.Contains(stdout.String(), want) {
					t.Errorf("stdout missing %q; got:\n%s", want, stdout.String())
				}
			}
			for _, want := range tt.wantStderr {
				if !strings.Contains(stderr.String(), want) {
					t.Errorf("stderr missing %q; got:\n%s", want, stderr.String())
				}
			}
		})
	}
}

func TestValidateIssuesOnePerLine(t *testing.T) {
	invalidPath := writeFile(t, "invalid.yaml", invalidYAML)
	var stdout, stderr strings.Builder
	if code := run([]string{"validate", "--config", invalidPath}, &stdout, &stderr); code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	lines := strings.Split(strings.TrimSpace(stderr.String()), "\n")
	if len(lines) < 2 {
		t.Fatalf("want one issue per line (>= 2 lines), got %d:\n%s", len(lines), stderr.String())
	}
	if strings.Contains(stderr.String(), "invalid configuration:") {
		t.Errorf("issues should be printed bare, not as the joined error: %s", stderr.String())
	}
}

func TestParseServeOptions(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		var stderr strings.Builder
		opts, code := parseServeOptions(nil, &stderr)
		if code != -1 {
			t.Fatalf("code = %d, want -1 (stderr %s)", code, stderr.String())
		}
		if opts.ConfigPath != defaultConfigPath {
			t.Errorf("ConfigPath = %q, want %q", opts.ConfigPath, defaultConfigPath)
		}
		if opts.Version != version {
			t.Errorf("Version = %q, want %q", opts.Version, version)
		}
		if opts.ProxyListen != "" || opts.AdminListen != "" || opts.LogLevel != "" ||
			opts.DisableAdmin || opts.DisableWeb {
			t.Errorf("overrides should default to zero values, got %+v", opts)
		}
	})

	t.Run("all flags", func(t *testing.T) {
		var stderr strings.Builder
		opts, code := parseServeOptions([]string{
			"--config", "custom.yaml",
			"--proxy-listen", ":18080",
			"--admin-listen", "127.0.0.1:19090",
			"--disable-admin",
			"--disable-web",
			"--log-level", "debug",
		}, &stderr)
		if code != -1 {
			t.Fatalf("code = %d, want -1 (stderr %s)", code, stderr.String())
		}
		if opts.ConfigPath != "custom.yaml" || opts.ProxyListen != ":18080" ||
			opts.AdminListen != "127.0.0.1:19090" || !opts.DisableAdmin ||
			!opts.DisableWeb || opts.LogLevel != "debug" {
			t.Errorf("parsed options = %+v", opts)
		}
	})

	t.Run("help exits zero", func(t *testing.T) {
		var stderr strings.Builder
		_, code := parseServeOptions([]string{"-h"}, &stderr)
		if code != 0 {
			t.Fatalf("code = %d, want 0 for -h", code)
		}
	})
}
