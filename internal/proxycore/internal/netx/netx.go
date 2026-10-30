// Package netx holds the small networking helpers shared by the proxy clients.
package netx

import (
	"context"
	"net"
	"time"
)

// Server is an upstream proxy endpoint with optional credentials.
type Server struct {
	Address  string
	Username string
	Password string
}

// HasAuth reports whether credentials are set.
func (s Server) HasAuth() bool { return s.Username != "" || s.Password != "" }

// DialTimeout bounds the TCP connect to the proxy itself.
const DialTimeout = 15 * time.Second

// Dial opens a TCP connection to the proxy.
func Dial(ctx context.Context, address string) (net.Conn, error) {
	d := net.Dialer{Timeout: DialTimeout}
	return d.DialContext(ctx, "tcp", address)
}

// Deadline is ctx's deadline, or now+fallback when ctx has none.
func Deadline(ctx context.Context, fallback time.Duration) time.Time {
	if ctx != nil {
		if dl, ok := ctx.Deadline(); ok {
			return dl
		}
	}
	return time.Now().Add(fallback)
}

// Bound limits a handshake on conn to ctx's deadline (fallback without one)
// and aborts it when ctx is cancelled, so a proxy that accepts TCP and then
// stays silent cannot hang the caller. The returned func clears the
// deadline and reports ctx's error if ctx ended first.
func Bound(ctx context.Context, conn net.Conn, fallback time.Duration) func() error {
	_ = conn.SetDeadline(Deadline(ctx, fallback))
	stop := context.AfterFunc(ctx, func() { _ = conn.SetDeadline(time.Unix(1, 0)) })
	return func() error {
		if !stop() {
			return ctx.Err()
		}
		_ = conn.SetDeadline(time.Time{})
		return nil
	}
}
