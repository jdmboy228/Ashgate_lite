package capture

import (
	"context"
	"errors"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"ashgate-lite/internal/proxycore/capture/packet"
	"ashgate-lite/internal/proxycore/capture/process"
)

// Action is what the capture loop should do with a packet.
type Action int

const (
	// ActionPass reinjects the packet unchanged.
	ActionPass Action = iota
	// ActionModified reinjects the rewritten packet.
	ActionModified
	// ActionDrop discards the packet.
	ActionDrop
	// ActionUDP forwards the payload through the proxy and does not reinject.
	ActionUDP
	// ActionUnknown asks the capture loop to hold the packet briefly and retry.
	ActionUnknown
)

// Meta is capture metadata needed to inject a reply later.
type Meta struct {
	IfIdx    uint32
	SubIfIdx uint32
}

// Result is the classification of one packet.
type Result struct {
	Action   Action
	Inbound  bool
	IfIdx    uint32
	SubIfIdx uint32
	UDP      *UDPDatagram
}

// UDPDatagram is a proxied UDP payload. The capture loop passes it to ForwardUDP.
type UDPDatagram struct {
	Key      udpKey
	Payload  []byte
	RelayDst packet.Addr
	OrigDst  packet.Addr
}

type udpKey struct {
	Src packet.Addr
	Dst packet.Addr
}

// Resolver maps a local endpoint to the owning process.
type Resolver interface {
	Lookup(proto uint8, src packet.Addr) (process.Process, bool)
}

// Allocator reserves a local port used as the source of a redirected TCP handshake.
type Allocator interface {
	Reserve(ver int) (port uint16, release func(), err error)
}

// Injector delivers a locally generated reply to the operating system.
type Injector interface {
	InjectInbound(pkt []byte, ifIdx, subIfIdx uint32) error
}

// Options overrides production dependencies. Zero values use the defaults.
type Options struct {
	Resolver Resolver
	Alloc    Allocator
	Upstream Upstream
	Injector Injector
	SelfPID  uint32
	Now      func() time.Time
	SkipSelf bool
}

// Engine is safe for one packet goroutine plus the accept and UDP goroutines.
type Engine struct {
	alloc    Allocator
	up       Upstream
	resolver Resolver
	now      func() time.Time
	selfPID  uint32

	snap   atomic.Pointer[snapshot]
	bypass atomic.Pointer[map[string]struct{}]
	inj    atomic.Pointer[Injector]

	mu        sync.Mutex
	byClient  map[packet.Addr]*tcpFlow
	byAlloc   map[allocKey]*tcpFlow
	directTCP ageSet[packet.Addr]
	directUDP ageSet[udpKey]
	blockTCP  ageSet[packet.Addr]
	blockUDP  ageSet[udpKey]
	// fragDrop remembers datagrams whose first fragment was dropped, so
	// their later fragments (which carry no ports) are dropped too instead
	// of reaching the destination directly.
	fragDrop ageSet[fragKey]
	udp      map[udpKey]*udpFlow
	// stats are per-rule counters keyed by decision.key.
	stats map[string]*ruleStat
	// rotatedAt is when the direct/block caches last aged; see sweep.
	rotatedAt time.Time

	dnsSem chan struct{}
	dnsRR  atomic.Uint64
}

type fragKey struct {
	src, dst packet.Addr // ports zero
	id       uint32
}

func fragOf(h *packet.Header) fragKey {
	k := fragKey{src: h.Src, dst: h.Dst, id: h.FragID}
	k.src.Port, k.dst.Port = 0, 0
	return k
}

// dropFragment records h's datagram when h is a first fragment.
func (e *Engine) dropFragment(h *packet.Header) {
	if h.Frag {
		e.fragDrop.add(fragOf(h))
	}
}

type allocKey struct {
	ver  int
	port uint16
}

type snapshot struct {
	rules      *ruleSet
	carriesUDP map[string]bool
	bypass     []string
	relay4     packet.Addr
	relay6     packet.Addr
	dns        []packet.Addr
}

