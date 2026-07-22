// Package transport builds and owns the outbound transport pool
// (PRD §5.2, §11): the built-in "direct" entry plus one long-lived
// *http.Transport per configured http/socks5 proxy transport. Entries
// are shared by every route that references them so connection pools
// are reused across routes (PRD §11.4). No retries, no fallback
// (PRD §11.6).
package transport

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"slices"

	"github.com/kites262/piperouter/internal/config"
)

// dialFunc opens a raw connection through the transport chain.
type dialFunc = func(ctx context.Context, network, addr string) (net.Conn, error)

// Entry is one outbound link. It is immutable after construction and is
// shared by every route referencing the transport name.
type Entry struct {
	Name     string
	Type     string // config.TransportDirect | config.TransportHTTP | config.TransportSOCKS5
	ProxyURL string // "" for direct

	// RoundTripper is this entry's single long-lived *http.Transport,
	// shared across routes (PRD §11.4).
	RoundTripper http.RoundTripper

	// DialContext opens a raw TCP connection through the chain (direct
	// dial / HTTP CONNECT tunnel / SOCKS5). It is used by the WebSocket
	// path; the caller layers TLS on top for https targets.
	DialContext func(ctx context.Context, network, addr string) (net.Conn, error)
}

// Pool is the immutable set of transport entries built from one
// configuration snapshot. It is safe for concurrent use.
type Pool struct {
	entries map[string]*Entry
}

// NewPool builds a pool containing the built-in "direct" entry plus one
// entry per configured transport. The input is assumed to be validated
// (config.Validate), but structural problems still return errors rather
// than panicking.
func NewPool(transports []config.TransportConfig, netCfg config.NetworkConfig) (*Pool, error) {
	entries := make(map[string]*Entry, len(transports)+1)
	entries[config.DirectName] = newDirectEntry(netCfg)

	for _, tc := range transports {
		if tc.Name == config.DirectName {
			return nil, fmt.Errorf("transport %q: name is reserved for the built-in direct transport", tc.Name)
		}
		if _, dup := entries[tc.Name]; dup {
			return nil, fmt.Errorf("transport %q: duplicate name", tc.Name)
		}
		var (
			e   *Entry
			err error
		)
		switch tc.Type {
		case config.TransportHTTP:
			e, err = newHTTPEntry(tc, netCfg)
		case config.TransportSOCKS5:
			e, err = newSOCKS5Entry(tc, netCfg)
		default:
			err = fmt.Errorf("unsupported type %q", tc.Type)
		}
		if err != nil {
			return nil, fmt.Errorf("transport %q: %w", tc.Name, err)
		}
		entries[tc.Name] = e
	}
	return &Pool{entries: entries}, nil
}

// Get returns the entry for name, or (nil, false) if it does not exist.
func (p *Pool) Get(name string) (*Entry, bool) {
	e, ok := p.entries[name]
	return e, ok
}

// Names returns all entry names sorted ascending; it always includes
// "direct".
func (p *Pool) Names() []string {
	names := make([]string, 0, len(p.entries))
	for name := range p.entries {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// Len reports the number of entries, including the built-in direct one.
func (p *Pool) Len() int { return len(p.entries) }

// CloseIdleConnections closes idle pooled connections on every entry.
// In-flight requests keep their connections (PRD §11.4, §12.2).
func (p *Pool) CloseIdleConnections() {
	for _, e := range p.entries {
		if t, ok := e.RoundTripper.(interface{ CloseIdleConnections() }); ok {
			t.CloseIdleConnections()
		}
	}
}

// newBaseTransport builds the per-entry *http.Transport with the shared
// tuning from PRD §11.5 and the transparency rules from PRD §9.1
// (DisableCompression: never inject Accept-Encoding).
//
// MaxIdleConns is raised above the Go default of 100 so multi-host routing
// does not thrash the global idle pool while MaxIdleConnsPerHost is 32.
// MaxConnsPerHost stays at 0 (unlimited) — same as before — so concurrency
// semantics are unchanged.
func newBaseTransport(dial dialFunc, netCfg config.NetworkConfig) *http.Transport {
	const perHostIdle = 32
	return &http.Transport{
		DialContext:           dial,
		TLSHandshakeTimeout:   netCfg.TLSHandshakeTimeout.Std(),
		ResponseHeaderTimeout: netCfg.ResponseHeaderTimeout.Std(),
		IdleConnTimeout:       netCfg.IdleConnectionTimeout.Std(),
		ForceAttemptHTTP2:     true,
		DisableCompression:    true,
		MaxIdleConns:          perHostIdle * 16, // headroom for many upstream hosts
		MaxIdleConnsPerHost:   perHostIdle,
	}
}
