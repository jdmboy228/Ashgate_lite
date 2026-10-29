// Package proxycore is the proxy engine of Ashgate Lite: SOCKS5, HTTP
// CONNECT and VLESS clients, per-application capture, and health checks.
// The rest of the app talks to it only through Core and the types here.
package proxycore

import (
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"

	"ashgate-lite/internal/proxycore/internal/netx"
	"ashgate-lite/internal/proxycore/vless"
)

// Type is the upstream protocol.
type Type string

// Supported proxy types, spelled as the UI shows them.
const (
	SOCKS5 Type = "SOCKS5"
	HTTP   Type = "HTTP"
	VLESS  Type = "VLESS"
)

// Proxy is one upstream proxy, parsed from a share link.
type Proxy struct {
	Type     Type
	Host     string
	Port     uint16
	Username string
	Password string
	// Label is the link's #fragment, such as a country or server name.
	Label string
	// VLESS holds the client parameters of a VLESS proxy.
	VLESS *vless.Params
	// Link is the share link the proxy was parsed from.
	Link string
}

// ErrParse wraps every link that cannot be used as a proxy.
var ErrParse = errors.New("не удалось разобрать ссылку прокси")

// Address is host:port of the proxy server.
func (p Proxy) Address() string {
	return net.JoinHostPort(p.Host, strconv.Itoa(int(p.Port)))
}

// CarriesUDP reports whether the proxy relays general UDP. HTTP CONNECT
// carries TCP only; DNS then goes as DNS-over-TCP through the tunnel.
func (p Proxy) CarriesUDP() bool { return p.Type != HTTP }

func (p Proxy) server() netx.Server {
	return netx.Server{Address: p.Address(), Username: p.Username, Password: p.Password}
}

// ParseLink reads a socks5://, socks://, http:// or vless:// share link.
func ParseLink(raw string) (Proxy, error) {
	raw = strings.TrimSpace(raw)
	scheme, _, ok := strings.Cut(raw, "://")
	if !ok {
		return Proxy{}, ErrParse
	}
	switch strings.ToLower(scheme) {
	case "socks5", "socks5h", "socks":
		return parseURL(raw, SOCKS5)
	case "http":
		return parseURL(raw, HTTP)
	case "vless":
		return parseVLESS(raw)
	default:
		return Proxy{}, ErrParse
	}
}

func parseURL(raw string, t Type) (Proxy, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.Port() == "" {
		return Proxy{}, ErrParse
	}
	port, err := strconv.ParseUint(u.Port(), 10, 16)
	if err != nil || port == 0 {
		return Proxy{}, ErrParse
	}
	p := Proxy{Type: t, Host: u.Hostname(), Port: uint16(port), Label: strings.TrimSpace(u.Fragment)}
	if u.User != nil {
		p.Username = u.User.Username()
		p.Password, _ = u.User.Password()
	}
	if len(p.Username) > 255 || len(p.Password) > 255 {
		return Proxy{}, ErrParse
	}
	p.Link = canonicalLink(p)
	return p, nil
}

// canonicalLink rebuilds a SOCKS5 or HTTP link in one spelling, so the same
// proxy pasted twice compares equal.
func canonicalLink(p Proxy) string {
	u := url.URL{Scheme: strings.ToLower(string(p.Type)), Host: p.Address(), Fragment: p.Label}
	if p.Username != "" || p.Password != "" {
		u.User = url.UserPassword(p.Username, p.Password)
	}
	return u.String()
}

func parseVLESS(raw string) (Proxy, error) {
	parsed, err := vless.ParseLink(raw)
	if err != nil {
		return Proxy{}, errors.Join(ErrParse, err)
	}
	host, portStr, err := net.SplitHostPort(parsed.Address)
	if err != nil {
		return Proxy{}, errors.Join(ErrParse, err)
	}
	port, err := strconv.ParseUint(portStr, 10, 16)
	if err != nil || port == 0 {
		return Proxy{}, ErrParse
	}
	params := parsed.Params
	return Proxy{Type: VLESS, Host: host, Port: uint16(port), Label: parsed.Label, VLESS: &params, Link: raw}, nil
}
