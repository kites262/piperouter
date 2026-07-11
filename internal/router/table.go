// Package router implements the PipeRouter route table: longest-prefix
// path matching on segment boundaries and escaped-path rewrite
// (PRD §7, §8, §23.1–23.3).
package router

import (
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/kites262/piperouter/internal/config"
)

// Route is one compiled prefix→target mapping.
type Route struct {
	Name          string
	Prefix        string   // validated: "/" or no trailing slash
	Target        *url.URL // absolute, no query/fragment/userinfo
	StripPrefix   bool
	TransportName string
}

// Table is an immutable set of enabled routes supporting longest-prefix
// matching. Build a new Table on every config swap; never mutate one.
type Table struct {
	byLength []*Route // longest prefix first (ties broken by prefix), for Match
	byName   []*Route // sorted by name, for Routes
}

// BuildTable compiles the enabled routes of a validated configuration.
// Disabled routes are skipped. The input is assumed to be pre-validated;
// an unparsable target is a programming error and returns an error.
func BuildTable(routes []config.RouteConfig) (*Table, error) {
	t := &Table{}
	for _, rc := range routes {
		if !rc.IsEnabled() {
			continue
		}
		target, err := url.Parse(rc.Target)
		if err != nil {
			return nil, fmt.Errorf("route %q: invalid target: %w", rc.Name, err)
		}
		r := &Route{
			Name:          rc.Name,
			Prefix:        rc.Prefix,
			Target:        target,
			StripPrefix:   rc.StripsPrefix(),
			TransportName: rc.Transport,
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
// A route matches iff path == prefix or path starts with prefix+"/".
// The root prefix "/" matches every path. Declaration order is irrelevant.
func (t *Table) Match(escapedPath string) *Route {
	for _, r := range t.byLength {
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
