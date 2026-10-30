package capture

import (
	"context"
	"io"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	"ashgate-lite/internal/proxycore/capture/packet"
	"ashgate-lite/internal/proxycore/capture/process"
	"ashgate-lite/internal/proxycore/httpproxy"
	"ashgate-lite/internal/proxycore/internal/netx"
	"ashgate-lite/internal/proxycore/socks5"
	"ashgate-lite/internal/proxycore/testkit"
)

const (
	curlPath    = `C:\Apps\curl.exe`
	browserPath = `C:\Apps\browser.exe`
)

type fixedRes struct {
	p  process.Process
	ok bool
}

func (f fixedRes) Lookup(uint8, packet.Addr) (process.Process, bool) { return f.p, f.ok }

func proc(path string) fixedRes {
	return fixedRes{p: process.Process{PID: 42, Path: path, Name: process.Base(path)}, ok: true}
}

// fakeUp dials TCP to a fixed target and talks to real test proxies for UDP and DNS.
type fakeUp struct {
	target  string
	socks   string
	http    string
	mu      sync.Mutex
	got     []string
	udpFail error
	udpTry  int
}

func (f *fakeUp) DialTCP(ctx context.Context, _ string, host string, port uint16) (net.Conn, error) {
	f.mu.Lock()
	f.got = append(f.got, net.JoinHostPort(host, strconv.Itoa(int(port))))
	f.mu.Unlock()
	var d net.Dialer
	return d.DialContext(ctx, "tcp", f.target)
}

func (f *fakeUp) DialUDP(ctx context.Context, _ string) (UDPSession, error) {
	f.mu.Lock()
	f.udpTry++
	f.mu.Unlock()
	if f.udpFail != nil {
		return nil, f.udpFail
	}
	return socks5.Associate(ctx, netx.Server{Address: f.socks})
}

func (f *fakeUp) DNSQuery(ctx context.Context, _ string, server string, query []byte) ([]byte, error) {
	return httpproxy.DNSQuery(ctx, netx.Server{Address: f.http}, server, query)
}

type memInj struct {
	mu   sync.Mutex
	pkts [][]byte
}

func (m *memInj) InjectInbound(pkt []byte, _, _ uint32) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pkts = append(m.pkts, append([]byte(nil), pkt...))
	return nil
}

func (m *memInj) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.pkts)
}

func (m *memInj) packet(i int) []byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]byte(nil), m.pkts[i]...)
}

func (m *memInj) wait(t *testing.T) []byte {
	t.Helper()
	for deadline := time.Now().Add(3 * time.Second); m.count() == 0 && time.Now().Before(deadline); {
		time.Sleep(10 * time.Millisecond)
	}
	if m.count() == 0 {
		t.Fatal("nothing injected")
	}
	return m.packet(0)
}

func testPlan() Plan {
	return Plan{
		Rules: []Rule{
			{Path: curlPath, ProxyID: "socks"},
			{Path: `C:\Apps\direct.exe`},
			{Path: browserPath, ProxyID: "web"},
		},
		CarriesUDP: map[string]bool{"socks": true, "web": false},
	}
}

func newEngine(t *testing.T, plan Plan, res Resolver, up Upstream, inj Injector) *Engine {
	t.Helper()
	e, err := NewEngine(plan, Options{Resolver: res, Alloc: &seqAlloc{}, Upstream: up, Injector: inj, SkipSelf: true})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func echoServer(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				_, _ = io.Copy(c, c)
			}()
		}
	}()
	return ln.Addr().String()
}

