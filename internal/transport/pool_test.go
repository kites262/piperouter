package transport

import (
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/kites262/piperouter/internal/config"
)

func TestNewPoolAlwaysHasDirect(t *testing.T) {
	p := newTestPool(t)

	if got := p.Len(); got != 1 {
		t.Fatalf("Len() = %d, want 1", got)
	}
	e := mustGet(t, p, config.DirectName)
	if e.Name != config.DirectName {
		t.Errorf("Name = %q, want %q", e.Name, config.DirectName)
	}
	if e.Type != config.TransportDirect {
		t.Errorf("Type = %q, want %q", e.Type, config.TransportDirect)
	}
	if e.ProxyURL != "" {
		t.Errorf("ProxyURL = %q, want empty", e.ProxyURL)
	}
	if e.RoundTripper == nil {
		t.Error("RoundTripper is nil")
	}
	if e.DialContext == nil {
		t.Error("DialContext is nil")
	}
}

func TestPoolGetUnknown(t *testing.T) {
	p := newTestPool(t)
	if e, ok := p.Get("no-such-transport"); ok || e != nil {
		t.Fatalf("Get(unknown) = %v, %v; want nil, false", e, ok)
	}
}

func TestPoolNamesSortedIncludesDirect(t *testing.T) {
	p := newTestPool(t,
		config.TransportConfig{Name: "zeta", Type: config.TransportHTTP, URL: "http://127.0.0.1:7890"},
		config.TransportConfig{Name: "alpha", Type: config.TransportSOCKS5, URL: "socks5://127.0.0.1:1080"},
	)

	got := p.Names()
	want := []string{"alpha", "direct", "zeta"}
	if !slices.Equal(got, want) {
		t.Fatalf("Names() = %v, want %v", got, want)
	}
	if p.Len() != 3 {
		t.Fatalf("Len() = %d, want 3", p.Len())
	}
}

func TestPoolSharedEntryAcrossLookups(t *testing.T) {
	p := newTestPool(t,
		config.TransportConfig{Name: "hp", Type: config.TransportHTTP, URL: "http://127.0.0.1:7890"},
	)

	// Two routes referencing the same transport must share the SAME
	// *http.Transport (PRD §11.4: shared connection pool).
	for _, name := range []string{config.DirectName, "hp"} {
		e1 := mustGet(t, p, name)
		e2 := mustGet(t, p, name)
		if e1 != e2 {
			t.Errorf("%s: Get returned distinct entries %p / %p", name, e1, e2)
		}
		if e1.RoundTripper != e2.RoundTripper {
			t.Errorf("%s: RoundTripper not shared", name)
		}
	}
}

func TestPoolTransportSettings(t *testing.T) {
	netCfg := testNetCfg()
	p, err := NewPool([]config.TransportConfig{
		{Name: "hp", Type: config.TransportHTTP, URL: "http://127.0.0.1:7890"},
		{Name: "sp", Type: config.TransportSOCKS5, URL: "socks5://127.0.0.1:1080"},
	}, netCfg)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	t.Cleanup(p.CloseIdleConnections)

	tests := []struct {
		name      string
		entryType string
		proxyURL  string
		wantProxy bool // Proxy func set on the *http.Transport
	}{
		{name: "direct", entryType: config.TransportDirect, proxyURL: "", wantProxy: false},
		{name: "hp", entryType: config.TransportHTTP, proxyURL: "http://127.0.0.1:7890", wantProxy: true},
		{name: "sp", entryType: config.TransportSOCKS5, proxyURL: "socks5://127.0.0.1:1080", wantProxy: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := mustGet(t, p, tc.name)
			if e.Type != tc.entryType {
				t.Errorf("Type = %q, want %q", e.Type, tc.entryType)
			}
			if e.ProxyURL != tc.proxyURL {
				t.Errorf("ProxyURL = %q, want %q", e.ProxyURL, tc.proxyURL)
			}
			if e.DialContext == nil {
				t.Error("DialContext is nil")
			}

			tr, ok := e.RoundTripper.(*http.Transport)
			if !ok {
				t.Fatalf("RoundTripper is %T, want *http.Transport", e.RoundTripper)
			}
			if !tr.DisableCompression {
				t.Error("DisableCompression = false, want true")
			}
			if !tr.ForceAttemptHTTP2 {
				t.Error("ForceAttemptHTTP2 = false, want true")
			}
			if tr.MaxIdleConnsPerHost != 32 {
				t.Errorf("MaxIdleConnsPerHost = %d, want 32", tr.MaxIdleConnsPerHost)
			}
			if got, want := tr.TLSHandshakeTimeout, netCfg.TLSHandshakeTimeout.Std(); got != want {
				t.Errorf("TLSHandshakeTimeout = %v, want %v", got, want)
			}
			if got, want := tr.ResponseHeaderTimeout, netCfg.ResponseHeaderTimeout.Std(); got != want {
				t.Errorf("ResponseHeaderTimeout = %v, want %v", got, want)
			}
			if got, want := tr.IdleConnTimeout, netCfg.IdleConnectionTimeout.Std(); got != want {
				t.Errorf("IdleConnTimeout = %v, want %v", got, want)
			}
			if tr.DialContext == nil {
				t.Error("transport DialContext is nil")
			}

			if tc.wantProxy {
				if tr.Proxy == nil {
					t.Fatal("Proxy = nil, want proxy func")
				}
				req, err := http.NewRequest(http.MethodGet, "http://example.com/", nil)
				if err != nil {
					t.Fatalf("NewRequest: %v", err)
				}
				pu, err := tr.Proxy(req)
				if err != nil {
					t.Fatalf("Proxy(): %v", err)
				}
				if pu == nil || pu.String() != tc.proxyURL {
					t.Errorf("Proxy() = %v, want %q", pu, tc.proxyURL)
				}
			} else if tr.Proxy != nil {
				t.Error("Proxy != nil, want nil")
			}
		})
	}
}