// NewEngine compiles plan and returns an engine.
func NewEngine(plan Plan, opt Options) (*Engine, error) {
	if opt.Upstream == nil {
		return nil, errors.New("capture: no upstream")
	}
	e := &Engine{
		alloc:     opt.Alloc,
		up:        opt.Upstream,
		resolver:  opt.Resolver,
		now:       opt.Now,
		byClient:  map[packet.Addr]*tcpFlow{},
		byAlloc:   map[allocKey]*tcpFlow{},
		directTCP: newAgeSet[packet.Addr](),
		directUDP: newAgeSet[udpKey](),
		blockTCP:  newAgeSet[packet.Addr](),
		blockUDP:  newAgeSet[udpKey](),
		fragDrop:  newAgeSet[fragKey](),
		udp:       map[udpKey]*udpFlow{},
		dnsSem:    make(chan struct{}, httpDNSParallel),
		stats:     map[string]*ruleStat{},
	}
	if e.alloc == nil {
		e.alloc = OSAllocator{}
	}
	if e.now == nil {
		e.now = time.Now
	}
	if opt.SelfPID != 0 {
		e.selfPID = opt.SelfPID
	} else if !opt.SkipSelf {
		e.selfPID = uint32(os.Getpid())
	}
	if opt.Injector != nil {
		e.SetInjector(opt.Injector)
	}
	if err := e.Apply(plan); err != nil {
		return nil, err
	}
	return e, nil
}

// SetInjector installs the reply path used by UDP and kill-switch resets.
func (e *Engine) SetInjector(inj Injector) {
	e.inj.Store(&inj)
}

func (e *Engine) injector() Injector {
	p := e.inj.Load()
	if p == nil || *p == nil {
		return nil
	}
	return *p
}

// Apply installs a new plan. Existing TCP flows keep their proxy.
func (e *Engine) Apply(plan Plan) error {
	snap, err := compile(plan)
	if err != nil {
		return err
	}
	e.snap.Store(snap)
	e.mu.Lock()
	e.directTCP = newAgeSet[packet.Addr]()
	e.directUDP = newAgeSet[udpKey]()
	e.blockTCP = newAgeSet[packet.Addr]()
	e.blockUDP = newAgeSet[udpKey]()
	e.mu.Unlock()
	return nil
}

// RefreshBypass resolves proxy hostnames so traffic to the proxy itself stays direct.
func (e *Engine) RefreshBypass(ctx context.Context) {
	snap := e.snap.Load()
	if snap == nil {
		return
	}
	m := map[string]struct{}{}
	for _, host := range snap.bypass {
		if ip := net.ParseIP(host); ip != nil {
			m[ip.String()] = struct{}{}
			continue
		}
		ctx2, cancel := context.WithTimeout(ctx, 5*time.Second)
		ips, err := net.DefaultResolver.LookupIP(ctx2, "ip", host)
		cancel()
		if err != nil {
			continue
		}
		for _, ip := range ips {
			m[ip.String()] = struct{}{}
		}
	}
	e.bypass.Store(&m)
}

func compile(plan Plan) (*snapshot, error) {
	r4, err := parseAddr(relay4, 4)
	if err != nil {
		return nil, err
	}
	r6, err := parseAddr(relay6, 6)
	if err != nil {
		return nil, err
	}
	dns := make([]packet.Addr, 0, len(dnsServers))
	for _, s := range dnsServers {
		a, err := parseAddr(s, 4)
		if err != nil {
			return nil, err
		}
		dns = append(dns, a)
	}
	return &snapshot{
		rules:      compileRules(plan),
		carriesUDP: plan.CarriesUDP,
		bypass:     append([]string(nil), plan.Bypass...),
		relay4:     r4,
		relay6:     r6,
		dns:        dns,
	}, nil
}

func parseAddr(addr string, ver int) (packet.Addr, error) {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return packet.Addr{}, err
	}
	port, err := net.LookupPort("tcp", portStr)
	if err != nil {
		return packet.Addr{}, err
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return packet.Addr{}, errors.New("capture: address must be an IP")
	}
	a := packet.FromIP(ip, uint16(port))
	if (ver == 4 && a.Len != 4) || (ver == 6 && a.Len != 16) {
		return packet.Addr{}, errors.New("capture: wrong IP version for " + addr)
	}
	return a, nil
}

