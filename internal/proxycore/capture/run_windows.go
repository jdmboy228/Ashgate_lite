//go:build windows

package capture

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"syscall"
	"time"

	"ashgate-lite/internal/proxycore/capture/process"
	"ashgate-lite/internal/proxycore/capture/windivert"
	"golang.org/x/sys/windows"
)

const (
	holdFor    = 50 * time.Millisecond
	holdTick   = 15 * time.Millisecond
	maxPending = 256
	udpWorkers = 4
)

// Start loads WinDivert, diverts outbound TCP/UDP, and relays flows selected
// by plan through up. Other applications are reinjected unchanged.
func Start(parent context.Context, plan Plan, up Upstream) (sess *Session, err error) {
	defer func() {
		if err != nil {
			err = annotateStart(err)
		}
	}()
	if !elevated() {
		return nil, errNotElevated
	}
	mutex, err := acquireEngine()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(parent)
	ok := false
	defer func() {
		if !ok {
			cancel()
			windows.CloseHandle(mutex)
		}
	}()
	res := process.New()
	eng, err := NewEngine(plan, Options{Resolver: res, Upstream: up})
	if err != nil {
		return nil, err
	}
	h, err := openHandles()
	if err != nil {
		return nil, err
	}
	defer func() {
		if !ok {
			h.close()
		}
	}()
	div := &divert{h: h.net}
	eng.SetInjector(div)
	ln4, err := net.Listen("tcp", relay4)
	if err != nil {
		return nil, fmt.Errorf("локальный приёмник %s: %w", relay4, err)
	}
	// IPv6 is optional: without it only IPv4 flows are proxied.
	ln6, _ := net.Listen("tcp", relay6)

	s := &Session{eng: eng, cancel: cancel, done: make(chan struct{})}
	var wg sync.WaitGroup
	runLoops(ctx, cancel, s, h, div, res, &wg)
	for _, ln := range []net.Listener{ln4, ln6} {
		if ln != nil {
			wg.Add(1)
			go acceptLoop(ln, eng, &wg)
		}
	}
	go func() {
		<-ctx.Done()
		h.shutdown()
		_ = ln4.Close()
		if ln6 != nil {
			_ = ln6.Close()
		}
		wg.Wait()
		res.Close()
		h.close()
		windows.CloseHandle(mutex)
		close(s.done)
	}()
	ok = true
	return s, nil
}

// handles are the three WinDivert handles of one capture.
type handles struct {
	net, sock, flow windivert.Handle
}

func openHandles() (*handles, error) {
	netH, err := windivert.Open("outbound and !impostor and (tcp or udp)", windivert.LayerNetwork, 0, 0)
	if err != nil {
		return nil, err
	}
	sockH, err := windivert.Open(
		"(tcp or udp) and (event == BIND or event == CONNECT or event == ACCEPT or event == CLOSE)",
		windivert.LayerSocket, 0, windivert.FlagSniff|windivert.FlagRecvOnly)
	if err != nil {
		netH.Close()
		return nil, err
	}
	flowH, err := windivert.Open("(tcp or udp) and event == ESTABLISHED", windivert.LayerFlow, 0, windivert.FlagSniff|windivert.FlagRecvOnly)
	if err != nil {
		netH.Close()
		sockH.Close()
		return nil, err
	}
	_ = netH.ConfigureQueue()
	return &handles{net: netH, sock: sockH, flow: flowH}, nil
}

func (h *handles) shutdown() {
	_ = h.net.Shutdown(windivert.ShutdownBoth)
	_ = h.sock.Shutdown(windivert.ShutdownBoth)
	_ = h.flow.Shutdown(windivert.ShutdownBoth)
}

func (h *handles) close() {
	_ = h.net.Close()
	_ = h.sock.Close()
	_ = h.flow.Close()
}

