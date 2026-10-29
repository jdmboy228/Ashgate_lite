package capture

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"ashgate-lite/internal/proxycore/capture/packet"
)

type udpFlow struct {
	key     udpKey
	app     packet.Addr
	orig    packet.Addr
	relay   packet.Addr
	proxyID string
	// viaTCP sends DNS as DNS-over-TCP through a proxy without UDP.
	viaTCP bool
	ifIdx  uint32
	sub    uint32
	last   atomic.Int64
	live   bool
	closed bool
	stat   *ruleStat
	sess   UDPSession
	// failedAt is the last failed DialUDP. While the proxy is down, packets
	// within udpRetry of it are dropped instead of each starting a new
	// association.
	failedAt time.Time
	mu       sync.Mutex
}

const (
	udpRetry = 3 * time.Second
	flowIdle = 30 * time.Second // idle UDP flows and unaccepted TCP handshakes
	// httpDNSParallel bounds concurrent DNS-over-TCP queries. Each one is a
	// full TCP round trip, so running them inline would let a few slow
	// lookups stall every proxied UDP flow.
	httpDNSParallel = 32
	// cacheAge is how often the direct/block caches age. A flow idle for one
	// to two intervals is classified again on its next packet, which gives
	// the same answer; the caches no longer grow with every connection.
	cacheAge = 2 * time.Minute
)

func (f *udpFlow) touch(t time.Time) {
	f.last.Store(t.UnixNano())
}

// ForwardUDP sends one datagram through the proxy and injects replies.
func (e *Engine) ForwardUDP(ctx context.Context, d UDPDatagram) error {
	e.mu.Lock()
	flow := e.udp[d.Key]
	e.mu.Unlock()
	if flow == nil {
		return nil
	}
	flow.touch(e.now())
	if !flow.viaTCP {
		return e.forwardSOCKS(ctx, flow, d)
	}
	select {
	case e.dnsSem <- struct{}{}:
		go func() {
			defer func() { <-e.dnsSem }()
			_ = e.forwardDNSOverTCP(ctx, flow, d.Payload)
		}()
		return nil
	default:
		return e.forwardDNSOverTCP(ctx, flow, d.Payload)
	}
}

func (e *Engine) forwardDNSOverTCP(ctx context.Context, flow *udpFlow, payload []byte) error {
	resp, err := e.up.DNSQuery(ctx, flow.proxyID, flow.relay.String(), payload)
	if err != nil {
		flow.stat.fail(err.Error(), e.now())
		return err
	}
	flow.stat.addDown(int64(len(resp)))
	return e.injectUDP(flow, resp)
}

func (e *Engine) forwardSOCKS(ctx context.Context, flow *udpFlow, d UDPDatagram) error {
	flow.mu.Lock()
	defer flow.mu.Unlock()
	if flow.closed {
		return nil
	}
	if flow.sess == nil {
		if !flow.failedAt.IsZero() && e.now().Sub(flow.failedAt) < udpRetry {
			return nil
		}
		sess, err := e.up.DialUDP(ctx, flow.proxyID)
		if err != nil {
			flow.stat.fail(err.Error(), e.now())
			flow.failedAt = e.now()
			return err
		}
		flow.sess = sess
		go e.readUDP(flow, sess)
	}
	dst := &net.UDPAddr{IP: flow.relay.NetIP(), Port: int(flow.relay.Port)}
	return flow.sess.Send(ctx, dst, d.Payload)
}

func (e *Engine) readUDP(flow *udpFlow, sess UDPSession) {
	defer e.retireUDP(flow)
	for {
		_, payload, err := sess.Recv(context.Background())
		if err != nil {
			return
		}
		flow.stat.addDown(int64(len(payload)))
		flow.touch(e.now())
		if err := e.injectUDP(flow, payload); err != nil {
			return
		}
	}
}

func (e *Engine) injectUDP(flow *udpFlow, payload []byte) error {
	inj := e.injector()
	if inj == nil {
		return nil
	}
	pkt, err := packet.BuildUDP(flow.orig, flow.app, payload)
	if err != nil {
		return err
	}
	return inj.InjectInbound(pkt, flow.ifIdx, flow.sub)
}

func (e *Engine) retireUDP(flow *udpFlow) {
	flow.mu.Lock()
	if flow.closed {
		flow.mu.Unlock()
		return
	}
	flow.closed = true
	sess := flow.sess
	flow.sess = nil
	flow.mu.Unlock()
	if sess != nil {
		_ = sess.Close()
	}
	e.mu.Lock()
	if cur, ok := e.udp[flow.key]; ok && cur == flow {
		delete(e.udp, flow.key)
	}
	if flow.live {
		flow.live = false
		flow.stat.closeFlow()
	}
	e.mu.Unlock()
}

// closeUDP ends a flow: closing its session makes readUDP retire it.
func (e *Engine) closeUDP(f *udpFlow) {
	f.mu.Lock()
	sess := f.sess
	f.mu.Unlock()
	if sess != nil {
		_ = sess.Close()
		return
	}
	e.retireUDP(f)
}

// Janitor closes idle UDP flows and TCP handshakes that never completed.
func (e *Engine) Janitor(ctx context.Context) {
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			e.closeAll()
			return
		case <-tick.C:
			e.sweep(e.now())
		}
	}
}

func (e *Engine) sweep(now time.Time) {
	e.mu.Lock()
	if e.rotatedAt.IsZero() {
		e.rotatedAt = now
	} else if now.Sub(e.rotatedAt) >= cacheAge {
		e.rotatedAt = now
		e.directTCP.rotate()
		e.directUDP.rotate()
		e.blockTCP.rotate()
		e.blockUDP.rotate()
		e.fragDrop.rotate()
	}
	for _, f := range e.byClient {
		f.mu.Lock()
		accepted := f.conn != nil
		f.mu.Unlock()
		if !accepted && now.Sub(f.created) > flowIdle {
			e.dropFlowLocked(f)
		}
	}
	var stale []*udpFlow
	for _, f := range e.udp {
		if now.Sub(time.Unix(0, f.last.Load())) > flowIdle {
			stale = append(stale, f)
		}
	}
	e.mu.Unlock()
	for _, f := range stale {
		e.closeUDP(f)
	}
}

func (e *Engine) closeAll() {
	e.mu.Lock()
	for _, f := range e.byClient {
		e.dropFlowLocked(f)
	}
	udps := make([]*udpFlow, 0, len(e.udp))
	for _, f := range e.udp {
		udps = append(udps, f)
	}
	e.mu.Unlock()
	for _, f := range udps {
		e.closeUDP(f)
	}
}
