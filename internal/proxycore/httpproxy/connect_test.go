package httpproxy

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	"ashgate-lite/internal/proxycore/internal/netx"
	"ashgate-lite/internal/proxycore/testkit"
)

func TestConnectAuthAndEcho(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
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
	target := ln.Addr().(*net.TCPAddr)

	px, err := testkit.ListenHTTP("user", "pass")
	if err != nil {
		t.Fatal(err)
	}
	defer px.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := Dial(ctx, netx.Server{Address: px.Addr}, "127.0.0.1", uint16(target.Port)); err == nil {
		t.Fatal("expected 407")
	}
	conn, err := Dial(ctx, netx.Server{Address: px.Addr, Username: "user", Password: "pass"}, "127.0.0.1", uint16(target.Port))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("xyz")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 3)
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatal(err)
	}
	if string(buf) != "xyz" {
		t.Fatalf("got %q", buf)
	}
}

func TestDNSQueryOverConnect(t *testing.T) {
	px, err := testkit.ListenHTTP("", "")
	if err != nil {
		t.Fatal(err)
	}
	defer px.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	query := []byte{0x12, 0x34, 0x01, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
	got, err := DNSQuery(ctx, netx.Server{Address: px.Addr}, "1.1.1.1:53", query)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) < 3 || got[0] != 0x12 || got[2]&0x80 == 0 {
		t.Fatalf("response %x", got)
	}
}
