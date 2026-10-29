package proxycore

import (
	"context"
	"errors"
	"net"
	"strconv"
	"sync"

	"ashgate-lite/internal/proxycore/capture"
	"ashgate-lite/internal/proxycore/httpproxy"
	"ashgate-lite/internal/proxycore/internal/netx"
	"ashgate-lite/internal/proxycore/socks5"
	"ashgate-lite/internal/proxycore/vless"
)

var errBridgeDown = errors.New("VLESS: локальный мост не запущен")

// dialTCP connects to host:port through p. A VLESS proxy goes through its
// local SOCKS5 bridge at bridge.
func dialTCP(ctx context.Context, p Proxy, bridge, host string, port uint16) (net.Conn, error) {
	switch p.Type {
	case SOCKS5:
		return socks5.Dial(ctx, p.server(), host, port)
	case HTTP:
		return httpproxy.Dial(ctx, p.server(), host, port)
	case VLESS:
		if bridge == "" {
			return nil, errBridgeDown
		}
		c, err := socks5.Dial(ctx, netx.Server{Address: bridge}, host, port)
		return c, vless.Explain(err)
	default:
		return nil, errors.New("unknown proxy type " + string(p.Type))
	}
}

// dialUDP opens a SOCKS5 UDP association through p. VLESS uses the same
// association on its local bridge; HTTP proxies carry no general UDP.
func dialUDP(ctx context.Context, p Proxy, bridge string) (*socks5.Assoc, error) {
	switch p.Type {
	case SOCKS5:
		return socks5.Associate(ctx, p.server())
	case VLESS:
		if bridge == "" {
			return nil, errBridgeDown
		}
		a, err := socks5.Associate(ctx, netx.Server{Address: bridge})
		return a, vless.Explain(err)
	default:
		return nil, capture.ErrUDPUnsupported
	}
}

// upstreams resolves proxy IDs for the capture engine. It is replaced as a
// whole when the configuration changes.
type upstreams struct {
	mu      sync.RWMutex
	proxies map[string]Proxy
	bridges map[string]string // proxy ID -> local SOCKS5 address of its VLESS bridge
}

func (u *upstreams) set(proxies map[string]Proxy, bridges map[string]string) {
	u.mu.Lock()
	u.proxies, u.bridges = proxies, bridges
	u.mu.Unlock()
}

func (u *upstreams) get(id string) (Proxy, string, error) {
	u.mu.RLock()
	defer u.mu.RUnlock()
	p, ok := u.proxies[id]
	if !ok {
		return Proxy{}, "", errors.New("unknown proxy " + strconv.Quote(id))
	}
	return p, u.bridges[id], nil
}

func (u *upstreams) DialTCP(ctx context.Context, id, host string, port uint16) (net.Conn, error) {
	p, bridge, err := u.get(id)
	if err != nil {
		return nil, err
	}
	return dialTCP(ctx, p, bridge, host, port)
}

func (u *upstreams) DialUDP(ctx context.Context, id string) (capture.UDPSession, error) {
	p, bridge, err := u.get(id)
	if err != nil {
		return nil, err
	}
	a, err := dialUDP(ctx, p, bridge)
	if err != nil {
		return nil, err
	}
	return a, nil
}

func (u *upstreams) DNSQuery(ctx context.Context, id, server string, query []byte) ([]byte, error) {
	p, _, err := u.get(id)
	if err != nil {
		return nil, err
	}
	return httpproxy.DNSQuery(ctx, p.server(), server, query)
}
