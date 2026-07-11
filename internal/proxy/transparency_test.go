package proxy

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// recordedRequest is what the upstream observed for one request.
type recordedRequest struct {
	Method     string
	RequestURI string
	Host       string
	Header     http.Header
	Body       string
}

// recordingUpstream captures every request and echoes a configurable
// response.
type recordingUpstream struct {
	mu   sync.Mutex
	reqs []recordedRequest
}

func (u *recordingUpstream) last(t *testing.T) recordedRequest {
	t.Helper()
	u.mu.Lock()
	defer u.mu.Unlock()
	if len(u.reqs) == 0 {
		t.Fatal("upstream saw no requests")
	}
	return u.reqs[len(u.reqs)-1]
}

func (u *recordingUpstream) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		u.mu.Lock()
		u.reqs = append(u.reqs, recordedRequest{
			Method:     r.Method,
			RequestURI: r.RequestURI,
			Host:       r.Host,
			Header:     r.Header.Clone(),
			Body:       string(body),
		})
		u.mu.Unlock()

		if strings.HasPrefix(r.URL.Path, "/status/") {
			code, _ := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/status/"))
			w.Header().Set("X-Upstream-Status", strconv.Itoa(code))
			w.WriteHeader(code)
			fmt.Fprintf(w, "upstream says %d", code)
			return
		}
		w.Header().Set("X-Upstream", "yes")
		w.Header().Add("Set-Cookie", "a=1; Path=/")
		w.Header().Add("Set-Cookie", "b=2; Path=/")
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, "echo:"+string(body))
	})
}

func newTransparencyFixture(t *testing.T) (*recordingUpstream, *testProxy, string) {
	t.Helper()
	up := &recordingUpstream{}
	upstream := httptest.NewServer(up.handler())
	t.Cleanup(upstream.Close)
	snap := buildSnapshot(t, fmt.Sprintf(`
version: 1
routes:
  - name: api
    prefix: /api
    target: %s
`, upstream.URL))
	tp := newTestProxy(t, snap)
	upstreamHost := strings.TrimPrefix(upstream.URL, "http://")
	return up, tp, upstreamHost
}

func TestTransparencyEndToEnd(t *testing.T) {
	up, tp, upstreamHost := newTransparencyFixture(t)

	req, err := http.NewRequest(http.MethodPost,
		tp.server.URL+"/api/echo?a=1&b=%2F&c=x%20y", strings.NewReader("hello body"))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("X-Test-In", "v1")
	req.Header.Set("Content-Type", "text/plain")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()

	seen := up.last(t)
	if seen.Method != http.MethodPost {
		t.Errorf("upstream method = %q, want POST", seen.Method)
	}
	// Prefix stripped, query preserved byte-exact (§9.1, §8).
	if seen.RequestURI != "/echo?a=1&b=%2F&c=x%20y" {
		t.Errorf("upstream RequestURI = %q", seen.RequestURI)
	}
	// Host rewritten to the target host (§9.2).
	if seen.Host != upstreamHost {
		t.Errorf("upstream Host = %q, want %q", seen.Host, upstreamHost)
	}
	if got := seen.Header.Get("X-Test-In"); got != "v1" {
		t.Errorf("X-Test-In = %q, want v1", got)
	}
	if got := seen.Header.Get("Content-Type"); got != "text/plain" {
		t.Errorf("Content-Type = %q", got)
	}
	if seen.Body != "hello body" {
		t.Errorf("upstream body = %q", seen.Body)
	}
	// Go's Rewrite path must not have added any forwarding headers (§9.3).
	for _, h := range []string{"X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto", "Forwarded", "Via"} {
		if _, ok := seen.Header[h]; ok {
			t.Errorf("upstream unexpectedly received %s = %q", h, seen.Header[h])
		}
	}

	// Response transparency (§9.5): headers pass through, incl. multiple
	// Set-Cookie values; body untouched.
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Upstream"); got != "yes" {
		t.Errorf("X-Upstream = %q", got)
	}
	if cookies := resp.Header["Set-Cookie"]; len(cookies) != 2 ||
		cookies[0] != "a=1; Path=/" || cookies[1] != "b=2; Path=/" {
		t.Errorf("Set-Cookie = %q", cookies)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "echo:hello body" {
		t.Errorf("client body = %q", body)
	}
}