// runLoops starts the socket-event readers, the packet pipeline, the UDP
// workers, the janitor and the bypass refresh. They all stop with ctx.
func runLoops(ctx context.Context, cancel context.CancelFunc, s *Session, h *handles, div *divert, res *process.Resolver, wg *sync.WaitGroup) {
	eng := s.eng
	udpCh := make(chan UDPDatagram, 256)
	for i := 0; i < udpWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for d := range udpCh {
				_ = eng.ForwardUDP(ctx, d)
			}
		}()
	}
	wg.Add(2)
	go readEvents(ctx, h.sock, res, wg)
	go readEvents(ctx, h.flow, res, wg)
	pkts := make(chan netMsg, 512)
	wg.Add(1)
	go recvNet(ctx, cancel, s, h.net, pkts, wg)
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(udpCh)
		consume(ctx, eng, div, pkts, udpCh)
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		eng.Janitor(ctx)
	}()
	wg.Add(1)
	go refreshLoop(ctx, eng, wg)
}

func readEvents(ctx context.Context, h windivert.Handle, res *process.Resolver, wg *sync.WaitGroup) {
	defer wg.Done()
	var addr windivert.Address
	for {
		if ctx.Err() != nil {
			return
		}
		if _, err := h.Recv(nil, &addr); err != nil {
			return
		}
		ev := addr.Event()
		closing := ev == windivert.EventSocketClose || ev == windivert.EventFlowDeleted
		res.Observe(addr.Protocol(), addr.Local(), addr.ProcessID(), closing)
	}
}

type netMsg struct {
	pkt  []byte
	addr windivert.Address
}

type held struct {
	pkt  []byte
	addr windivert.Address
	at   time.Time
}

func recvNet(ctx context.Context, cancel context.CancelFunc, s *Session, h windivert.Handle, out chan<- netMsg, wg *sync.WaitGroup) {
	defer wg.Done()
	defer close(out)
	buf := make([]byte, 65535)
	var addr windivert.Address
	for {
		n, err := h.Recv(buf, &addr)
		if err != nil {
			if ctx.Err() == nil && !windivert.IsNoData(err) {
				s.setErr(fmt.Errorf("WinDivertRecv: %s", windivert.FormatError(err)))
				cancel()
			}
			return
		}
		pkt := make([]byte, n)
		copy(pkt, buf[:n])
		msg := netMsg{pkt: pkt, addr: addr}
		select {
		case out <- msg:
		case <-ctx.Done():
			return
		}
	}
}

func consume(ctx context.Context, eng *Engine, div *divert, in <-chan netMsg, udpCh chan<- UDPDatagram) {
	tick := time.NewTicker(holdTick)
	defer tick.Stop()
	var pending []held
	flush := func(force bool) {
		now := time.Now()
		keep := pending[:0]
		for _, h := range pending {
			if force || now.Sub(h.at) >= holdFor {
				queue(eng, div, udpCh, &keep, h.pkt, h.addr, true)
				continue
			}
			// The owning socket's event has often arrived by now. Decide
			// on this tick instead of always waiting the full holdFor.
			meta := Meta{IfIdx: h.addr.IfIdx(), SubIfIdx: h.addr.SubIfIdx()}
			res := eng.HandlePacket(h.pkt, meta, false)
			if res.Action == ActionUnknown {
				keep = append(keep, h)
				continue
			}
			apply(div, udpCh, h.pkt, h.addr, res)
		}
		pending = keep
	}
	for {
		select {
		case <-ctx.Done():
			flush(true)
			return
		case msg, ok := <-in:
			if !ok {
				flush(true)
				return
			}
			if len(pending) >= maxPending {
				flush(true)
			}
			queue(eng, div, udpCh, &pending, msg.pkt, msg.addr, false)
		case <-tick.C:
			flush(false)
		}
	}
}