// HandlePacket classifies one IPv4 or IPv6 packet.
// final is set when the capture loop has already held the packet and must decide.
func (e *Engine) HandlePacket(pkt []byte, meta Meta, final bool) Result {
	var h packet.Header
	if err := packet.Parse(pkt, &h); err != nil {
		return Result{Action: ActionPass}
	}
	if h.L4Off == 0 {
		// A later fragment carries no ports. Drop it when its first
		// fragment was dropped, or it would reach the destination directly.
		if h.Frag {
			e.mu.Lock()
			drop := e.fragDrop.has(fragOf(&h))
			e.mu.Unlock()
			if drop {
				return Result{Action: ActionDrop}
			}
		}
		return Result{Action: ActionPass}
	}
	e.mu.Lock()
	if res, done := e.fastPath(pkt, &h); done {
		e.mu.Unlock()
		return res
	}
	e.mu.Unlock()

	var proc process.Process
	found := false
	if e.resolver != nil {
		proc, found = e.resolver.Lookup(h.Proto, h.Src)
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	if res, done := e.fastPath(pkt, &h); done {
		return res
	}
	return e.decide(pkt, &h, meta, proc, found, final)
}

func (e *Engine) fastPath(pkt []byte, h *packet.Header) (Result, bool) {
	snap := e.snap.Load()
	if snap == nil {
		return Result{Action: ActionPass}, true
	}
	if h.Proto == packet.ProtoTCP {
		return e.fastTCP(pkt, h, snap)
	}
	if h.Proto == packet.ProtoUDP {
		return e.fastUDP(pkt, h)
	}
	return Result{}, false
}

func (e *Engine) fastTCP(pkt []byte, h *packet.Header, snap *snapshot) (Result, bool) {
	if res, ok := e.tcpReturn(pkt, h, snap); ok {
		return res, true
	}
	flow := e.byClient[h.Src]
	if h.SYN && !h.ACK {
		if flow != nil && flow.orig.Equal(h.Dst) {
			return e.rewriteForward(pkt, h, flow, snap), true
		}
		if flow != nil {
			e.dropFlowLocked(flow)
		}
		e.directTCP.del(h.Src)
		e.blockTCP.del(h.Src)
		return Result{}, false
	}
	switch {
	case flow != nil:
		return e.rewriteForward(pkt, h, flow, snap), true
	case e.directTCP.has(h.Src):
		return Result{Action: ActionPass}, true
	case e.blockTCP.has(h.Src):
		return Result{Action: ActionDrop}, true
	}
	return Result{}, false
}

func (e *Engine) fastUDP(pkt []byte, h *packet.Header) (Result, bool) {
	key := udpKey{Src: h.Src, Dst: h.Dst}
	if flow := e.udp[key]; flow != nil {
		if h.Frag {
			// A fragmented datagram cannot be relayed; forwarding the
			// first piece would send a truncated payload.
			e.dropFragment(h)
			return Result{Action: ActionDrop}, true
		}
		flow.touch(e.now())
		flow.stat.addUp(int64(h.PayloadLen))
		return Result{Action: ActionUDP, UDP: datagram(pkt, h, flow)}, true
	}
	if e.directUDP.has(key) {
		return Result{Action: ActionPass}, true
	}
	if e.blockUDP.has(key) {
		e.dropFragment(h)
		return Result{Action: ActionDrop}, true
	}
	return Result{}, false
}

func (e *Engine) decide(pkt []byte, h *packet.Header, meta Meta, proc process.Process, found, final bool) Result {
	snap := e.snap.Load()
	if h.Frag && h.PayloadOff == 0 {
		return Result{Action: ActionPass}
	}
	if e.destinationBypass(h.Dst) {
		e.rememberDirect(h)
		return Result{Action: ActionPass}
	}
	if !found {
		if !final && (h.Proto == packet.ProtoUDP || (h.SYN && !h.ACK)) {
			return Result{Action: ActionUnknown}
		}
		e.rememberDirect(h)
		return Result{Action: ActionPass}
	}
	if e.selfPID != 0 && proc.PID == e.selfPID {
		e.rememberDirect(h)
		return Result{Action: ActionPass}
	}
	dec := snap.rules.match(proc)
	if !dec.matched() || dec.direct {
		e.rememberDirect(h)
		return Result{Action: ActionPass}
	}
	stat := e.statFor(dec.key)
	if h.Proto == packet.ProtoTCP {
		if !(h.SYN && !h.ACK) {
			return e.resetExisting(h, meta)
		}
		return e.startTCP(pkt, h, meta, dec.proxy, snap, stat)
	}
	return e.startUDP(pkt, h, meta, dec.proxy, snap, stat)
}

func (e *Engine) startTCP(pkt []byte, h *packet.Header, meta Meta, proxyID string, snap *snapshot, stat *ruleStat) Result {
	if h.Frag {
		e.dropFragment(h)
		return Result{Action: ActionDrop}
	}
	port, release, err := e.alloc.Reserve(h.Ver)
	if err != nil {
		stat.fail(err.Error(), e.now())
		return Result{Action: ActionDrop}
	}
	flow := &tcpFlow{
		ver:      h.Ver,
		app:      h.Src,
		orig:     h.Dst,
		alloc:    port,
		release:  release,
		proxyID:  proxyID,
		ifIdx:    meta.IfIdx,
		subIfIdx: meta.SubIfIdx,
		created:  e.now(),
		live:     true,
		stat:     stat,
	}
	stat.flows.Add(1)
	e.byClient[h.Src] = flow
	e.byAlloc[allocKey{ver: h.Ver, port: port}] = flow
	return e.rewriteForward(pkt, h, flow, snap)
}

// resetExisting handles a TCP connection of a proxied application that was
// open before capture started. It cannot be moved to the proxy, so it is
// dropped (kill switch) and the application gets a reset, which makes it
// reconnect through the proxy instead of staying direct.
func (e *Engine) resetExisting(h *packet.Header, meta Meta) Result {
	e.rememberBlock(h)
	if inj := e.injector(); inj != nil && !h.RST {
		if rst, err := packet.BuildRST(h.Dst, h.Src, h.Ack); err == nil {
			_ = inj.InjectInbound(rst, meta.IfIdx, meta.SubIfIdx)
		}
	}
	return Result{Action: ActionDrop}
}

func (e *Engine) startUDP(pkt []byte, h *packet.Header, meta Meta, proxyID string, snap *snapshot, stat *ruleStat) Result {
	key := udpKey{Src: h.Src, Dst: h.Dst}
	if h.Frag {
		e.blockUDP.add(key)
		e.dropFragment(h)
		return Result{Action: ActionDrop}
	}
	if !snap.carriesUDP[proxyID] && h.Dst.Port != 53 {
		// The kill switch never lets UDP bypass a proxy that cannot carry it.
		e.blockUDP.add(key)
		return Result{Action: ActionDrop}
	}
	relay := h.Dst
	if h.Dst.Port == 53 {
		relay = e.pickDNS(snap)
	}
	flow := &udpFlow{
		key:     key,
		app:     h.Src,
		orig:    h.Dst,
		relay:   relay,
		proxyID: proxyID,
		viaTCP:  !snap.carriesUDP[proxyID],
		ifIdx:   meta.IfIdx,
		sub:     meta.SubIfIdx,
		live:    true,
		stat:    stat,
	}
	stat.flows.Add(1)
	stat.addUp(int64(h.PayloadLen))
	flow.touch(e.now())
	e.udp[key] = flow
	return Result{Action: ActionUDP, UDP: datagram(pkt, h, flow)}
}

func (e *Engine) pickDNS(snap *snapshot) packet.Addr {
	n := e.dnsRR.Add(1)
	return snap.dns[int(n)%len(snap.dns)]
}

func datagram(pkt []byte, h *packet.Header, f *udpFlow) *UDPDatagram {
	var payload []byte
	if h.PayloadLen > 0 && h.PayloadOff >= 0 && h.PayloadOff+h.PayloadLen <= len(pkt) {
		payload = append([]byte(nil), pkt[h.PayloadOff:h.PayloadOff+h.PayloadLen]...)
	}
	return &UDPDatagram{Key: f.key, Payload: payload, RelayDst: f.relay, OrigDst: f.orig}
}

func (e *Engine) tcpReturn(pkt []byte, h *packet.Header, snap *snapshot) (Result, bool) {
	relay := snap.relay6
	if h.Ver == 4 {
		relay = snap.relay4
	}
	if !h.Src.Equal(relay) {
		return Result{}, false
	}
	flow := e.byAlloc[allocKey{ver: h.Ver, port: h.Dst.Port}]
	if flow == nil {
		return Result{Action: ActionPass}, true
	}
	if err := packet.Rewrite(pkt, h, flow.orig, flow.app); err != nil {
		return Result{Action: ActionDrop}, true
	}
	return Result{Action: ActionModified, Inbound: true, IfIdx: flow.ifIdx, SubIfIdx: flow.subIfIdx}, true
}

func (e *Engine) rewriteForward(pkt []byte, h *packet.Header, flow *tcpFlow, snap *snapshot) Result {
	src := packet.FromIP(net.IPv6loopback, flow.alloc)
	dst := snap.relay6
	if h.Ver == 4 {
		src = packet.V4(127, 0, 0, 1, flow.alloc)
		dst = snap.relay4
	}
	if err := packet.Rewrite(pkt, h, src, dst); err != nil {
		return Result{Action: ActionDrop}
	}
	return Result{Action: ActionModified, IfIdx: flow.ifIdx, SubIfIdx: flow.subIfIdx}
}

func (e *Engine) rememberDirect(h *packet.Header) {
	if h.Proto == packet.ProtoTCP {
		e.directTCP.add(h.Src)
		return
	}
	e.directUDP.add(udpKey{Src: h.Src, Dst: h.Dst})
}

func (e *Engine) rememberBlock(h *packet.Header) {
	if h.Proto == packet.ProtoTCP {
		e.blockTCP.add(h.Src)
		return
	}
	e.blockUDP.add(udpKey{Src: h.Src, Dst: h.Dst})
}

func (e *Engine) destinationBypass(dst packet.Addr) bool {
	if packet.Loopback(dst) || packet.MulticastOrBroadcast(dst) || packet.LinkLocal(dst) {
		return true
	}
	m := e.bypass.Load()
	if m == nil {
		return false
	}
	_, ok := (*m)[dst.NetIP().String()]
	return ok
}
