package capture

import (
	"context"
	"io"
	"net"
	"sync"
	"time"

	"ashgate-lite/internal/proxycore/capture/packet"
)

const tcpDialTimeout = 20 * time.Second

type tcpFlow struct {
	ver      int
	app      packet.Addr
	orig     packet.Addr
	alloc    uint16
	release  func()
	proxyID  string
	ifIdx    uint32
	subIfIdx uint32
	created  time.Time
	conn     net.Conn
	live     bool
	stat     *ruleStat
	mu       sync.Mutex
}

// ServeConn looks up the NAT entry for an accepted local connection and relays it.
func (e *Engine) ServeConn(conn net.Conn) {
	ta, ok := conn.RemoteAddr().(*net.TCPAddr)
	if !ok {
		conn.Close()
		return
	}
	ver := 4
	if ta.IP.To4() == nil {
		ver = 6
	}
	e.mu.Lock()
	flow := e.byAlloc[allocKey{ver: ver, port: uint16(ta.Port)}]
	if flow != nil {
		flow.mu.Lock()
		flow.conn = conn
		flow.mu.Unlock()
	}
	e.mu.Unlock()
	if flow == nil {
		conn.Close()
		return
	}
	e.serveFlow(conn, flow)
}

func (e *Engine) serveFlow(conn net.Conn, flow *tcpFlow) {
	defer conn.Close()
	defer e.finishTCP(flow)

	ctx, cancel := context.WithTimeout(context.Background(), tcpDialTimeout)
	upstream, err := e.up.DialTCP(ctx, flow.proxyID, flow.orig.NetIP().String(), flow.orig.Port)
	cancel()
	if err != nil {
		flow.stat.fail(err.Error(), e.now())
		return
	}
	defer upstream.Close()
	splice(conn, upstream, flow.stat)
}

func (e *Engine) finishTCP(flow *tcpFlow) {
	e.mu.Lock()
	e.dropFlowLocked(flow)
	e.mu.Unlock()
}

func (e *Engine) dropFlowLocked(flow *tcpFlow) {
	if cur, ok := e.byClient[flow.app]; ok && cur == flow {
		delete(e.byClient, flow.app)
	}
	key := allocKey{ver: flow.ver, port: flow.alloc}
	if cur, ok := e.byAlloc[key]; ok && cur == flow {
		delete(e.byAlloc, key)
	}
	if flow.release != nil {
		flow.release()
		flow.release = nil
	}
	if flow.live {
		flow.live = false
		flow.stat.closeFlow()
	}
	flow.mu.Lock()
	c := flow.conn
	flow.conn = nil
	flow.mu.Unlock()
	if c != nil {
		// Unblock splice without waiting for it. Close is safe if serveFlow
		// already returned and closed the same connection.
		_ = c.Close()
	}
}

// countWriter adds relayed bytes to the rule's counters as they move, so
// speeds update during long transfers instead of when the connection closes.
type countWriter struct {
	w   io.Writer
	add func(int64)
}

func (cw countWriter) Write(p []byte) (int, error) {
	n, err := cw.w.Write(p)
	cw.add(int64(n))
	return n, err
}

func splice(app, upstream net.Conn, stat *ruleStat) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(countWriter{upstream, stat.addUp}, app)
		closeWrite(upstream)
	}()
	go func() {
		defer wg.Done()
		_, _ = io.Copy(countWriter{app, stat.addDown}, upstream)
		closeWrite(app)
	}()
	wg.Wait()
}

func closeWrite(c net.Conn) {
	if cw, ok := c.(interface{ CloseWrite() error }); ok {
		_ = cw.CloseWrite()
		return
	}
	_ = c.Close()
}

// OSAllocator reserves a localhost port by binding it for the life of the flow.
type OSAllocator struct{}

// Reserve binds 127.0.0.1 or ::1 on an ephemeral port and returns that port.
func (OSAllocator) Reserve(ver int) (uint16, func(), error) {
	network, host := "tcp4", "127.0.0.1"
	if ver == 6 {
		network, host = "tcp6", "::1"
	}
	ln, err := net.Listen(network, net.JoinHostPort(host, "0"))
	if err != nil {
		return 0, nil, err
	}
	port := ln.Addr().(*net.TCPAddr).Port
	return uint16(port), func() { _ = ln.Close() }, nil
}