func TestForwardHeaderStripping(t *testing.T) {
	up := &recordingUpstream{}
	upstream := httptest.NewServer(up.handler())
	t.Cleanup(upstream.Close)
	snap := buildSnapshot(t, fmt.Sprintf(`
version: 1
routes:
  - name: strip
    prefix: /strip
    target: %s
  - name: keep
    prefix: /keep
    target: %s
    strip_forward_headers: false
`, upstream.URL, upstream.URL))
	tp := newTestProxy(t, snap)

	send := func(t *testing.T, path string) recordedRequest {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, tp.server.URL+path, nil)
		req.Header.Set("X-Forwarded-For", "1.2.3.4")
		req.Header.Set("X-Forwarded-Host", "public.example.com")
		req.Header.Set("X-Forwarded-Proto", "https")
		req.Header.Set("Forwarded", "for=1.2.3.4")
		req.Header.Set("Via", "1.1 caddy")
		req.Header.Set("X-Keep", "yes")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("Do: %v", err)
		}
		resp.Body.Close()
		return up.last(t)
	}

	t.Run("default strips proxy metadata", func(t *testing.T) {
		seen := send(t, "/strip/echo")
		for _, h := range []string{"X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto", "Forwarded", "Via"} {
			if _, ok := seen.Header[h]; ok {
				t.Errorf("%s leaked to upstream: %q", h, seen.Header[h])
			}
		}
		if got := seen.Header.Get("X-Keep"); got != "yes" {
			t.Errorf("end-to-end X-Keep = %q, want yes", got)
		}
	})

	t.Run("strip_forward_headers false passes through unchanged", func(t *testing.T) {
		seen := send(t, "/keep/echo")
		// Pass through UNCHANGED: no client IP appended, nothing stripped.
		if got := seen.Header["X-Forwarded-For"]; len(got) != 1 || got[0] != "1.2.3.4" {
			t.Errorf("X-Forwarded-For = %q, want exactly [1.2.3.4]", got)
		}
		if got := seen.Header.Get("X-Forwarded-Proto"); got != "https" {
			t.Errorf("X-Forwarded-Proto = %q", got)
		}
		if got := seen.Header.Get("Forwarded"); got != "for=1.2.3.4" {
			t.Errorf("Forwarded = %q", got)
		}
		if got := seen.Header.Get("Via"); got != "1.1 caddy" {
			t.Errorf("Via = %q", got)
		}
	})
}

func TestHopByHopHeadersStripped(t *testing.T) {
	up, tp, _ := newTransparencyFixture(t)

	// Raw HTTP/1.1 so hop-by-hop headers reach the proxy verbatim.
	conn, err := net.Dial("tcp", strings.TrimPrefix(tp.server.URL, "http://"))
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer conn.Close()
	fmt.Fprintf(conn, "GET /api/echo HTTP/1.1\r\n"+
		"Host: proxy.test\r\n"+
		"Connection: close, X-Hop\r\n"+
		"X-Hop: zap\r\n"+
		"Keep-Alive: timeout=5\r\n"+
		"Proxy-Authorization: Basic c2VjcmV0\r\n"+
		"X-Keep: yes\r\n\r\n")
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	seen := up.last(t)
	for _, h := range []string{"Connection", "X-Hop", "Keep-Alive", "Proxy-Authorization"} {
		if _, ok := seen.Header[h]; ok {
			t.Errorf("hop-by-hop header %s leaked to upstream: %q", h, seen.Header[h])
		}
	}
	if got := seen.Header.Get("X-Keep"); got != "yes" {
		t.Errorf("end-to-end X-Keep = %q, want yes", got)
	}
}

func TestUpstreamStatusPassthrough(t *testing.T) {
	_, tp, _ := newTransparencyFixture(t)
	for _, code := range []int{401, 404, 429, 500} {
		t.Run(strconv.Itoa(code), func(t *testing.T) {
			resp, err := http.Get(fmt.Sprintf("%s/api/status/%d", tp.server.URL, code))
			if err != nil {
				t.Fatalf("GET: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != code {
				t.Fatalf("status = %d, want %d", resp.StatusCode, code)
			}
			if got := resp.Header.Get("X-Upstream-Status"); got != strconv.Itoa(code) {
				t.Fatalf("X-Upstream-Status = %q", got)
			}
			body, _ := io.ReadAll(resp.Body)
			if want := fmt.Sprintf("upstream says %d", code); string(body) != want {
				t.Fatalf("body = %q, want %q", body, want)
			}
		})
	}
}

func TestRewriteEndToEnd(t *testing.T) {
	up := &recordingUpstream{}
	upstream := httptest.NewServer(up.handler())
	t.Cleanup(upstream.Close)

	snap := buildSnapshot(t, fmt.Sprintf(`
version: 1
routes:
  - name: openai
    prefix: /openai
    target: %s/v1
  - name: keep
    prefix: /svc
    target: %s
    strip_prefix: false
`, upstream.URL, upstream.URL))
	tp := newTestProxy(t, snap)

	cases := []struct {
		name string
		path string
		want string
	}{
		{"base path + strip + query", "/openai/chat/completions?model=gpt-4&q=a%2Fb", "/v1/chat/completions?model=gpt-4&q=a%2Fb"},
		{"escaped slash preserved in path", "/openai/files/a%2Fb", "/v1/files/a%2Fb"},
		{"prefix itself", "/openai", "/v1"},
		{"strip_prefix false keeps full path", "/svc/deep/x", "/svc/deep/x"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u, err := url.Parse(tp.server.URL + tc.path)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			// Build the request from the parsed URL so %2F survives the
			// client side too.
			resp, err := http.DefaultClient.Do(&http.Request{Method: http.MethodGet, URL: u})
			if err != nil {
				t.Fatalf("GET: %v", err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d", resp.StatusCode)
			}
			if seen := up.last(t); seen.RequestURI != tc.want {
				t.Fatalf("upstream RequestURI = %q, want %q", seen.RequestURI, tc.want)
			}
		})
	}
}
