// Package config defines the PipeRouter configuration schema, strict
// parsing/validation, atomic persistence and file watching.
//
// The YAML configuration file is the single source of truth (PRD §6).
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"gopkg.in/yaml.v3"
)

// DirectName is the reserved name of the built-in direct transport.
// It always exists, cannot be declared, overridden or deleted (PRD §5.2).
const DirectName = "direct"

// SupportedVersion is the configuration schema version this binary accepts.
// Schema versions track the application release series that introduced them
// ("v0.3", "v0.4", ...). There is NO automatic migration: a file written for
// another schema version is rejected with a hint to migrate by hand
// (docs/configuration.md describes the mapping).
const SupportedVersion Version = "v0.3"

// Version is the configuration schema version scalar. It decodes from any
// YAML/JSON scalar (string or number) so that a legacy "version: 1" file
// fails the version CHECK with a migration hint instead of dying earlier
// with an opaque YAML type error.
type Version string

func (v *Version) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind != yaml.ScalarNode {
		return fmt.Errorf("version must be a scalar like %q", SupportedVersion)
	}
	*v = Version(value.Value)
	return nil
}

func (v *Version) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		*v = Version(s)
		return nil
	}
	// Legacy numeric version (v1 wrote an integer).
	*v = Version(bytes.TrimSpace(data))
	return nil
}

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
	Version    Version           `yaml:"version" json:"version"`
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

// Route match modes: how a route's Prefix is compared to request paths.
const (
	MatchPrefix = "prefix" // longest-prefix on path-segment boundaries (default)
	MatchExact  = "exact"  // only a request path equal to Prefix matches
)

// RouteConfig declares one prefix→handler mapping (PRD §5.1).
//
// The wire shape (YAML and JSON, schema v0.3) is a tagged union: the shared
// matching fields sit at the top level and everything specific to the
// handler type lives in an options block whose schema is selected by type.
// One routes[] entry looks like:
//
//	name: api
//	type: proxy            # selects the options schema; static uses options.file
//	prefix: /v1
//	match: prefix
//	options:
//	  target: https://api.example.com/v1
//	  transport: mihomo
//
// In memory, exactly one of Proxy/Static is non-nil for a valid config,
// matching EffectiveType(). The custom (un)marshalers below map the options
// block to that struct; unknown fields inside options are rejected as
// strictly as top-level ones (PRD §6.3).
type RouteConfig struct {
	Name    string
	Enabled *bool
	// Type is "proxy" or "static". Empty means "proxy" and is normalized.
	Type   string
	Prefix string
	// Match is "prefix" (default: longest-prefix on path-segment boundaries)
	// or "exact" (only a request path equal to Prefix matches — nothing
	// below it). Empty is normalized to "prefix".
	Match string

	// Proxy holds the options block of a proxy route; nil otherwise.
	Proxy *ProxyOptions
	// Static holds the options block of a static route; nil otherwise.
	Static *StaticOptions
}

// ProxyOptions is the options block of a proxy route: reverse-proxy the
// matched request to Target over Transport.
type ProxyOptions struct {
	// Target is an absolute http/https URL (no userinfo, query or fragment).
	Target string `yaml:"target" json:"target"`
	// Transport is the egress link. Default "direct".
	Transport string `yaml:"transport" json:"transport"`
	// StripPrefix removes the matched prefix before joining with the target
	// path. Default true.
	StripPrefix *bool `yaml:"strip_prefix" json:"strip_prefix"`
	// StripForwardHeaders removes proxy metadata request headers (Forwarded,
	// Via, X-Forwarded-For/-Host/-Proto) before forwarding, so a fronting
	// reverse proxy never leaks client details to the target. Default true;
	// set false to pass inbound values through unchanged.
	StripForwardHeaders *bool `yaml:"strip_forward_headers" json:"strip_forward_headers"`
}

// StaticOptions is the options block of a static route: answer every
// matching request with the single local file at File.
type StaticOptions struct {
	// File is the filesystem path of a regular file — absolute, or relative
	// to the configuration file's directory. Not a directory, not a URL.
	File string `yaml:"file" json:"file"`
}

// routeConfigWire is the on-the-wire shape of RouteConfig with the options
// block still raw; the typed decode happens per EffectiveType.
type routeConfigWire struct {
	Name    string `yaml:"name" json:"name"`
	Enabled *bool  `yaml:"enabled" json:"enabled"`
	Type    string `yaml:"type" json:"type"`
	Prefix  string `yaml:"prefix" json:"prefix"`
	Match   string `yaml:"match" json:"match"`
}

// routeConfigMarshal is the outbound wire shape: options carries whichever
// union member is set. Field order defines the canonical YAML output.
type routeConfigMarshal struct {
	Name    string `yaml:"name" json:"name"`
	Enabled *bool  `yaml:"enabled" json:"enabled"`
	Type    string `yaml:"type" json:"type"`
	Prefix  string `yaml:"prefix" json:"prefix"`
	Match   string `yaml:"match" json:"match"`
	Options any    `yaml:"options,omitempty" json:"options,omitempty"`
}

func (r RouteConfig) wireOut() routeConfigMarshal {
	out := routeConfigMarshal{
		Name:    r.Name,
		Enabled: r.Enabled,
		Type:    r.Type,
		Prefix:  r.Prefix,
		Match:   r.Match,
	}
	switch {
	case r.Proxy != nil:
		out.Options = r.Proxy
	case r.Static != nil:
		out.Options = r.Static
	}
	return out
}

