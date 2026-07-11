package transport

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"time"

	xproxy "golang.org/x/net/proxy"

	"github.com/kites262/piperouter/internal/config"
)

// newSOCKS5Entry builds the entry for a type "socks5" transport (PRD §11.3).
// The SOCKS5 dialer (no auth) is the transport's DialContext, so target
// hostnames are passed through to the proxy unresolved (ATYP domain) and
// name resolution happens at the SOCKS5 server. Proxy stays nil on the
// *http.Transport: all traffic already flows through the SOCKS5 dialer.
func newSOCKS5Entry(tc config.TransportConfig, netCfg config.NetworkConfig) (*Entry, error) {
	u, err := url.Parse(tc.URL)
	if err != nil {
		return nil, fmt.Errorf("invalid proxy url: %w", err)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("proxy url has no host")
	}
	proxyAddr := u.Host
	if u.Port() == "" {
		proxyAddr = net.JoinHostPort(u.Hostname(), "1080")
	}

	forward := &net.Dialer{Timeout: netCfg.DialTimeout.Std()}
	d, err := xproxy.SOCKS5("tcp", proxyAddr, nil, forward)
	if err != nil {
		return nil, fmt.Errorf("build socks5 dialer: %w", err)
	}
	cd, ok := d.(xproxy.ContextDialer)
	if !ok {
		return nil, fmt.Errorf("socks5 dialer does not implement proxy.ContextDialer")
	}

	// Bound the WHOLE SOCKS5 attempt (TCP connect + method negotiation +
	// CONNECT reply) by dial_timeout. net.Dialer.Timeout only covers the
	// connect to the proxy; x/net's socks client derives its deadline from
	// the context, which data-plane requests never carry (PRD §11.5, §23.8).
	dial := boundedDial(cd.DialContext, netCfg.DialTimeout.Std())

	return &Entry{
		Name:         tc.Name,
		Type:         config.TransportSOCKS5,
		ProxyURL:     tc.URL,
		RoundTripper: newBaseTransport(dial, netCfg),
		DialContext:  dial,
	}, nil
}

// boundedDial wraps a DialContext so each attempt is capped by timeout when
// the incoming context has no earlier deadline of its own.
func boundedDial(dial dialFunc, timeout time.Duration) dialFunc {
	if timeout <= 0 {
		return dial
	}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		ctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		return dial(ctx, network, addr)
	}
}
