// Package capture diverts the traffic of selected Windows applications with
// WinDivert and relays it through upstream proxies.
package capture

import (
	"context"
	"errors"
	"net"
)

// Relay addresses are the local TCP endpoints that redirected handshakes go to.
const (
	relay4 = "127.0.0.1:34020"
	relay6 = "[::1]:34020"
)

// dnsServers receive the port-53 queries of proxied applications, through
// the proxy, so name lookups do not leak to the local resolver.
var dnsServers = []string{"1.1.1.1:53", "8.8.8.8:53"}

// OthersKey is the Stats key of the default route (applications without a rule).
const OthersKey = "*"

// Rule sends one application, matched by its full exe path, through a proxy.
// An empty ProxyID keeps the application direct even when Default is set.
type Rule struct {
	Path    string
	ProxyID string
}

// Plan is everything the engine needs to route traffic.
type Plan struct {
	Rules []Rule
	// Default is the proxy for applications without a rule; empty means direct.
	Default string
	// Bypass lists proxy server hosts. Traffic to them always stays direct,
	// otherwise the connection to the proxy itself would loop.
	Bypass []string
	// CarriesUDP reports, per proxy ID, whether the proxy relays general UDP.
	CarriesUDP map[string]bool
}

// ErrUDPUnsupported is returned for UDP through a proxy that has no UDP transport.
var ErrUDPUnsupported = errors.New("proxy does not support UDP")

// UDPSession is one UDP association through a proxy.
type UDPSession interface {
	Send(ctx context.Context, dst *net.UDPAddr, payload []byte) error
	Recv(ctx context.Context) (*net.UDPAddr, []byte, error)
	Close() error
}

// Upstream opens connections through the proxy with the given ID.
type Upstream interface {
	DialTCP(ctx context.Context, proxyID, host string, port uint16) (net.Conn, error)
	DialUDP(ctx context.Context, proxyID string) (UDPSession, error)
	// DNSQuery sends one DNS message over TCP through a proxy without UDP.
	DNSQuery(ctx context.Context, proxyID, server string, query []byte) ([]byte, error)
}