func TestTCPRedirectAndRelay(t *testing.T) {
	up := &fakeUp{target: echoServer(t)}
	e := newEngine(t, testPlan(), proc(curlPath), up, nil)

	app := packet.V4(10, 0, 0, 8, 41000)
	dst := packet.V4(9, 9, 9, 9, 443)
	pkt, _ := packet.BuildTCP(app, dst, 0x02)
	original := append([]byte(nil), pkt...)
	res := e.HandlePacket(pkt, Meta{IfIdx: 7}, true)
	if res.Action != ActionModified || res.Inbound {
		t.Fatalf("syn result %+v", res)
	}
	if err := packet.ValidChecksums(pkt); err != nil {
		t.Fatal(err)
	}
	var h packet.Header
	if err := packet.Parse(pkt, &h); err != nil {
		t.Fatal(err)
	}
	if h.Src.IP[0] != 127 || h.Dst.Port != 34020 || h.Src.Port < 20000 {
		t.Fatalf("rewritten header %+v", h)
	}
	alloc := h.Src.Port

	// A retransmitted SYN keeps the same allocation.
	again := append([]byte(nil), original...)
	e.HandlePacket(again, Meta{IfIdx: 7}, true)
	var h2 packet.Header
	if err := packet.Parse(again, &h2); err != nil || h2.Src.Port != alloc {
		t.Fatalf("retransmit port %d want %d (%v)", h2.Src.Port, alloc, err)
	}

	// Return path: the local relay's reply becomes the real server's.
	ret, _ := packet.BuildTCP(packet.V4(127, 0, 0, 1, 34020), packet.V4(127, 0, 0, 1, alloc), 0x12)
	back := e.HandlePacket(ret, Meta{}, true)
	if back.Action != ActionModified || !back.Inbound || back.IfIdx != 7 {
		t.Fatalf("return %+v", back)
	}
	var hr packet.Header
	if err := packet.Parse(ret, &hr); err != nil || hr.Src.Port != 443 || hr.Dst.Port != 41000 || hr.Src.IP[0] != 9 {
		t.Fatalf("restored %+v %v", hr, err)
	}

	relayAccepted(t, e, alloc)
	if len(up.got) != 1 || up.got[0] != "9.9.9.9:443" {
		t.Fatalf("dialed %v", up.got)
	}
	st := e.Stats()[`c:\apps\curl.exe`]
	if st.Up != 4 || st.Down != 4 || st.Flows != 0 {
		t.Fatalf("stats %+v", st)
	}
}

// relayAccepted plays the application side of a redirected connection.
func relayAccepted(t *testing.T, e *Engine, alloc uint16) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	served := make(chan struct{})
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		e.ServeConn(c)
		close(served)
	}()
	d := net.Dialer{LocalAddr: &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: int(alloc)}, Timeout: 2 * time.Second}
	conn, err := d.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4)
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.ReadFull(conn, buf); err != nil || string(buf) != "ping" {
		t.Fatalf("echo %q %v", buf, err)
	}
	conn.Close()
	select {
	case <-served:
	case <-time.After(3 * time.Second):
		t.Fatal("relay did not finish")
	}
}

func TestDirectUnmatchedAndSelf(t *testing.T) {
	for name, res := range map[string]fixedRes{
		"explicit direct": proc(`C:\Apps\direct.exe`),
		"unmatched":       proc(`C:\Windows\notepad.exe`),
	} {
		e := newEngine(t, testPlan(), res, &fakeUp{}, nil)
		pkt, _ := packet.BuildTCP(packet.V4(10, 0, 0, 2, 3000), packet.V4(1, 2, 3, 4, 80), 0x02)
		raw := append([]byte(nil), pkt...)
		if e.HandlePacket(pkt, Meta{}, true).Action != ActionPass || string(pkt) != string(raw) {
			t.Fatalf("%s: packet was not passed unchanged", name)
		}
	}
	self, _ := NewEngine(testPlan(), Options{Resolver: proc(curlPath), Alloc: &seqAlloc{}, Upstream: &fakeUp{}, SelfPID: 42})
	pkt, _ := packet.BuildTCP(packet.V4(10, 0, 0, 4, 3002), packet.V4(1, 1, 1, 1, 443), 0x02)
	if self.HandlePacket(pkt, Meta{}, true).Action != ActionPass {
		t.Fatal("own traffic must stay direct")
	}
}

