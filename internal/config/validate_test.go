package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// baseConfig returns a normalized, fully valid configuration used as the
// starting point for the rejection matrix.
func baseConfig() *Config {
	c := &Config{
		Version: SupportedVersion,
		Transports: []TransportConfig{
			{Name: "jp-proxy", Type: TransportHTTP, URL: "http://127.0.0.1:7890"},
			{Name: "us-socks", Type: TransportSOCKS5, URL: "socks5://127.0.0.1:1080"},
		},
		Routes: []RouteConfig{
			{Name: "openai", Prefix: "/openai", Target: "https://api.openai.com/v1", Transport: "jp-proxy"},
			{Name: "github", Prefix: "/github", Target: "https://api.github.com"},
		},
	}
	c.Normalize()
	return c
}

func TestValidateAcceptsBaseConfig(t *testing.T) {
	if err := Validate(baseConfig(), ""); err != nil {
		t.Fatalf("Validate(base) = %v, want nil", err)
	}
}

func TestValidateAcceptsStaticFileRoute(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "index.html")
	if err := os.WriteFile(path, []byte("<html>ok</html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := baseConfig()
	c.Routes = append(c.Routes, RouteConfig{
		Name:   "landing",
		Type:   RouteTypeStatic,
		Prefix: "/",
		Target: path,
	})
	// Two routes cannot share prefix — base already has /openai and /github.
	// Use a free prefix instead of "/".
	c.Routes[len(c.Routes)-1].Prefix = "/home"
	c.Normalize()
	if err := Validate(c, ""); err != nil {
		t.Fatalf("Validate(static) = %v, want nil", err)
	}
}

func TestValidateRejectsStaticDirectory(t *testing.T) {
	dir := t.TempDir()
	c := baseConfig()
	c.Routes[0].Type = RouteTypeStatic
	c.Routes[0].Target = dir
	c.Normalize()
	err := Validate(c, "")
	if err == nil {
		t.Fatal("Validate accepted directory as static target")
	}
	if !strings.Contains(err.Error(), "not a directory") && !strings.Contains(err.Error(), "must be a file") {
		t.Fatalf("error = %q, want directory rejection", err)
	}
}

// TestValidateRejectionMatrix is the full PRD §6.3 rejection matrix.
func TestValidateRejectionMatrix(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(c *Config)
		want   []string // substrings that must appear in the error
	}{
		{
			name:   "unsupported version",
			mutate: func(c *Config) { c.Version = 2 },
			want:   []string{"version 2"},
		},
		{
			name: "duplicate route name",
			mutate: func(c *Config) {
				c.Routes = append(c.Routes, RouteConfig{Name: "openai", Prefix: "/dup", Target: "https://dup.example.com", Transport: DirectName})
			},
			want: []string{"duplicate route name"},
		},
		{
			name: "duplicate transport name",
			mutate: func(c *Config) {
				c.Transports = append(c.Transports, TransportConfig{Name: "jp-proxy", Type: TransportHTTP, URL: "http://127.0.0.1:7891"})
			},
			want: []string{"duplicate transport name"},
		},
		{
			name: "duplicate prefix",
			mutate: func(c *Config) {
				c.Routes = append(c.Routes, RouteConfig{Name: "other", Prefix: "/openai", Target: "https://dup.example.com", Transport: DirectName})
			},
			want: []string{"duplicate prefix"},
		},
		{
			name:   "route references unknown transport",
			mutate: func(c *Config) { c.Routes[0].Transport = "no-such" },
			want:   []string{"unknown transport", "no-such"},
		},
		{
			name: "transport named direct",
			mutate: func(c *Config) {
				c.Transports = append(c.Transports, TransportConfig{Name: DirectName, Type: TransportHTTP, URL: "http://127.0.0.1:1"})
			},
			want: []string{"reserved"},
		},
		{
			name:   "route name empty",
			mutate: func(c *Config) { c.Routes[0].Name = "" },
			want:   []string{"name must match"},
		},
		{
			name:   "route name bad first char",
			mutate: func(c *Config) { c.Routes[0].Name = "-openai" },
			want:   []string{"name must match"},
		},
		{
			name:   "route name illegal char",
			mutate: func(c *Config) { c.Routes[0].Name = "open ai" },
			want:   []string{"name must match"},
		},
		{
			name:   "route name too long",
			mutate: func(c *Config) { c.Routes[0].Name = strings.Repeat("a", 65) },
			want:   []string{"name must match"},
		},
		{
			name:   "transport name invalid",
			mutate: func(c *Config) { c.Transports[0].Name = "_bad"; c.Routes[0].Transport = "_bad" },
			want:   []string{"name must match"},
		},
		{
			name:   "target relative",
			mutate: func(c *Config) { c.Routes[0].Target = "api.openai.com/v1" },
			want:   []string{"absolute"},
		},
		{
			name:   "target empty",
			mutate: func(c *Config) { c.Routes[0].Target = "" },
			want:   []string{"target is required"},
		},
		{
			name:   "target bad scheme",
			mutate: func(c *Config) { c.Routes[0].Target = "ftp://example.com/v1" },
			want:   []string{"scheme must be http or https"},
		},
		{
			name:   "target userinfo",
			mutate: func(c *Config) { c.Routes[0].Target = "https://user:pw@example.com/v1" },
			want:   []string{"userinfo"},
		},
		{
			name:   "target query",
			mutate: func(c *Config) { c.Routes[0].Target = "https://example.com/v1?key=1" },
			want:   []string{"query"},
		},
		{
			name:   "target force query",
			mutate: func(c *Config) { c.Routes[0].Target = "https://example.com/v1?" },
			want:   []string{"query"},
		},
		{
			name:   "target fragment",
			mutate: func(c *Config) { c.Routes[0].Target = "https://example.com/v1#frag" },
			want:   []string{"fragment"},
		},
		{
			name:   "target empty host",
			mutate: func(c *Config) { c.Routes[0].Target = "https:///v1" },
			want:   []string{"host must not be empty"},
		},
		{
			name:   "target unparseable",
			mutate: func(c *Config) { c.Routes[0].Target = "http://[::1" },
			want:   []string{"not a valid URL"},
		},
		{
			name:   "route type unsupported",
			mutate: func(c *Config) { c.Routes[0].Type = "redirect" },
			want:   []string{"type", "not supported"},
		},
		{
			name: "static target relative without baseDir",
			mutate: func(c *Config) {
				c.Routes[0].Type = RouteTypeStatic
				c.Routes[0].Target = "www/index.html"
			},
			want: []string{"relative"},
		},
		{
			name: "static target is URL",
			mutate: func(c *Config) {
				c.Routes[0].Type = RouteTypeStatic
				c.Routes[0].Target = "file:///var/www/index.html"
			},
			want: []string{"filesystem path", "not a URL"},
		},
		{
			name: "static target directory trailing slash",
			mutate: func(c *Config) {
				c.Routes[0].Type = RouteTypeStatic
				c.Routes[0].Target = "/var/www/"
			},
			want: []string{"not a directory"},
		},
		{
			name:   "proxy url userinfo",
			mutate: func(c *Config) { c.Transports[0].URL = "http://user:pw@127.0.0.1:7890" },
			want:   []string{"userinfo"},
		},
		{
			name:   "http transport with socks5 scheme",
			mutate: func(c *Config) { c.Transports[0].URL = "socks5://127.0.0.1:7890" },
			want:   []string{`scheme must be "http"`},
		},
		{
			name:   "socks5 transport with http scheme",
			mutate: func(c *Config) { c.Transports[1].URL = "http://127.0.0.1:1080" },
			want:   []string{`scheme must be "socks5"`},
		},
		{
			name:   "proxy url empty",
			mutate: func(c *Config) { c.Transports[0].URL = "" },
			want:   []string{"url is required"},
		},
		{
			name:   "proxy url empty host",
			mutate: func(c *Config) { c.Transports[0].URL = "http://" },
			want:   []string{"host must not be empty"},
		},
		{
			name:   "proxy url unparseable",
			mutate: func(c *Config) { c.Transports[0].URL = "http://[::1" },
			want:   []string{"not a valid URL"},
		},
		{
			name:   "transport type unsupported",
			mutate: func(c *Config) { c.Transports[0].Type = "ssh" },
			want:   []string{"not supported"},
		},
		{
			name: "transport type direct not declarable",
			mutate: func(c *Config) {
				c.Transports = append(c.Transports, TransportConfig{Name: "d2", Type: TransportDirect, URL: "http://127.0.0.1:1"})
			},
			want: []string{"not supported"},
		},
		{
			name:   "prefix missing leading slash",
			mutate: func(c *Config) { c.Routes[0].Prefix = "openai" },
			want:   []string{`prefix must start with "/"`},
		},
		{
			name:   "prefix with query char",
			mutate: func(c *Config) { c.Routes[0].Prefix = "/openai?x=1" },
			want:   []string{`"?" or "#"`},
		},
		{
			name:   "prefix with fragment char",
			mutate: func(c *Config) { c.Routes[0].Prefix = "/openai#frag" },
			want:   []string{`"?" or "#"`},
		},
		{
			name:   "prefix with dotdot segment",
			mutate: func(c *Config) { c.Routes[0].Prefix = "/a/../b" },
			want:   []string{`".." segments`},
		},
		{
			name:   "prefix with empty segment",
			mutate: func(c *Config) { c.Routes[0].Prefix = "/a//b" },
			want:   []string{"empty segments"},
		},
		{
			name:   "non-root prefix trailing slash",
			mutate: func(c *Config) { c.Routes[0].Prefix = "/openai/" },
			want:   []string{`must not end with "/"`},
		},
		{
			name: "tls enabled without files",
			mutate: func(c *Config) {
				c.Server.Proxy.TLS.Enabled = true
			},
			want: []string{"cert_file is empty", "key_file is empty"},
		},
		{
			name: "tls enabled missing key only",
			mutate: func(c *Config) {
				c.Server.Proxy.TLS.Enabled = true
				c.Server.Proxy.TLS.CertFile = "/etc/tls/cert.pem"
			},
			want: []string{"key_file is empty"},
		},
		{
			name:   "invalid log level",
			mutate: func(c *Config) { c.Runtime.LogLevel = "verbose" },
			want:   []string{"log_level"},
		},
		{
			name:   "negative recent_logs",
			mutate: func(c *Config) { c.Runtime.RecentLogs = intPtr(-1) },
			want:   []string{"recent_logs"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := baseConfig()
			tt.mutate(c)
			err := Validate(c, "")
			if err == nil {
				t.Fatal("Validate accepted invalid configuration")
			}
			var verr *ValidationError
			if !errors.As(err, &verr) {
				t.Fatalf("error type = %T, want *ValidationError", err)
			}
			if len(verr.Issues) == 0 {
				t.Fatal("ValidationError has no issues")
			}
			msg := err.Error()
			if !strings.HasPrefix(msg, "invalid configuration: ") {
				t.Errorf("Error() = %q, want 'invalid configuration: ' prefix", msg)
			}
			for _, want := range tt.want {
				if !strings.Contains(msg, want) {
					t.Errorf("error %q missing substring %q", msg, want)
				}
			}
		})
	}
}

