package router

import (
	"net/url"
	"testing"

	"github.com/kites262/piperouter/internal/config"
)

// TestRewrite drives Match+Rewrite exactly the way the proxy does: the
// request URL is built with url.Parse (mirroring the server parsing the
// request line), matched via Table.Match(u.EscapedPath()) and rewritten.
func TestRewrite(t *testing.T) {
	tests := []struct {
		name        string
		prefix      string
		target      string
		strip       *bool // nil → default (true)
		reqURL      string
		want        string // full rewritten URL via String()
		wantPath    string
		wantRawPath string
	}{
		{
			// PRD §23.3 acceptance case.
			name:     "acceptance strip with query",
			prefix:   "/openai",
			target:   "https://api.example.com/v1",
			reqURL:   "http://gw.local/openai/chat?stream=true",
			want:     "https://api.example.com/v1/chat?stream=true",
			wantPath: "/v1/chat",
		},
		{
			// PRD §8.1: stripping to an empty rest yields exactly the base.
			name:     "strip empty rest",
			prefix:   "/openai",
			target:   "https://api.example.com/v1",
			reqURL:   "http://gw.local/openai",
			want:     "https://api.example.com/v1",
			wantPath: "/v1",
		},
		{
			name:     "strip trailing slash kept",
			prefix:   "/openai",
			target:   "https://api.example.com/v1",
			reqURL:   "http://gw.local/openai/",
			want:     "https://api.example.com/v1/",
			wantPath: "/v1/",
		},
		{
			// PRD §8.2.
			name:     "strip_prefix false keeps full path",
			prefix:   "/openai",
			target:   "https://example.com/v1",
			strip:    boolp(false),
			reqURL:   "http://gw.local/openai/models",
			want:     "https://example.com/v1/openai/models",
			wantPath: "/v1/openai/models",
		},
		{
			name:     "strip_prefix false with pathless target",
			prefix:   "/openai",
			target:   "https://example.com",
			strip:    boolp(false),
			reqURL:   "http://gw.local/openai",
			want:     "https://example.com/openai",
			wantPath: "/openai",
		},
		{
			// PRD §8.3: %2F must survive end-to-end in RawPath.
			name:        "encoded slash preserved",
			prefix:      "/openai",
			target:      "https://api.example.com/v1",
			reqURL:      "http://gw.local/openai/a%2Fb",
			want:        "https://api.example.com/v1/a%2Fb",
			wantPath:    "/v1/a/b",
			wantRawPath: "/v1/a%2Fb",
		},
		{
			// PRD §8.3: duplicate slashes pass through untouched.
			name:     "duplicate slashes preserved",
			prefix:   "/openai",
			target:   "https://api.example.com/v1",
			reqURL:   "http://gw.local/openai//x",
			want:     "https://api.example.com/v1//x",
			wantPath: "/v1//x",
		},
		{
			name:        "encoded space preserved",
			prefix:      "/openai",
			target:      "https://api.example.com/v1",
			reqURL:      "http://gw.local/openai/a%20b",
			want:        "https://api.example.com/v1/a%20b",
			wantPath:    "/v1/a b",
			wantRawPath: "/v1/a%20b",
		},
		{
			name:        "mixed escapes and slashes with encoded query",
			prefix:      "/openai",
			target:      "https://api.example.com/v1",
			reqURL:      "http://gw.local/openai/a%2Fb%20c//d?q=%2F",
			want:        "https://api.example.com/v1/a%2Fb%20c//d?q=%2F",
			wantPath:    "/v1/a/b c//d",
			wantRawPath: "/v1/a%2Fb%20c//d",
		},
		{
			name:     "target without path",
			prefix:   "/openai",
			target:   "https://example.com",
			reqURL:   "http://gw.local/openai/models",
			want:     "https://example.com/models",
			wantPath: "/models",
		},
		{
			name:     "target with bare root path",
			prefix:   "/openai",
			target:   "https://example.com/",
			reqURL:   "http://gw.local/openai/models",
			want:     "https://example.com/models",
			wantPath: "/models",
		},
		{
			// final == "" → "/" (never an illegal empty path, PRD §8.1).
			name:     "empty final becomes root",
			prefix:   "/openai",
			target:   "https://example.com",
			reqURL:   "http://gw.local/openai",
			want:     "https://example.com/",
			wantPath: "/",
		},
		{
			name:     "query with empty value preserved",
			prefix:   "/openai",
			target:   "https://api.example.com/v1",
			reqURL:   "http://gw.local/openai/x?stream=",
			want:     "https://api.example.com/v1/x?stream=",
			wantPath: "/v1/x",
		},
		{
			// A bare "?" sets ForceQuery; it must survive verbatim.
			name:     "force query preserved",
			prefix:   "/openai",
			target:   "https://api.example.com/v1",
			reqURL:   "http://gw.local/openai/x?",
			want:     "https://api.example.com/v1/x?",
			wantPath: "/v1/x",
		},
		{
			// PRD §8.3: query parameter order stays verbatim.
			name:     "query order verbatim",
			prefix:   "/openai",
			target:   "https://api.example.com/v1",
			reqURL:   "http://gw.local/openai/x?b=2&a=1&b=1",
			want:     "https://api.example.com/v1/x?b=2&a=1&b=1",
			wantPath: "/v1/x",
		},
		{
			// Root prefix strips nothing even with strip_prefix=true.
			name:     "root prefix strip strips nothing",
			prefix:   "/",
			target:   "https://backend.local/base",
			reqURL:   "http://gw.local/anything/x",
			want:     "https://backend.local/base/anything/x",
			wantPath: "/base/anything/x",
		},
		{
			name:     "root prefix root request",
			prefix:   "/",
			target:   "https://backend.local",
			reqURL:   "http://gw.local/",
			want:     "https://backend.local/",
			wantPath: "/",
		},
		{
			name:     "root prefix no strip",
			prefix:   "/",
			target:   "https://backend.local/base",
			strip:    boolp(false),
			reqURL:   "http://gw.local/x",
			want:     "https://backend.local/base/x",
			wantPath: "/base/x",
		},
		{
			name:     "target port preserved",
			prefix:   "/openai",
			target:   "https://api.example.com:8443/v1",
			reqURL:   "http://gw.local/openai/x",
			want:     "https://api.example.com:8443/v1/x",
			wantPath: "/v1/x",
		},
		{
			// Escapes in the TARGET base path must also pass through.
			name:        "target path with escapes",
			prefix:      "/openai",
			target:      "https://example.com/base%20dir",
			reqURL:      "http://gw.local/openai/x",
			want:        "https://example.com/base%20dir/x",
			wantPath:    "/base dir/x",
			wantRawPath: "/base%20dir/x",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tbl := mustTable(t, config.RouteConfig{
				Name:        "r",
				Prefix:      tc.prefix,
				Target:      tc.target,
				StripPrefix: tc.strip,
				Transport:   config.DirectName,
			})
			reqURL, err := url.Parse(tc.reqURL)
			if err != nil {
				t.Fatalf("url.Parse(%q): %v", tc.reqURL, err)
			}
			r := tbl.Match(reqURL.EscapedPath())
			if r == nil {
				t.Fatalf("Match(%q) = nil, want route", reqURL.EscapedPath())
			}

			before := *reqURL
			res := r.Rewrite(reqURL)

			if res == reqURL {
				t.Fatal("Rewrite returned the input URL, want a new URL")
			}
			if *reqURL != before {
				t.Errorf("Rewrite mutated input URL: before %+v, after %+v", before, *reqURL)
			}
			if got := res.String(); got != tc.want {
				t.Errorf("String() = %q, want %q", got, tc.want)
			}
			if res.Path != tc.wantPath {
				t.Errorf("Path = %q, want %q", res.Path, tc.wantPath)
			}
			if res.RawPath != tc.wantRawPath {
				t.Errorf("RawPath = %q, want %q", res.RawPath, tc.wantRawPath)
			}
			wantEscaped := tc.wantRawPath
			if wantEscaped == "" {
				wantEscaped = tc.wantPath
			}
			if got := res.EscapedPath(); got != wantEscaped {
				t.Errorf("EscapedPath() = %q, want %q", got, wantEscaped)
			}
			if res.Scheme != r.Target.Scheme || res.Host != r.Target.Host {
				t.Errorf("scheme/host = %q/%q, want %q/%q",
					res.Scheme, res.Host, r.Target.Scheme, r.Target.Host)
			}
			if res.RawQuery != reqURL.RawQuery {
				t.Errorf("RawQuery = %q, want %q", res.RawQuery, reqURL.RawQuery)
			}
			if res.ForceQuery != reqURL.ForceQuery {
				t.Errorf("ForceQuery = %v, want %v", res.ForceQuery, reqURL.ForceQuery)
			}
			if res.User != nil {
				t.Errorf("User = %v, want nil", res.User)
			}
			if res.Fragment != "" || res.RawFragment != "" {
				t.Errorf("fragment = %q/%q, want empty", res.Fragment, res.RawFragment)
			}
		})
	}
}