func TestDefaultRouteProxiesOthers(t *testing.T) {
	plan := testPlan()
	plan.Default = "socks"
	e := newEngine(t, plan, proc(`C:\Windows\notepad.exe`), &fakeUp{}, nil)
	pkt, _ := packet.BuildTCP(packet.V4(10, 0, 0, 2, 3000), packet.V4(1, 2, 3, 4, 443), 0x02)
	if res := e.HandlePacket(pkt, Meta{}, true); res.Action != ActionModified {
		t.Fatalf("other app went %v", res.Action)
	}
	if st := e.Stats()[OthersKey]; st.Flows != 1 {
		t.Fatalf("others stats %+v", e.Stats())
	}
}

func TestUnknownThenPass(t *testing.T) {
	e := newEngine(t, testPlan(), fixedRes{}, &fakeUp{}, nil)
	pkt, _ := packet.BuildTCP(packet.V4(10, 0, 0, 9, 2222), packet.V4(1, 1, 1, 1, 443), 0x02)
	if e.HandlePacket(append([]byte(nil), pkt...), Meta{}, false).Action != ActionUnknown {
		t.Fatal("expected hold")
	}
	if e.HandlePacket(pkt, Meta{}, true).Action != ActionPass {
		t.Fatal("expected pass")
	}
}

func TestLoopbackAndProxyServerBypass(t *testing.T) {
	plan := testPlan()
	plan.Bypass = []string{"203.0.113.5"}
	e := newEngine(t, plan, proc(curlPath), &fakeUp{}, nil)
	e.RefreshBypass(context.Background())
	for _, dst := range []packet.Addr{packet.V4(127, 0, 0, 1, 80), packet.V4(203, 0, 113, 5, 1080)} {
		pkt, _ := packet.BuildTCP(packet.V4(10, 0, 0, 1, 50), dst, 0x02)
		if e.HandlePacket(pkt, Meta{}, true).Action != ActionPass {
			t.Fatalf("%s should bypass", dst)
		}
	}
}

