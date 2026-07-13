package router

import (
	"net/url"
	"strings"
)

// Rewrite maps an incoming request URL onto the route's target (PRD §8).
// Only proxy routes support rewrite; static routes panic if Rewrite is
// called (callers must branch on IsStatic first).
//
// It operates exclusively on escaped path strings: it never calls
// path.Clean and never re-encodes, so percent escapes (%2F, %20, ...) and
// duplicate slashes pass through untouched (PRD §8.3).
//
//	base  = target.EscapedPath() with a trailing "/" trimmed
//	        (target path "/" or "" → base "")
//	rest  = escapedPath minus prefix when StripPrefix
//	        (prefix "/" strips nothing); else the full escapedPath
//	final = base + rest; "" → "/"
//
// The query string (including an empty one forced by a bare "?") is
// preserved verbatim; scheme and host come from the target. The returned
// URL is new and carries no userinfo or fragment; neither reqURL nor the
// route is mutated.
func (r *Route) Rewrite(reqURL *url.URL) *url.URL {
	if r.IsStatic() || r.Target == nil {
		panic("router: Rewrite called on static route " + r.Name)
	}
	base := strings.TrimSuffix(r.Target.EscapedPath(), "/")

	escapedPath := reqURL.EscapedPath()
	rest := escapedPath
	if r.StripPrefix && r.Prefix != "/" && strings.HasPrefix(escapedPath, r.Prefix) {
		rest = escapedPath[len(r.Prefix):]
	}

	final := base + rest
	if final == "" {
		final = "/"
	}

	out := &url.URL{
		Scheme:     r.Target.Scheme,
		Host:       r.Target.Host,
		RawQuery:   reqURL.RawQuery,
		ForceQuery: reqURL.ForceQuery,
	}
	if p, err := url.PathUnescape(final); err == nil {
		out.Path = p
		if p != final {
			out.RawPath = final
		}
	} else {
		// Unreachable for URLs built by net/http or url.Parse; keep the
		// escaped form as-is rather than fail (contract: never re-encode).
		out.Path = final
	}
	return out
}