// decodeOptions populates the union member selected by wire.Type from the
// raw options block via decode (strict; unknown option fields error).
// An unsupported type leaves both members nil — Validate reports it.
func (r *RouteConfig) decodeOptions(hasOptions bool, decode func(any) error) error {
	r.Proxy, r.Static = nil, nil
	switch r.EffectiveType() {
	case RouteTypeProxy:
		opts := &ProxyOptions{}
		if hasOptions {
			if err := decode(opts); err != nil {
				return fmt.Errorf("route %q: invalid proxy options: %w", r.Name, err)
			}
		}
		r.Proxy = opts
	case RouteTypeStatic:
		opts := &StaticOptions{}
		if hasOptions {
			if err := decode(opts); err != nil {
				return fmt.Errorf("route %q: invalid static options: %w", r.Name, err)
			}
		}
		r.Static = opts
	}
	return nil
}

func (r *RouteConfig) UnmarshalYAML(value *yaml.Node) error {
	var wire struct {
		routeConfigWire `yaml:",inline"`
		Options         yaml.Node `yaml:"options"`
	}
	if err := strictYAMLNodeDecode(value, &wire); err != nil {
		return err
	}
	r.Name = wire.Name
	r.Enabled = wire.Enabled
	r.Type = wire.Type
	r.Prefix = wire.Prefix
	r.Match = wire.Match
	hasOptions := !wire.Options.IsZero() && wire.Options.Tag != "!!null"
	return r.decodeOptions(hasOptions, func(out any) error {
		return strictYAMLNodeDecode(&wire.Options, out)
	})
}

func (r RouteConfig) MarshalYAML() (any, error) { return r.wireOut(), nil }

func (r *RouteConfig) UnmarshalJSON(data []byte) error {
	var wire struct {
		routeConfigWire
		Options json.RawMessage `json:"options"`
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields() // same strictness as the YAML loader
	if err := dec.Decode(&wire); err != nil {
		return err
	}
	r.Name = wire.Name
	r.Enabled = wire.Enabled
	r.Type = wire.Type
	r.Prefix = wire.Prefix
	r.Match = wire.Match
	hasOptions := len(wire.Options) > 0 && !bytes.Equal(bytes.TrimSpace(wire.Options), []byte("null"))
	return r.decodeOptions(hasOptions, func(out any) error {
		d := json.NewDecoder(bytes.NewReader(wire.Options))
		d.DisallowUnknownFields()
		return d.Decode(out)
	})
}

func (r RouteConfig) MarshalJSON() ([]byte, error) { return json.Marshal(r.wireOut()) }

// strictYAMLNodeDecode decodes node into out rejecting unknown fields, the
// same strictness Parse applies to the whole document. yaml.Node.Decode has
// no KnownFields switch, so the node is re-encoded and strictly re-decoded.
func strictYAMLNodeDecode(node *yaml.Node, out any) error {
	raw, err := yaml.Marshal(node)
	if err != nil {
		return err
	}
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(out); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func (r RouteConfig) IsEnabled() bool { return r.Enabled == nil || *r.Enabled }

// StripsPrefix reports the effective strip_prefix of a proxy route
// (default true). Static routes never strip.
func (r RouteConfig) StripsPrefix() bool {
	return r.Proxy == nil || r.Proxy.StripPrefix == nil || *r.Proxy.StripPrefix
}

// StripsForwardHeaders reports the effective strip_forward_headers of a
// proxy route (default true).
func (r RouteConfig) StripsForwardHeaders() bool {
	return r.Proxy == nil || r.Proxy.StripForwardHeaders == nil || *r.Proxy.StripForwardHeaders
}

// TransportName returns the proxy route's transport ("" for static routes).
func (r RouteConfig) TransportName() string {
	if r.Proxy == nil {
		return ""
	}
	return r.Proxy.Transport
}

// EffectiveType returns the route type after treating empty as proxy.
func (r RouteConfig) EffectiveType() string {
	if r.Type == "" {
		return RouteTypeProxy
	}
	return r.Type
}

// EffectiveMatch returns the match mode after treating empty as prefix.
func (r RouteConfig) EffectiveMatch() string {
	if r.Match == "" {
		return MatchPrefix
	}
	return r.Match
}

// MatchesExactly reports whether only a path equal to Prefix matches.
func (r RouteConfig) MatchesExactly() bool { return r.EffectiveMatch() == MatchExact }

// IsStatic reports whether this route serves a local file.
func (r RouteConfig) IsStatic() bool        { return r.EffectiveType() == RouteTypeStatic }
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
		rt := &c.Routes[i]
		if rt.Type == "" {
			rt.Type = RouteTypeProxy
		}
		if rt.Match == "" {
			rt.Match = MatchPrefix
		}
		if rt.Enabled == nil {
			rt.Enabled = boolPtr(true)
		}
		// Materialize the matching options block when none is present (a
		// mismatched block is left alone for Validate to report), then fill
		// its defaults.
		switch rt.Type {
		case RouteTypeProxy:
			if rt.Proxy == nil && rt.Static == nil {
				rt.Proxy = &ProxyOptions{}
			}
		case RouteTypeStatic:
			if rt.Static == nil && rt.Proxy == nil {
				rt.Static = &StaticOptions{}
			}
		}
		if p := rt.Proxy; p != nil {
			if p.StripPrefix == nil {
				p.StripPrefix = boolPtr(true)
			}
			if p.StripForwardHeaders == nil {
				p.StripForwardHeaders = boolPtr(true)
			}
			if p.Transport == "" {
				p.Transport = DirectName
			}
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
		if r.Proxy != nil {
			p := *r.Proxy
			if p.StripPrefix != nil {
				p.StripPrefix = boolPtr(*p.StripPrefix)
			}
			if p.StripForwardHeaders != nil {
				p.StripForwardHeaders = boolPtr(*p.StripForwardHeaders)
			}
			r.Proxy = &p
		}
		if r.Static != nil {
			st := *r.Static
			r.Static = &st
		}
		out.Routes[i] = r
	}
	return &out
}
