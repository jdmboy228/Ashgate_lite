package packet

import (
	"net"
	"testing"
)

func TestIPv4TCPRewriteChecksum(t *testing.T) {
	src := V4(10, 1, 2, 3, 40000)
	dst := V4(1, 2, 3, 4, 443)
	pkt, err := BuildTCP(src, dst, 0x02)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidChecksums(pkt); err != nil {
		t.Fatal(err)
	}
	var h Header
	if err := Parse(pkt, &h); err != nil {
		t.Fatal(err)
	}
	if !h.SYN || h.ACK || h.Src.Port != 40000 || h.Dst.Port != 443 {
		t.Fatalf("header = %+v", h)
	}
	if err := Rewrite(pkt, &h, V4(127, 0, 0, 1, 34010), V4(127, 0, 0, 1, 50000)); err != nil {
		t.Fatal(err)
	}
	if err := ValidChecksums(pkt); err != nil {
		t.Fatal(err)
	}
	var h2 Header
	if err := Parse(pkt, &h2); err != nil {
		t.Fatal(err)
	}
	if h2.Src.Port != 34010 || h2.Dst.Port != 50000 || h2.Src.IP[0] != 127 {
		t.Fatalf("rewritten = %+v", h2)
	}
}

func TestIPv6UDPRoundTrip(t *testing.T) {
	src := FromIP(net.ParseIP("2001:db8::1"), 53000)
	dst := FromIP(net.ParseIP("2001:db8::53"), 53)
	payload := []byte{0x12, 0x34, 0x01, 0x00}
	pkt, err := BuildUDP(src, dst, payload)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidChecksums(pkt); err != nil {
		t.Fatal(err)
	}
	var h Header
	if err := Parse(pkt, &h); err != nil {
		t.Fatal(err)
	}
	if h.PayloadLen != len(payload) || h.Proto != ProtoUDP {
		t.Fatalf("%+v", h)
	}
	if err := Rewrite(pkt, &h, FromIP(net.ParseIP("::1"), 9), FromIP(net.ParseIP("::1"), 34010)); err != nil {
		t.Fatal(err)
	}
	if err := ValidChecksums(pkt); err != nil {
		t.Fatal(err)
	}
}

func TestFragmentHasNoPorts(t *testing.T) {
	pkt, err := BuildTCP(V4(10, 0, 0, 1, 1), V4(10, 0, 0, 2, 2), 0x02)
	if err != nil {
		t.Fatal(err)
	}
	// Set a non-zero fragment offset.
	pkt[6], pkt[7] = 0x00, 0x20
	var h Header
	if err := Parse(pkt, &h); err != nil {
		t.Fatal(err)
	}
	if !h.Frag || h.L4Off != 0 {
		t.Fatalf("frag header = %+v", h)
	}
}

func TestAddressClasses(t *testing.T) {
	if !Loopback(V4(127, 0, 0, 1, 1)) || !Loopback(FromIP(net.ParseIP("::1"), 1)) {
		t.Fatal("loopback")
	}
	if !MulticastOrBroadcast(V4(224, 0, 0, 1, 1)) || !LinkLocal(V4(169, 254, 1, 1, 1)) {
		t.Fatal("special")
	}
}

func TestParseAllocs(t *testing.T) {
	pkt, err := BuildTCP(V4(10, 0, 0, 1, 1), V4(1, 2, 3, 4, 80), 0x02)
	if err != nil {
		t.Fatal(err)
	}
	var h Header
	allocs := testing.AllocsPerRun(100, func() {
		if err := Parse(pkt, &h); err != nil {
			t.Fatal(err)
		}
	})
	if allocs > 0 {
		t.Fatalf("Parse allocated %v times", allocs)
	}
}
