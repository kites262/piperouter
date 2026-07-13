package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

const minimalYAML = `
version: 1
routes:
  - name: r1
    prefix: /r1
    target: https://example.com
`

func TestParseDefaults(t *testing.T) {
	c, err := Parse([]byte(minimalYAML))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	tests := []struct {
		name string
		got  any
		want any
	}{
		{"proxy listen", c.Server.Proxy.Listen, ":8080"},
		{"tls enabled", c.Server.Proxy.TLS.Enabled, false},
		{"admin enabled", c.Server.Admin.IsEnabled(), true},
		{"admin listen", c.Server.Admin.Listen, "127.0.0.1:9090"},
		{"web enabled", c.Server.Web.IsEnabled(), true},
		{"log level", c.Runtime.LogLevel, "info"},
		{"recent logs", *c.Runtime.RecentLogs, 1000},
		{"dial timeout", c.Network.DialTimeout.Std(), 10 * time.Second},
		{"tls handshake timeout", c.Network.TLSHandshakeTimeout.Std(), 10 * time.Second},
		{"response header timeout", c.Network.ResponseHeaderTimeout.Std(), 120 * time.Second},
		{"idle connection timeout", c.Network.IdleConnectionTimeout.Std(), 90 * time.Second},
		{"route type", c.Routes[0].Type, RouteTypeProxy},
		{"route enabled", c.Routes[0].IsEnabled(), true},
		{"route strip_prefix", c.Routes[0].StripsPrefix(), true},
		{"route strip_forward_headers", c.Routes[0].StripsForwardHeaders(), true},
		{"route transport", c.Routes[0].Transport, DirectName},
	}
	for _, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("%s = %v, want %v", tt.name, tt.got, tt.want)
		}
	}
}

func TestParseUnknownFieldRejected(t *testing.T) {
	tests := []struct {
		name  string
		yaml  string
		field string
	}{
		{"top level", "version: 1\nbogus: true\n", "bogus"},
		{"nested server", "version: 1\nserver:\n  proxy:\n    lisen: \":8080\"\n", "lisen"},
		{"route field", "version: 1\nroutes:\n  - name: a\n    prefix: /a\n    target: https://x.com\n    striip: true\n", "striip"},
		{"transport field", "version: 1\ntransports:\n  - name: a\n    type: http\n    url: http://h:1\n    weight: 3\n", "weight"},
		{"runtime field", "version: 1\nruntime:\n  log_levl: info\n", "log_levl"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.yaml))
			if err == nil {
				t.Fatal("Parse accepted unknown field")
			}
			msg := err.Error()
			if !strings.Contains(msg, "unknown") {
				t.Errorf("error %q does not mention unknown field", msg)
			}
			if !strings.Contains(msg, tt.field) {
				t.Errorf("error %q does not name field %q", msg, tt.field)
			}
		})
	}
}

func TestParseVersion(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantErr bool
	}{
		{"version 1", "version: 1\n", false},
		{"empty input", "", true},
		{"null document", "null\n", true},
		{"missing version", "runtime:\n  log_level: info\n", true},
		{"version 0", "version: 0\n", true},
		{"version 2", "version: 2\n", true},
		{"negative version", "version: -1\n", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := Parse([]byte(tt.yaml))
			if tt.wantErr {
				if err == nil {
					t.Fatal("Parse accepted unsupported version")
				}
				if !strings.Contains(err.Error(), "version") {
					t.Errorf("error %q does not mention the version", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if c.Version != SupportedVersion {
				t.Errorf("Version = %d, want %d", c.Version, SupportedVersion)
			}
		})
	}
}

func TestParseMalformedInput(t *testing.T) {
	tests := []struct {
		name string
		yaml string
	}{
		{"not yaml", ":\n:::"},
		{"wrong type version", "version: \"one\"\n"},
		{"wrong type routes", "version: 1\nroutes: notalist\n"},
		{"bad duration", "version: 1\nnetwork:\n  dial_timeout: 5 parsecs\n"},
		{"non-string duration", "version: 1\nnetwork:\n  dial_timeout: [1, 2]\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Parse([]byte(tt.yaml)); err == nil {
				t.Fatal("Parse accepted malformed input")
			}
		})
	}
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "piperouter.yaml")
	if err := os.WriteFile(path, []byte(minimalYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	c, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(c.Routes) != 1 || c.Routes[0].Name != "r1" {
		t.Errorf("unexpected routes: %+v", c.Routes)
	}

	_, err = Load(filepath.Join(dir, "missing.yaml"))
	if err == nil {
		t.Fatal("Load succeeded for missing file")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("error %v is not os.ErrNotExist", err)
	}
}

func TestDurationRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		d    Duration
		text string
	}{
		{"seconds", Duration(10 * time.Second), "10s"},
		{"composite", Duration(90 * time.Second), "1m30s"},
		{"millis", Duration(250 * time.Millisecond), "250ms"},
		{"zero", Duration(0), "0s"},
	}
	for _, tt := range tests {
		t.Run(tt.name+"/yaml", func(t *testing.T) {
			out, err := yaml.Marshal(tt.d)
			if err != nil {
				t.Fatalf("yaml.Marshal: %v", err)
			}
			if got := strings.TrimSpace(string(out)); got != tt.text {
				t.Errorf("yaml = %q, want %q", got, tt.text)
			}
			var back Duration
			if err := yaml.Unmarshal(out, &back); err != nil {
				t.Fatalf("yaml.Unmarshal: %v", err)
			}
			if back != tt.d {
				t.Errorf("round-trip = %v, want %v", back, tt.d)
			}
		})
		t.Run(tt.name+"/json", func(t *testing.T) {
			out, err := json.Marshal(tt.d)
			if err != nil {
				t.Fatalf("json.Marshal: %v", err)
			}
			if want := `"` + tt.text + `"`; string(out) != want {
				t.Errorf("json = %s, want %s", out, want)
			}
			var back Duration
			if err := json.Unmarshal(out, &back); err != nil {
				t.Fatalf("json.Unmarshal: %v", err)
			}
			if back != tt.d {
				t.Errorf("round-trip = %v, want %v", back, tt.d)
			}
		})
	}
}

func TestDurationRejectsInvalid(t *testing.T) {
	var d Duration
	if err := yaml.Unmarshal([]byte(`"soon"`), &d); err == nil {
		t.Error("yaml accepted invalid duration")
	}
	if err := json.Unmarshal([]byte(`"soon"`), &d); err == nil {
		t.Error("json accepted invalid duration")
	}
	if err := json.Unmarshal([]byte(`42`), &d); err == nil {
		t.Error("json accepted numeric duration")
	}
}

func TestParseDurationsFromConfig(t *testing.T) {
	c, err := Parse([]byte("version: 1\nnetwork:\n  dial_timeout: 3s\n  response_header_timeout: 2m\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := c.Network.DialTimeout.Std(); got != 3*time.Second {
		t.Errorf("dial_timeout = %v, want 3s", got)
	}
	if got := c.Network.ResponseHeaderTimeout.Std(); got != 2*time.Minute {
		t.Errorf("response_header_timeout = %v, want 2m", got)
	}
}
