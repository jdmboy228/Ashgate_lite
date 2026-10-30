//go:build !race

// Xray's VLESS outbound uses unsafe pointer arithmetic that the race
// detector rejects, so the live VLESS check runs without -race.
package proxycore

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/infra/conf/serial"

	"ashgate-lite/internal/proxycore/testkit"

	// Server-side features of the test VLESS server.
	_ "github.com/xtls/xray-core/proxy/freedom"
	_ "github.com/xtls/xray-core/proxy/vless/inbound"
)

func TestPingSOCKS5(t *testing.T) {
	px, err := testkit.ListenSOCKS("u", "p")
	if err != nil {
		t.Fatal(err)
	}
	defer px.Close()
	target := closingServer(t)
	ok := pingTarget(context.Background(), mustParse(t, "socks5://u:p@"+px.Addr), target)
	if !ok.OK || ok.Millis < 1 {
		t.Fatalf("%+v", ok)
	}
	if bad := pingTarget(context.Background(), mustParse(t, "socks5://u:wrong@"+px.Addr), target); bad.OK || bad.Err == nil {
		t.Fatalf("wrong password passed: %+v", bad)
	}
}

func TestPingHTTP(t *testing.T) {
	px, err := testkit.ListenHTTP("", "")
	if err != nil {
		t.Fatal(err)
	}
	defer px.Close()
	if rep := pingTarget(context.Background(), mustParse(t, "http://"+px.Addr), closingServer(t)); !rep.OK {
		t.Fatalf("%+v", rep)
	}
}

func TestPingSilentProxyTimesOut(t *testing.T) {
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
			defer c.Close() // accept and stay silent
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	rep := pingTarget(ctx, mustParse(t, "socks5://"+ln.Addr().String()), closingServer(t))
	if rep.OK || !rep.Timeout {
		t.Fatalf("%+v", rep)
	}
}

func TestPingVLESS(t *testing.T) {
	port := freePort(t)
	startXray(t, map[string]any{
		"inbounds": []any{map[string]any{
			"listen": "127.0.0.1", "port": port, "protocol": "vless",
			"settings": map[string]any{"decryption": "none", "clients": []any{map[string]any{"id": testUUID}}},
		}},
		"outbounds": []any{map[string]any{"protocol": "freedom"}},
	})
	p := mustParse(t, "vless://"+testUUID+"@127.0.0.1:"+strconv.Itoa(port)+"?type=tcp&security=none#local")
	if rep := pingTarget(context.Background(), p, closingServer(t)); !rep.OK {
		t.Fatalf("%+v", rep)
	}
}

func mustParse(t *testing.T, link string) Proxy {
	t.Helper()
	p, err := ParseLink(link)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// closingServer accepts TCP and closes it: enough for a tunnel to open.
func closingServer(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	return ln.Addr().String()
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func startXray(t *testing.T, doc map[string]any) {
	t.Helper()
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := serial.LoadJSONConfig(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	inst, err := core.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := inst.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = inst.Close() })
}