// TestValidatePrefixForms is the PRD §6.4 prefix form matrix.
func TestValidatePrefixForms(t *testing.T) {
	tests := []struct {
		prefix string
		ok     bool
	}{
		{"/openai", true},
		{"/openai/", false},
		{"/", true},
		{"openai", false},
		{"/a//b", false},
		{"/a/../b", false},
		{"", false},
		{"/..", false},
		{"/a?b", false},
		{"/a#b", false},
		{"/a/b.c", true},
		{"/a/", false},
		{"//", false},
		{"/v1/chat", true},
	}
	for _, tt := range tests {
		t.Run(tt.prefix, func(t *testing.T) {
			c := &Config{
				Version: SupportedVersion,
				Routes: []RouteConfig{
					{Name: "r1", Prefix: tt.prefix, Target: "https://example.com"},
				},
			}
			c.Normalize()
			err := Validate(c, "")
			if tt.ok && err != nil {
				t.Errorf("Validate rejected valid prefix %q: %v", tt.prefix, err)
			}
			if !tt.ok && err == nil {
				t.Errorf("Validate accepted invalid prefix %q", tt.prefix)
			}
		})
	}
}

func TestValidateCollectsAllIssues(t *testing.T) {
	c := baseConfig()
	c.Version = 3
	c.Routes[0].Prefix = "bad"
	c.Routes[1].Target = "ftp://example.com"
	c.Runtime.LogLevel = "loud"
	c.Runtime.RecentLogs = intPtr(-5)

	err := Validate(c, "")
	if err == nil {
		t.Fatal("Validate accepted invalid configuration")
	}
	var verr *ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("error type = %T, want *ValidationError", err)
	}
	if len(verr.Issues) < 5 {
		t.Errorf("Issues = %d, want >= 5: %v", len(verr.Issues), verr.Issues)
	}
	if got := strings.Count(err.Error(), "; "); got < 4 {
		t.Errorf("Error() joined %d separators, want >= 4: %q", got, err.Error())
	}
}

func TestValidateEmptyNormalizedConfig(t *testing.T) {
	c := &Config{Version: SupportedVersion}
	c.Normalize()
	if err := Validate(c, ""); err != nil {
		t.Fatalf("Validate(empty normalized) = %v, want nil", err)
	}
}

func TestValidateExplicitDirectRoute(t *testing.T) {
	c := &Config{
		Version: SupportedVersion,
		Routes: []RouteConfig{
			{Name: "r1", Prefix: "/r1", Target: "https://example.com", Transport: DirectName},
		},
	}
	c.Normalize()
	if err := Validate(c, ""); err != nil {
		t.Fatalf("Validate = %v, want nil (direct is always known)", err)
	}
}
