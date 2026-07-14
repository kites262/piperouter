package config

import (
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"
)

// ValidationError aggregates every problem found in a configuration so a
// user can fix them all in one pass (PRD §6.3).
type ValidationError struct{ Issues []string }

func (e *ValidationError) Error() string {
	return "invalid configuration: " + strings.Join(e.Issues, "; ")
}

var nameRE = regexp.MustCompile(NamePattern)

// Validate checks c against the PRD §6.3/§6.4 rules and returns a
// *ValidationError listing ALL problems found, or nil when the configuration
// is valid. It assumes Normalize has already been called on c.
//
// baseDir is the absolute directory of the configuration file (see
// ConfigBaseDir). It is used only to resolve relative static-route targets
// at validate time; the YAML is never rewritten. Pass "" when no config
// path is available — absolute static targets still work, relative ones
// are rejected. This path is never consulted on the request hot path.
func Validate(c *Config, baseDir string) error {
	var issues []string
	add := func(format string, args ...any) {
		issues = append(issues, fmt.Sprintf(format, args...))
	}

	if c.Version != SupportedVersion {
		add("unsupported version %q (supported version is %q)", c.Version, SupportedVersion)
	}

	if c.Server.Proxy.TLS.Enabled {
		if c.Server.Proxy.TLS.CertFile == "" {
			add("server.proxy.tls: enabled but cert_file is empty")
		}
		if c.Server.Proxy.TLS.KeyFile == "" {
			add("server.proxy.tls: enabled but key_file is empty")
		}
	}

	switch c.Runtime.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		add("runtime.log_level %q must be one of debug, info, warn, error", c.Runtime.LogLevel)
	}
	if c.Runtime.RecentLogs != nil && *c.Runtime.RecentLogs < 0 {
		add("runtime.recent_logs must be >= 0, got %d", *c.Runtime.RecentLogs)
	}

	// Negative network timeouts pass through to http.Transport as
	// already-expired deadlines and silently break all forwarding
	// (dial/TLS/response) or defeat connection reuse (PRD §11.5).
	for _, d := range []struct {
		field string
		value Duration
	}{
		{"network.dial_timeout", c.Network.DialTimeout},
		{"network.tls_handshake_timeout", c.Network.TLSHandshakeTimeout},
		{"network.response_header_timeout", c.Network.ResponseHeaderTimeout},
		{"network.idle_connection_timeout", c.Network.IdleConnectionTimeout},
	} {
		if d.value < 0 {
			add("%s must not be negative, got %s", d.field, d.value)
		}
	}

	// The built-in direct transport is always a valid reference (PRD §5.2).
	knownTransports := map[string]bool{DirectName: true}
	seenTransportNames := map[string]bool{}
	for i, t := range c.Transports {
		ref := fmt.Sprintf("transports[%d] (name %q)", i, t.Name)

		if t.Name == DirectName {
			add("%s: %q is a reserved built-in transport name and cannot be declared", ref, DirectName)
		} else if !nameRE.MatchString(t.Name) {
			add("%s: name must match %s", ref, NamePattern)
		}
		if seenTransportNames[t.Name] {
			add("%s: duplicate transport name", ref)
		}
		seenTransportNames[t.Name] = true
		knownTransports[t.Name] = true

		wantScheme := ""
		switch t.Type {
		case TransportHTTP:
			wantScheme = "http"
		case TransportSOCKS5:
			wantScheme = "socks5"
		default:
			add("%s: type %q is not supported (must be %q or %q)", ref, t.Type, TransportHTTP, TransportSOCKS5)
		}

		// Never echo the raw proxy URL into issues (it could carry
		// credentials, which must never be logged or exposed).
		if t.URL == "" {
			add("%s: proxy url is required", ref)
			continue
		}
		u, err := url.Parse(t.URL)
		if err != nil {
			add("%s: proxy url is not a valid URL", ref)
			continue
		}
		if wantScheme != "" && u.Scheme != wantScheme {
			add("%s: proxy url scheme must be %q for type %q, got %q", ref, wantScheme, t.Type, u.Scheme)
		}
		if u.User != nil {
			add("%s: proxy url must not contain userinfo", ref)
		}
		if u.Host == "" {
			add("%s: proxy url host must not be empty", ref)
		}
	}

	seenRouteNames := map[string]bool{}
	seenPrefixes := map[string]bool{}
	for i, r := range c.Routes {
		ref := fmt.Sprintf("routes[%d] (name %q)", i, r.Name)

		if !nameRE.MatchString(r.Name) {
			add("%s: name must match %s", ref, NamePattern)
		}
		if seenRouteNames[r.Name] {
			add("%s: duplicate route name", ref)
		}
		seenRouteNames[r.Name] = true

		for _, msg := range prefixIssues(r.Prefix) {
			add("%s: %s", ref, msg)
		}
		if seenPrefixes[r.Prefix] {
			add("%s: duplicate prefix %q", ref, r.Prefix)
		}
		seenPrefixes[r.Prefix] = true

		switch r.EffectiveMatch() {
		case MatchPrefix, MatchExact:
		default:
			add("%s: match %q is not supported (must be %q or %q)", ref, r.Match, MatchPrefix, MatchExact)
		}

		switch r.EffectiveType() {
		case RouteTypeProxy:
			if r.Static != nil {
				add("%s: static options on a proxy route", ref)
			}
			if r.Proxy == nil {
				add("%s: missing options (proxy routes need options.target)", ref)
				break
			}
			for _, msg := range targetIssues(r.Proxy.Target) {
				add("%s: %s", ref, msg)
			}
			if r.Proxy.Transport != "" && !knownTransports[r.Proxy.Transport] {
				add("%s: references unknown transport %q", ref, r.Proxy.Transport)
			}
		case RouteTypeStatic:
			if r.Proxy != nil {
				add("%s: proxy options on a static route", ref)
			}
			if r.Static == nil {
				add("%s: missing options (static routes need options.file)", ref)
				break
			}
			for _, msg := range staticFileIssues(r.Static.File, baseDir) {
				add("%s: %s", ref, msg)
			}
		default:
			add("%s: type %q is not supported (must be %q or %q)", ref, r.Type, RouteTypeProxy, RouteTypeStatic)
		}
	}

	if len(issues) == 0 {
		return nil
	}
	return &ValidationError{Issues: issues}
}

