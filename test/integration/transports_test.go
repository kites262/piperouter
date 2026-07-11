// Outbound transport acceptance cases through the whole app: PRD §23.7
// (HTTP proxy transport) and §23.8 (SOCKS5 transport), including the
// proxy-down → 502 upstream_connection_failed mapping (PRD §9.6).
package integration_test

import (
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/kites262/piperouter/internal/config"
)

// assertUpstreamConnFailed checks the fixed 502 JSON error contract.
func assertUpstreamConnFailed(t *testing.T, resp *http.Response, body string) {
	t.Helper()
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	if !strings.Contains(body, `"error":"upstream_connection_failed"`) {
		t.Errorf("body = %q, want upstream_connection_failed", body)
	}
}

// TestHTTPProxyTransport covers PRD §23.7: a route bound to an HTTP proxy
// transport sends its http-target traffic THROUGH the proxy (observed via
// the proxy's request counter, absolute-form), and a dead proxy maps to
// 502 upstream_connection_failed.
func TestHTTPProxyTransport(t *testing.T) {
	up := pathEcho(t, "via-http")
	hp := newTestHTTPProxy(t)

	cfg := baseConfig(
		routeVia("through", "/through", up.URL, "hp"),
		routeVia("down", "/down", up.URL, "hp-dead"),
	)
	cfg.Transports = []config.TransportConfig{
		{Name: "hp", Type: config.TransportHTTP, URL: hp.srv.URL},
		{Name: "hp-dead", Type: config.TransportHTTP, URL: "http://" + deadAddr(t)},
	}
	ta := startApp(t, cfg)
	client := newClient(t)

	// Traffic must flow through the proxy for a plain-http target.
	resp, body := get(t, client, ta.proxyURL()+"/through/hello")
	if resp.StatusCode != http.StatusOK || body != "via-http:/hello" {
		t.Errorf("via HTTP proxy: status=%d body=%q, want 200 %q", resp.StatusCode, body, "via-http:/hello")
	}
	plain, absolute, connects := hp.counts()
	if plain != 1 || absolute != 1 {
		t.Errorf("HTTP proxy saw plain=%d absolute=%d, want 1/1 — traffic did not flow through the proxy", plain, absolute)
	}
	if connects != 0 {
		t.Errorf("HTTP proxy saw %d CONNECTs for an http target, want 0", connects)
	}

	// Proxy unavailable → 502 upstream_connection_failed (§23.7, §9.6).
	dresp, dbody := get(t, client, ta.proxyURL()+"/down/hello")
	assertUpstreamConnFailed(t, dresp, dbody)
}

// TestSOCKS5Transport covers PRD §23.8: a domain-name target is reached
// through the SOCKS5 proxy with the hostname passed through unresolved
// (ATYP=domain), and a dead SOCKS5 server maps to 502.
func TestSOCKS5Transport(t *testing.T) {
	up := pathEcho(t, "via-socks")
	socks := newTestSOCKS5Server(t)

	// Address the upstream BY HOSTNAME so name resolution must happen at
	// the SOCKS5 proxy (PRD §11.3).
	upURL, ok := strings.CutPrefix(up.URL, "http://")
	if !ok {
		t.Fatalf("unexpected upstream URL %q", up.URL)
	}
	_, port, err := net.SplitHostPort(upURL)
	if err != nil {
		t.Fatalf("split upstream host port: %v", err)
	}
	target := "http://localhost:" + port

	cfg := baseConfig(
		routeVia("s", "/s", target, "sp"),
		routeVia("sdown", "/sdown", up.URL, "sp-dead"),
	)
	cfg.Transports = []config.TransportConfig{
		{Name: "sp", Type: config.TransportSOCKS5, URL: "socks5://" + socks.addr()},
		{Name: "sp-dead", Type: config.TransportSOCKS5, URL: "socks5://" + deadAddr(t)},
	}
	ta := startApp(t, cfg)
	client := newClient(t)

	resp, body := get(t, client, ta.proxyURL()+"/s/hi")
	if resp.StatusCode != http.StatusOK || body != "via-socks:/hi" {
		t.Errorf("via SOCKS5: status=%d body=%q, want 200 %q", resp.StatusCode, body, "via-socks:/hi")
	}
	reqs := socks.seen()
	if len(reqs) != 1 {
		t.Fatalf("SOCKS5 server saw %d CONNECTs, want 1: %v", len(reqs), reqs)
	}
	if reqs[0].atyp != 0x03 {
		t.Errorf("SOCKS5 ATYP = %#x, want 0x03 (domain: hostname must reach the proxy unresolved)", reqs[0].atyp)
	}
	if reqs[0].host != "localhost" {
		t.Errorf("SOCKS5 host = %q, want %q", reqs[0].host, "localhost")
	}

	// SOCKS5 unavailable → 502 upstream_connection_failed (§23.8, §9.6).
	dresp, dbody := get(t, client, ta.proxyURL()+"/sdown/hi")
	assertUpstreamConnFailed(t, dresp, dbody)
}
