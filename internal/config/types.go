// Package config defines the PipeRouter configuration schema, strict
// parsing/validation, atomic persistence and file watching.
//
// The YAML configuration file is the single source of truth (PRD §6).
package config

import (
	"encoding/json"
	"fmt"
	"time"

	"gopkg.in/yaml.v3"
)

// DirectName is the reserved name of the built-in direct transport.
// It always exists, cannot be declared, overridden or deleted (PRD §5.2).
const DirectName = "direct"

// SupportedVersion is the only accepted config schema version.
const SupportedVersion = 1

// NamePattern documents the constraint applied to Route and Transport names.
const NamePattern = `^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`

// Duration is a time.Duration that (un)marshals as a human string ("10s").
type Duration time.Duration

func (d Duration) Std() time.Duration { return time.Duration(d) }

func (d Duration) String() string { return time.Duration(d).String() }

func (d Duration) MarshalYAML() (interface{}, error) {
	return time.Duration(d).String(), nil
}

func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	var s string
	if err := value.Decode(&s); err != nil {
		return fmt.Errorf("duration must be a string like \"10s\": %w", err)
	}
	parsed, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", s, err)
	}
	*d = Duration(parsed)
	return nil
}

func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String())
}

func (d *Duration) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return fmt.Errorf("duration must be a string like \"10s\": %w", err)
	}
	parsed, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", s, err)
	}
	*d = Duration(parsed)
	return nil
}

// Config is the root configuration object (PRD §6.2).
type Config struct {
	Version    int               `yaml:"version" json:"version"`
	Server     ServerConfig      `yaml:"server" json:"server"`
	Runtime    RuntimeConfig     `yaml:"runtime" json:"runtime"`
	Network    NetworkConfig     `yaml:"network" json:"network"`
	Transports []TransportConfig `yaml:"transports" json:"transports"`
	Routes     []RouteConfig     `yaml:"routes" json:"routes"`
}

type ServerConfig struct {
	Proxy ProxyServerConfig `yaml:"proxy" json:"proxy"`
	Admin AdminServerConfig `yaml:"admin" json:"admin"`
	Web   WebConfig         `yaml:"web" json:"web"`
}

type ProxyServerConfig struct {
	Listen string    `yaml:"listen" json:"listen"`
	TLS    TLSConfig `yaml:"tls" json:"tls"`
}

type TLSConfig struct {
	Enabled  bool   `yaml:"enabled" json:"enabled"`
	CertFile string `yaml:"cert_file" json:"cert_file"`
	KeyFile  string `yaml:"key_file" json:"key_file"`
}

type AdminServerConfig struct {
	Enabled *bool  `yaml:"enabled" json:"enabled"`
	Listen  string `yaml:"listen" json:"listen"`
}

type WebConfig struct {
	Enabled *bool `yaml:"enabled" json:"enabled"`
}

type RuntimeConfig struct {
	LogLevel   string `yaml:"log_level" json:"log_level"`
	RecentLogs *int   `yaml:"recent_logs" json:"recent_logs"`
}

// NetworkConfig holds global outbound tuning (PRD §11.5). No overall
// request deadline exists so long-lived SSE streams are never cut off.
type NetworkConfig struct {
	DialTimeout           Duration `yaml:"dial_timeout" json:"dial_timeout"`
	TLSHandshakeTimeout   Duration `yaml:"tls_handshake_timeout" json:"tls_handshake_timeout"`
	ResponseHeaderTimeout Duration `yaml:"response_header_timeout" json:"response_header_timeout"`
	IdleConnectionTimeout Duration `yaml:"idle_connection_timeout" json:"idle_connection_timeout"`
}

// TransportConfig declares an outbound proxy link (PRD §5.2).
type TransportConfig struct {
	Name string `yaml:"name" json:"name"`
	Type string `yaml:"type" json:"type"` // "http" | "socks5" ("direct" is built in)
	URL  string `yaml:"url" json:"url"`
}

// Transport type enum values.
const (
	TransportDirect = "direct"
	TransportHTTP   = "http"
	TransportSOCKS5 = "socks5"
)

// Route type enum values: how the data plane handles a matched request.
const (
	RouteTypeProxy  = "proxy"  // reverse-proxy to an http(s) target (default)
	RouteTypeStatic = "static" // serve one local file from target (absolute path)
)

// RouteConfig declares one prefix→backend mapping (PRD §5.1).
//
// Type selects the handler:
//   - "proxy" (default): Target is an absolute http/https URL; traffic is
//     reverse-proxied via Transport.
//   - "static": Target is an absolute filesystem path to a single regular
//     file (not a directory). Every request matching Prefix is served that
//     file. strip_prefix, strip_forward_headers and transport are ignored.
type RouteConfig struct {
	Name    string `yaml:"name" json:"name"`
	Enabled *bool  `yaml:"enabled" json:"enabled"`
	// Type is "proxy" or "static". Empty means "proxy" and is normalized.
	Type   string `yaml:"type" json:"type"`
	Prefix string `yaml:"prefix" json:"prefix"`
	// Target is an absolute http/https URL for type proxy, or an absolute
	// local file path for type static.
	Target string `yaml:"target" json:"target"`
	// StripForwardHeaders removes proxy metadata request headers (Forwarded,
	// Via, X-Forwarded-For/-Host/-Proto) before forwarding, so a fronting
	// reverse proxy never leaks client details to the target. Default true;
	// set false to pass inbound values through unchanged. Ignored for static.
	StripForwardHeaders *bool `yaml:"strip_forward_headers" json:"strip_forward_headers"`
	// StripPrefix removes the matched prefix before joining with the target
	// path. Default true. Ignored for static.
	StripPrefix *bool `yaml:"strip_prefix" json:"strip_prefix"`
	// Transport is the egress link for proxy routes. Default "direct".
	// Ignored for static.
	Transport string `yaml:"transport" json:"transport"`
}