func queue(eng *Engine, div *divert, udpCh chan<- UDPDatagram, pending *[]held, pkt []byte, addr windivert.Address, final bool) {
	meta := Meta{IfIdx: addr.IfIdx(), SubIfIdx: addr.SubIfIdx()}
	res := eng.HandlePacket(pkt, meta, final)
	if res.Action == ActionUnknown && !final && len(*pending) < maxPending {
		*pending = append(*pending, held{pkt: pkt, addr: addr, at: time.Now()})
		return
	}
	if res.Action == ActionUnknown {
		res = eng.HandlePacket(pkt, meta, true)
		if res.Action == ActionUnknown {
			res.Action = ActionPass
		}
	}
	apply(div, udpCh, pkt, addr, res)
}

func apply(div *divert, udpCh chan<- UDPDatagram, pkt []byte, addr windivert.Address, res Result) {
	switch res.Action {
	case ActionDrop:
		return
	case ActionUDP:
		if res.UDP == nil {
			return
		}
		select {
		case udpCh <- *res.UDP:
		default: // queue full: drop, the application retries
		}
		return
	case ActionModified:
		// Impostor stops this handle from taking the packet again.
		// WinDivert decrements TTL on impostor packets; that is acceptable
		// because the packet was rewritten. Unmodified packets keep the
		// address WinDivertRecv returned, so their TTL and checksum offload
		// flags stay as they were.
		addr.SetImpostor(true)
		if res.Inbound {
			addr.SetOutbound(false)
			addr.SetIfIdx(res.IfIdx)
			addr.SetSubIfIdx(res.SubIfIdx)
		}
		windivert.MarkValidChecksums(&addr, pkt)
		_ = div.send(pkt, &addr)
	default:
		_ = div.send(pkt, &addr)
	}
}

func acceptLoop(ln net.Listener, eng *Engine, wg *sync.WaitGroup) {
	defer wg.Done()
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		go eng.ServeConn(c)
	}
}

func refreshLoop(ctx context.Context, eng *Engine, wg *sync.WaitGroup) {
	defer wg.Done()
	eng.RefreshBypass(ctx)
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			eng.RefreshBypass(ctx)
		}
	}
}

type divert struct {
	h  windivert.Handle
	mu sync.Mutex
}

func (d *divert) send(pkt []byte, addr *windivert.Address) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.h.Send(pkt, addr)
}

func (d *divert) InjectInbound(pkt []byte, ifIdx, subIfIdx uint32) error {
	var addr windivert.Address
	addr.SetLayer(windivert.LayerNetwork)
	addr.SetEvent(windivert.EventNetworkPacket)
	addr.SetOutbound(false)
	addr.SetImpostor(true)
	addr.SetIfIdx(ifIdx)
	addr.SetSubIfIdx(subIfIdx)
	windivert.MarkValidChecksums(&addr, pkt)
	return d.send(pkt, &addr)
}

func acquireEngine() (windows.Handle, error) {
	name, err := windows.UTF16PtrFromString(`Global\AshgateLiteEngine`)
	if err != nil {
		return 0, err
	}
	h, err := windows.CreateMutex(nil, false, name)
	if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		if h != 0 {
			windows.CloseHandle(h)
		}
		return 0, errors.New("движок Ashgate Lite уже запущен")
	}
	if err != nil {
		return 0, err
	}
	if h == 0 {
		return 0, errors.New("не удалось создать мьютекс движка")
	}
	return h, nil
}

var errNotElevated = errors.New("Ashgate Lite запущен без прав администратора.")

func elevated() bool {
	token, err := windows.OpenCurrentProcessToken()
	if err != nil {
		return false
	}
	defer token.Close()
	return token.IsElevated()
}

// annotateStart turns driver failures into a message the window can show.
func annotateStart(err error) error {
	if errors.Is(err, errNotElevated) {
		return err
	}
	msg := err.Error()
	if accessDenied(err) {
		msg = windivert.FormatError(syscall.Errno(5))
	}
	if !elevated() {
		msg = errNotElevated.Error() + " " + msg
	}
	if msg == err.Error() {
		return err
	}
	return errors.New(msg)
}

func accessDenied(err error) bool {
	var n syscall.Errno
	if errors.As(err, &n) && n == syscall.Errno(5) {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "access is denied")
}