func TestNewPoolErrors(t *testing.T) {
	tests := []struct {
		name       string
		transports []config.TransportConfig
	}{
		{
			name: "unsupported type",
			transports: []config.TransportConfig{
				{Name: "t1", Type: "quic", URL: "http://127.0.0.1:7890"},
			},
		},
		{
			name: "reserved direct name",
			transports: []config.TransportConfig{
				{Name: "direct", Type: config.TransportHTTP, URL: "http://127.0.0.1:7890"},
			},
		},
		{
			name: "duplicate name",
			transports: []config.TransportConfig{
				{Name: "dup", Type: config.TransportHTTP, URL: "http://127.0.0.1:7890"},
				{Name: "dup", Type: config.TransportSOCKS5, URL: "socks5://127.0.0.1:1080"},
			},
		},
		{
			name: "invalid http proxy url",
			transports: []config.TransportConfig{
				{Name: "bad", Type: config.TransportHTTP, URL: "http://[::1"},
			},
		},
		{
			name: "http proxy url without host",
			transports: []config.TransportConfig{
				{Name: "bad", Type: config.TransportHTTP, URL: "http://"},
			},
		},
		{
			name: "invalid socks5 proxy url",
			transports: []config.TransportConfig{
				{Name: "bad", Type: config.TransportSOCKS5, URL: "socks5://[::1"},
			},
		},
		{
			name: "socks5 proxy url without host",
			transports: []config.TransportConfig{
				{Name: "bad", Type: config.TransportSOCKS5, URL: "socks5://"},
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p, err := NewPool(tc.transports, testNetCfg())
			if err == nil {
				p.CloseIdleConnections()
				t.Fatal("NewPool succeeded, want error")
			}
		})
	}
}

func TestCloseIdleConnectionsNoPanic(t *testing.T) {
	p := newTestPool(t,
		config.TransportConfig{Name: "hp", Type: config.TransportHTTP, URL: "http://127.0.0.1:7890"},
		config.TransportConfig{Name: "sp", Type: config.TransportSOCKS5, URL: "socks5://127.0.0.1:1080"},
	)
	p.CloseIdleConnections()
	p.CloseIdleConnections() // idempotent
}

func TestNetworkDefaultsApplied(t *testing.T) {
	// Sanity: pool built from normalized default config uses the
	// documented defaults (PRD §11.5).
	cfg := config.Default()
	p, err := NewPool(cfg.Transports, cfg.Network)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	t.Cleanup(p.CloseIdleConnections)

	tr := mustGet(t, p, config.DirectName).RoundTripper.(*http.Transport)
	if tr.TLSHandshakeTimeout != 10*time.Second {
		t.Errorf("TLSHandshakeTimeout = %v, want 10s", tr.TLSHandshakeTimeout)
	}
	if tr.ResponseHeaderTimeout != 120*time.Second {
		t.Errorf("ResponseHeaderTimeout = %v, want 120s", tr.ResponseHeaderTimeout)
	}
	if tr.IdleConnTimeout != 90*time.Second {
		t.Errorf("IdleConnTimeout = %v, want 90s", tr.IdleConnTimeout)
	}
}