func (r RouteConfig) IsEnabled() bool    { return r.Enabled == nil || *r.Enabled }
func (r RouteConfig) StripsPrefix() bool { return r.StripPrefix == nil || *r.StripPrefix }
func (r RouteConfig) StripsForwardHeaders() bool {
	return r.StripForwardHeaders == nil || *r.StripForwardHeaders
}

// EffectiveType returns the route type after treating empty as proxy.
func (r RouteConfig) EffectiveType() string {
	if r.Type == "" {
		return RouteTypeProxy
	}
	return r.Type
}

// IsStatic reports whether this route serves a local file.
func (r RouteConfig) IsStatic() bool { return r.EffectiveType() == RouteTypeStatic }
func (a AdminServerConfig) IsEnabled() bool { return a.Enabled == nil || *a.Enabled }
func (w WebConfig) IsEnabled() bool         { return w.Enabled == nil || *w.Enabled }

func boolPtr(b bool) *bool { return &b }
func intPtr(i int) *int    { return &i }

// Default returns a fully-populated default configuration (PRD §4.2, §6.2).
func Default() *Config {
	c := &Config{Version: SupportedVersion}
	c.Normalize()
	return c
}

// Normalize fills every unset field with its documented default so that all
// pointer fields are non-nil afterwards. It is idempotent and must be called
// after Parse and before Validate/Build.
func (c *Config) Normalize() {
	if c.Server.Proxy.Listen == "" {
		c.Server.Proxy.Listen = ":8080"
	}
	if c.Server.Admin.Enabled == nil {
		c.Server.Admin.Enabled = boolPtr(true)
	}
	if c.Server.Admin.Listen == "" {
		c.Server.Admin.Listen = "127.0.0.1:9090"
	}
	if c.Server.Web.Enabled == nil {
		c.Server.Web.Enabled = boolPtr(true)
	}
	if c.Runtime.LogLevel == "" {
		c.Runtime.LogLevel = "info"
	}
	if c.Runtime.RecentLogs == nil {
		c.Runtime.RecentLogs = intPtr(1000)
	}
	if c.Network.DialTimeout == 0 {
		c.Network.DialTimeout = Duration(10 * time.Second)
	}
	if c.Network.TLSHandshakeTimeout == 0 {
		c.Network.TLSHandshakeTimeout = Duration(10 * time.Second)
	}
	if c.Network.ResponseHeaderTimeout == 0 {
		c.Network.ResponseHeaderTimeout = Duration(120 * time.Second)
	}
	if c.Network.IdleConnectionTimeout == 0 {
		c.Network.IdleConnectionTimeout = Duration(90 * time.Second)
	}
	for i := range c.Routes {
		if c.Routes[i].Type == "" {
			c.Routes[i].Type = RouteTypeProxy
		}
		if c.Routes[i].Enabled == nil {
			c.Routes[i].Enabled = boolPtr(true)
		}
		if c.Routes[i].StripPrefix == nil {
			c.Routes[i].StripPrefix = boolPtr(true)
		}
		if c.Routes[i].StripForwardHeaders == nil {
			c.Routes[i].StripForwardHeaders = boolPtr(true)
		}
		if c.Routes[i].Transport == "" {
			c.Routes[i].Transport = DirectName
		}
	}
}

// Clone returns a deep copy safe for independent mutation.
func (c *Config) Clone() *Config {
	if c == nil {
		return nil
	}
	out := *c
	if c.Server.Admin.Enabled != nil {
		out.Server.Admin.Enabled = boolPtr(*c.Server.Admin.Enabled)
	}
	if c.Server.Web.Enabled != nil {
		out.Server.Web.Enabled = boolPtr(*c.Server.Web.Enabled)
	}
	if c.Runtime.RecentLogs != nil {
		out.Runtime.RecentLogs = intPtr(*c.Runtime.RecentLogs)
	}
	out.Transports = make([]TransportConfig, len(c.Transports))
	copy(out.Transports, c.Transports)
	out.Routes = make([]RouteConfig, len(c.Routes))
	for i, r := range c.Routes {
		if r.Enabled != nil {
			r.Enabled = boolPtr(*r.Enabled)
		}
		if r.StripPrefix != nil {
			r.StripPrefix = boolPtr(*r.StripPrefix)
		}
		if r.StripForwardHeaders != nil {
			r.StripForwardHeaders = boolPtr(*r.StripForwardHeaders)
		}
		out.Routes[i] = r
	}
	return &out
}
