package transport

import (
	"net"

	"github.com/kites262/piperouter/internal/config"
)

// newDirectEntry builds the built-in "direct" entry (PRD §11.1): plain
// TCP dial to the target, no proxy in between.
func newDirectEntry(netCfg config.NetworkConfig) *Entry {
	dialer := &net.Dialer{Timeout: netCfg.DialTimeout.Std()}
	return &Entry{
		Name:         config.DirectName,
		Type:         config.TransportDirect,
		ProxyURL:     "",
		RoundTripper: newBaseTransport(dialer.DialContext, netCfg),
		DialContext:  dialer.DialContext,
	}
}