func TestHTTPProxyBlocksUDPAndTunnelsDNS(t *testing.T) {
	px, err := testkit.ListenHTTP("", "")
	if err != nil {
		t.Fatal(err)
	}
	defer px.Close()
	inj := &memInj{}
	e := newEngine(t, testPlan(), proc(browserPath), &fakeUp{http: px.Addr}, inj)

	for i := 0; i < 2; i++ {
		quic, _ := packet.BuildUDP(packet.V4(10, 0, 0, 4, 4444), packet.V4(1, 2, 3, 4, 443), []byte("quic"))
		if res := e.HandlePacket(quic, Meta{}, true); res.Action != ActionDrop {
			t.Fatalf("udp leaked past an http proxy: %v", res.Action)
		}
	}

	dns, _ := packet.BuildUDP(packet.V4(10, 0, 0, 4, 5353), packet.V4(192, 168, 1, 1, 53), []byte{0x12, 0x34, 0x01, 0x00})
	res := e.HandlePacket(dns, Meta{IfIdx: 4}, true)
	if res.Action != ActionUDP || res.UDP == nil || res.UDP.OrigDst.String() != "192.168.1.1:53" {
		t.Fatalf("dns %+v", res)
	}
	if relay := res.UDP.RelayDst.String(); relay != "1.1.1.1:53" && relay != "8.8.8.8:53" {
		t.Fatalf("dns relay %s", relay)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := e.ForwardUDP(ctx, *res.UDP); err != nil {
		t.Fatal(err)
	}
	raw := inj.wait(t)
	var got packet.Header
	if err := packet.Parse(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Src.String() != "192.168.1.1:53" || got.Dst.Port != 5353 || raw[got.PayloadOff+2]&0x80 == 0 {
		t.Fatalf("injected header %+v payload %x", got, raw[got.PayloadOff:])
	}
}

func TestSOCKSUDP(t *testing.T) {
	socks, err := testkit.ListenSOCKS("", "")
	if err != nil {
		t.Fatal(err)
	}
	defer socks.Close()
	inj := &memInj{}
	e := newEngine(t, testPlan(), proc(curlPath), &fakeUp{socks: socks.Addr}, inj)
	payload := []byte{0xde, 0xad}
	pkt, _ := packet.BuildUDP(packet.V4(10, 9, 8, 7, 1900), packet.V4(9, 9, 9, 9, 3478), payload)
	res := e.HandlePacket(pkt, Meta{IfIdx: 3, SubIfIdx: 1}, true)
	if res.Action != ActionUDP {
		t.Fatal(res.Action)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := e.ForwardUDP(ctx, *res.UDP); err != nil {
		t.Fatal(err)
	}
	raw := inj.wait(t)
	var h packet.Header
	if err := packet.Parse(raw, &h); err != nil {
		t.Fatal(err)
	}
	if h.Src.String() != "9.9.9.9:3478" || h.Dst.String() != "10.9.8.7:1900" {
		t.Fatalf("reply %s -> %s", h.Src, h.Dst)
	}
	if string(raw[h.PayloadOff:h.PayloadOff+h.PayloadLen]) != string(payload) {
		t.Fatal("payload mismatch")
	}
	if err := packet.ValidChecksums(raw); err != nil {
		t.Fatal(err)
	}
}

func TestDirectCacheAges(t *testing.T) {
	e := newEngine(t, testPlan(), proc(`C:\Windows\other.exe`), &fakeUp{}, nil)
	start := time.Unix(1000, 0)
	for i := 0; i < 50; i++ {
		pkt, _ := packet.BuildTCP(packet.V4(10, 0, 0, 2, uint16(3000+i)), packet.V4(1, 2, 3, 4, 80), 0x02)
		if res := e.HandlePacket(pkt, Meta{}, true); res.Action != ActionPass {
			t.Fatalf("unmatched app should pass, got %v", res.Action)
		}
	}
	if n := e.directTCP.len(); n != 50 {
		t.Fatalf("direct cache %d", n)
	}
	e.sweep(start)
	e.sweep(start.Add(cacheAge))
	e.sweep(start.Add(2 * cacheAge))
	if n := e.directTCP.len(); n != 0 {
		t.Fatalf("idle direct entries kept: %d", n)
	}
}

func TestUDPDialBacksOffAndRecordsError(t *testing.T) {
	up := &fakeUp{udpFail: io.ErrUnexpectedEOF}
	e := newEngine(t, testPlan(), proc(curlPath), up, nil)
	for i := 0; i < 20; i++ {
		pkt, _ := packet.BuildUDP(packet.V4(10, 9, 8, 7, 1900), packet.V4(9, 9, 9, 9, 3478), []byte{1})
		res := e.HandlePacket(pkt, Meta{}, true)
		if res.Action != ActionUDP {
			t.Fatal(res.Action)
		}
		_ = e.ForwardUDP(context.Background(), *res.UDP)
	}
	if up.udpTry != 1 {
		t.Fatalf("dialed %d times while the proxy was down", up.udpTry)
	}
	if st := e.Stats()[`c:\apps\curl.exe`]; st.Err == "" || st.ErrAt.IsZero() {
		t.Fatalf("error not recorded: %+v", st)
	}
}

func TestChildProcessIsProxied(t *testing.T) {
	child := process.Process{PID: 77, Name: "helper.exe", Parents: []process.Process{{PID: 76, Name: "curl.exe", Path: curlPath}}}
	e := newEngine(t, testPlan(), fixedRes{p: child, ok: true}, &fakeUp{}, nil)
	pkt, _ := packet.BuildTCP(packet.V4(10, 0, 0, 2, 3100), packet.V4(1, 2, 3, 4, 443), 0x02)
	if res := e.HandlePacket(pkt, Meta{}, true); res.Action != ActionModified {
		t.Fatalf("child of a proxied app went %v", res.Action)
	}
	e.mu.Lock()
	flow := e.byClient[packet.V4(10, 0, 0, 2, 3100)]
	e.mu.Unlock()
	if flow == nil || flow.proxyID != "socks" {
		t.Fatalf("flow %+v", flow)
	}
}

func TestKillSwitchResetsExistingConnection(t *testing.T) {
	inj := &memInj{}
	e := newEngine(t, testPlan(), proc(curlPath), &fakeUp{}, inj)
	data, _ := packet.BuildTCP(packet.V4(10, 0, 0, 2, 3000), packet.V4(1, 2, 3, 4, 443), 0x18)
	if res := e.HandlePacket(data, Meta{}, true); res.Action != ActionDrop {
		t.Fatalf("pre-existing proxied connection went %v", res.Action)
	}
	if inj.count() != 1 {
		t.Fatal("no reset injected")
	}
	var h packet.Header
	if err := packet.Parse(inj.packet(0), &h); err != nil || !h.RST || h.Dst.String() != "10.0.0.2:3000" {
		t.Fatalf("reset %+v %v", h, err)
	}
	again, _ := packet.BuildTCP(packet.V4(10, 0, 0, 2, 3000), packet.V4(1, 2, 3, 4, 443), 0x18)
	if res := e.HandlePacket(again, Meta{}, true); res.Action != ActionDrop {
		t.Fatal("second packet passed")
	}
}

func TestLaterFragmentsOfDroppedDatagramAreDropped(t *testing.T) {
	e := newEngine(t, testPlan(), proc(browserPath), &fakeUp{}, nil)
	first, _ := packet.BuildUDP(packet.V4(10, 0, 0, 4, 4444), packet.V4(1, 2, 3, 4, 443), make([]byte, 64))
	first[4], first[5] = 0x12, 0x34 // identification
	first[6] = 0x20                 // more fragments
	if res := e.HandlePacket(first, Meta{}, true); res.Action != ActionDrop {
		t.Fatalf("first fragment %v", res.Action)
	}
	rest, _ := packet.BuildUDP(packet.V4(10, 0, 0, 4, 1), packet.V4(1, 2, 3, 4, 1), make([]byte, 64))
	rest[4], rest[5] = 0x12, 0x34
	rest[6], rest[7] = 0x00, 0x09 // offset 72 bytes, last fragment
	if res := e.HandlePacket(rest, Meta{}, true); res.Action != ActionDrop {
		t.Fatalf("later fragment leaked: %v", res.Action)
	}
	other, _ := packet.BuildUDP(packet.V4(10, 0, 0, 4, 1), packet.V4(1, 2, 3, 4, 1), make([]byte, 64))
	other[4], other[5] = 0x99, 0x99
	other[6], other[7] = 0x00, 0x09
	if res := e.HandlePacket(other, Meta{}, true); res.Action != ActionPass {
		t.Fatalf("unrelated fragment %v", res.Action)
	}
}

func TestStatsCountFlows(t *testing.T) {
	e := newEngine(t, testPlan(), proc(curlPath), &fakeUp{}, nil)
	for i := 0; i < 3; i++ {
		syn, _ := packet.BuildTCP(packet.V4(10, 0, 0, 2, uint16(3200+i)), packet.V4(1, 2, 3, 4, 443), 0x02)
		e.HandlePacket(syn, Meta{}, true)
	}
	if st := e.Stats()[`c:\apps\curl.exe`]; st.Flows != 3 {
		t.Fatalf("flows %d (%+v)", st.Flows, e.Stats())
	}
	e.closeAll()
	if st := e.Stats()[`c:\apps\curl.exe`]; st.Flows != 0 {
		t.Fatalf("flows after close %d", st.Flows)
	}
}
