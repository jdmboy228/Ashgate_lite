package proxycore

import (
	"context"
	"errors"
	"net"
	"time"

	"ashgate-lite/internal/proxycore/vless"
)

// probeTarget is the destination a health check opens through the proxy.
const probeTarget = "1.1.1.1:443"

const probeTimeout = 8 * time.Second

// PingResult is the outcome of one health check.
type PingResult struct {
	OK bool
	// Millis is the time to open a tunnel to the probe target.
	Millis int64
	// Timeout is set when the proxy did not answer in time.
	Timeout bool
	Err     error
}

// Ping opens a TCP tunnel to the probe target through p and reports how long
// it took. A VLESS proxy gets its own short-lived bridge, so a check never
// disturbs the bridge capture is using.
func Ping(ctx context.Context, p Proxy) PingResult {
	return pingTarget(ctx, p, probeTarget)
}

func pingTarget(ctx context.Context, p Proxy, target string) PingResult {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	host, port, err := splitTarget(target)
	if err != nil {
		return failed(err)
	}
	start := time.Now()
	bridge := ""
	if p.Type == VLESS {
		br, err := startBridge(ctx, p)
		if err != nil {
			return failed(err)
		}
		defer br.Close()
		bridge = br.Addr
	}
	conn, err := dialTCP(ctx, p, bridge, host, port)
	if err != nil {
		return failed(err)
	}
	_ = conn.Close()
	return PingResult{OK: true, Millis: max(time.Since(start).Milliseconds(), 1)}
}

func startBridge(ctx context.Context, p Proxy) (*vless.Bridge, error) {
	if p.VLESS == nil {
		return nil, ErrParse
	}
	br, err := vless.Listen(ctx, p.Address(), *p.VLESS)
	return br, vless.Explain(err)
}

func failed(err error) PingResult {
	var ne net.Error
	timeout := errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout()) || errors.Is(err, vless.ErrTimeout)
	return PingResult{Timeout: timeout, Err: err}
}

func splitTarget(target string) (string, uint16, error) {
	host, portStr, err := net.SplitHostPort(target)
	if err != nil {
		return "", 0, err
	}
	port, err := net.LookupPort("tcp", portStr)
	if err != nil {
		return "", 0, err
	}
	return host, uint16(port), nil
}
