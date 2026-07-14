// Package router implements the PipeRouter route table: longest-prefix
// path matching on segment boundaries (or exact-path matching per route)
// and escaped-path rewrite (PRD §7, §8, §23.1–23.3). Static routes serve
// a single local file and do not participate in URL rewrite.
package router

import (
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/kites262/piperouter/internal/config"
)

// Route is one compiled prefix→backend mapping.
//
// For Type == config.RouteTypeProxy, Target is non-nil and TransportName
// names the egress pool entry. For Type == config.RouteTypeStatic, File is
// the absolute path of the single file to serve and Target is nil.
type Route struct {
	Name                string
	Type                string // config.RouteTypeProxy | config.RouteTypeStatic
	Prefix              string // validated: "/" or no trailing slash
	Exact               bool   // match only path == Prefix (config "match: exact")
	Target              *url.URL
	File                string // absolute path; static only
	StripPrefix         bool
	StripForwardHeaders bool
	TransportName       string
}

// IsStatic reports whether this route serves a local file.
func (r *Route) IsStatic() bool { return r != nil && r.Type == config.RouteTypeStatic }

// Table is an immutable set of enabled routes supporting longest-prefix
// matching. Build a new Table on every config swap; never mutate one.
type Table struct {
	byLength []*Route // longest prefix first (ties broken by prefix), for Match
	byName   []*Route // sorted by name, for Routes
}

// BuildTable compiles the enabled routes of a validated configuration.
// Disabled routes are skipped. The input is assumed to be pre-validated;
// an unparsable proxy target is a programming error and returns an error.
//
// baseDir is the configuration file's directory (config.ConfigBaseDir).
// Static targets are resolved to an absolute path here exactly once per
// config swap and stored on Route.File — the request hot path never joins
// or Abs's paths. Pass "" only when all static targets are already absolute.
func BuildTable(routes []config.RouteConfig, baseDir string) (*Table, error) {
	t := &Table{}
	for _, rc := range routes {
		if !rc.IsEnabled() {
			continue
		}
		r := &Route{
			Name:                rc.Name,
			Type:                rc.EffectiveType(),
			Prefix:              rc.Prefix,
			Exact:               rc.MatchesExactly(),
			StripPrefix:         rc.StripsPrefix(),
			StripForwardHeaders: rc.StripsForwardHeaders(),
			TransportName:       rc.TransportName(),
		}
		switch r.Type {
		case config.RouteTypeStatic:
			if rc.Static == nil {
				return nil, fmt.Errorf("route %q: missing static options", rc.Name)
			}
			// Resolve once at build time; serveStatic only reads r.File.
			abs, err := config.ResolveStaticFilePath(rc.Static.File, baseDir)
			if err != nil {
				return nil, fmt.Errorf("route %q: %w", rc.Name, err)
			}
			r.File = abs
		default:
			if rc.Proxy == nil {
				return nil, fmt.Errorf("route %q: missing proxy options", rc.Name)
			}
			target, err := url.Parse(rc.Proxy.Target)
			if err != nil {
				return nil, fmt.Errorf("route %q: invalid target: %w", rc.Name, err)
			}
			r.Target = target
		}
		t.byLength = append(t.byLength, r)
		t.byName = append(t.byName, r)
	}
	sort.Slice(t.byLength, func(i, j int) bool {
		a, b := t.byLength[i], t.byLength[j]
		if len(a.Prefix) != len(b.Prefix) {
			return len(a.Prefix) > len(b.Prefix)
		}
		return a.Prefix < b.Prefix
	})
	sort.Slice(t.byName, func(i, j int) bool {
		return t.byName[i].Name < t.byName[j].Name
	})
	return t, nil
}

// Match returns the route with the longest prefix matching escapedPath
// on a path-segment boundary, or nil if none matches (PRD §7.2, §7.3).
// The input must be r.URL.EscapedPath(); matching never decodes.
//
// A prefix route matches iff path == prefix or path starts with prefix+"/";
// the root prefix "/" matches every path. An exact route (match: exact)
// matches iff path == prefix — paths below it fall through to the remaining
// routes. Declaration order is irrelevant.
func (t *Table) Match(escapedPath string) *Route {
	for _, r := range t.byLength {
		if r.Exact {
			if escapedPath == r.Prefix {
				return r
			}
			continue
		}
		if r.Prefix == "/" {
			return r
		}
		if escapedPath == r.Prefix || strings.HasPrefix(escapedPath, r.Prefix+"/") {
			return r
		}
	}
	return nil
}

// Routes returns the enabled routes in stable order (sorted by name).
// The returned slice is a copy; the routes themselves are shared.
func (t *Table) Routes() []*Route {
	out := make([]*Route, len(t.byName))
	copy(out, t.byName)
	return out
}

// Len reports the number of enabled routes in the table.
func (t *Table) Len() int { return len(t.byName) }