// prefixIssues checks a route prefix against PRD §6.4:
// must start with "/", no "?"/"#", no ".." segments, no empty segments
// ("//"), and a non-root prefix must not end with "/".
func prefixIssues(prefix string) []string {
	if !strings.HasPrefix(prefix, "/") {
		return []string{`prefix must start with "/"`}
	}
	var issues []string
	if strings.ContainsAny(prefix, "?#") {
		issues = append(issues, `prefix must not contain "?" or "#"`)
	}
	if strings.Contains(prefix, "//") {
		issues = append(issues, `prefix must not contain empty segments ("//")`)
	}
	for _, seg := range strings.Split(prefix, "/") {
		if seg == ".." {
			issues = append(issues, `prefix must not contain ".." segments`)
			break
		}
	}
	if prefix != "/" && strings.HasSuffix(prefix, "/") {
		issues = append(issues, `non-root prefix must not end with "/"`)
	}
	return issues
}

// targetIssues checks a proxy-route target: absolute URL, scheme http/https,
// no userinfo, no query, no fragment, non-empty host.
func targetIssues(target string) []string {
	if target == "" {
		return []string{"target is required"}
	}
	u, err := url.Parse(target)
	if err != nil {
		return []string{"target is not a valid URL"}
	}
	var issues []string
	if !u.IsAbs() {
		issues = append(issues, "target must be an absolute URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		issues = append(issues, fmt.Sprintf("target scheme must be http or https, got %q", u.Scheme))
	}
	if u.User != nil {
		issues = append(issues, "target must not contain userinfo")
	}
	if u.RawQuery != "" || u.ForceQuery {
		issues = append(issues, "target must not contain a query")
	}
	if u.Fragment != "" {
		issues = append(issues, "target must not contain a fragment")
	}
	if u.Host == "" {
		issues = append(issues, "target host must not be empty")
	}
	return issues
}

// staticFileIssues checks a static route's options.file: filesystem path to
// a single regular file (directories are not supported). Relative paths are
// resolved against baseDir (config file directory) via ResolveStaticFilePath.
// When the resolved path already exists it must be a regular file; a missing
// path is allowed so deploys can place the file after configuration.
//
// The resolved absolute path is NOT written back into the config — only
// checked here and re-resolved once in router.BuildTable into Route.File.
func staticFileIssues(file, baseDir string) []string {
	abs, err := ResolveStaticFilePath(file, baseDir)
	if err != nil {
		return []string{err.Error()}
	}
	var issues []string
	fi, err := os.Stat(abs)
	if err != nil {
		// Missing file is OK at validate time; ServeFile will 404 at runtime.
		if !os.IsNotExist(err) {
			issues = append(issues, fmt.Sprintf("static file is not accessible: %v", err))
		}
		return issues
	}
	if fi.IsDir() {
		issues = append(issues, "static file must be a file, not a directory")
	} else if !fi.Mode().IsRegular() {
		issues = append(issues, "static file must be a regular file")
	}
	return issues
}