// Rewrite must not mutate the Route (or its Target URL) either.
func TestRewriteDoesNotMutateRoute(t *testing.T) {
	tbl := mustTable(t, route("openai", "/openai", "https://api.example.com/v1/"))
	r := tbl.Match("/openai/x")
	if r == nil {
		t.Fatal("Match(/openai/x) = nil")
	}
	targetBefore := *r.Target
	routeBefore := Route{
		Name:          r.Name,
		Prefix:        r.Prefix,
		StripPrefix:   r.StripPrefix,
		TransportName: r.TransportName,
	}

	u, err := url.Parse("http://gw.local/openai/x?q=1")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ { // repeated rewrites must stay stable
		res := r.Rewrite(u)
		if got, want := res.String(), "https://api.example.com/v1/x?q=1"; got != want {
			t.Fatalf("rewrite %d: String() = %q, want %q", i, got, want)
		}
	}

	if *r.Target != targetBefore {
		t.Errorf("Rewrite mutated route target: %+v → %+v", targetBefore, *r.Target)
	}
	after := Route{
		Name:          r.Name,
		Prefix:        r.Prefix,
		StripPrefix:   r.StripPrefix,
		TransportName: r.TransportName,
	}
	if after != routeBefore {
		t.Errorf("Rewrite mutated route: %+v → %+v", routeBefore, after)
	}
}

// The escaped request path drives the rewrite: a request whose DECODED
// path contains the prefix boundary but whose escaped path does not must
// keep its escapes (matching happened on the escaped form upstream).
func TestRewriteUsesEscapedPath(t *testing.T) {
	tbl := mustTable(t, route("api", "/api", "https://up.example/base"))
	u, err := url.Parse("http://gw.local/api/x%2Fy%2Fz")
	if err != nil {
		t.Fatal(err)
	}
	r := tbl.Match(u.EscapedPath())
	if r == nil {
		t.Fatal("Match = nil")
	}
	res := r.Rewrite(u)
	if got, want := res.EscapedPath(), "/base/x%2Fy%2Fz"; got != want {
		t.Errorf("EscapedPath() = %q, want %q", got, want)
	}
	if got, want := res.Path, "/base/x/y/z"; got != want {
		t.Errorf("Path = %q, want %q", got, want)
	}
}
