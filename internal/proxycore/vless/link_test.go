package vless

import (
	"strings"
	"testing"
)

const (
	sampleUUID = "b7e2c1a0-4f3a-4c2b-9a11-111111111111"
	samplePBK  = "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8"
)

func TestParseXHTTPReality(t *testing.T) {
	raw := "vless://" + sampleUUID + "@203.0.113.10:443?encryption=none&security=reality&type=xhttp&path=%2Fxhttp&host=www.microsoft.com&mode=auto&sni=www.microsoft.com&fp=chrome&pbk=" + samplePBK + "&sid=ab12&spx=%2F&flow=xtls-rprx-vision#aeza-se"
	got, err := ParseLink(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.Label != "aeza-se" || got.Address != "203.0.113.10:443" {
		t.Fatalf("%+v", got)
	}
	p := got.Params
	if p.UUID != sampleUUID || p.Network != "xhttp" || p.Security != "reality" {
		t.Fatalf("%+v", p)
	}
	if p.SNI != "www.microsoft.com" || p.Fingerprint != "chrome" || p.PublicKey != samplePBK {
		t.Fatalf("%+v", p)
	}
	if p.ShortID != "ab12" || p.Path != "/xhttp" || p.Host != "www.microsoft.com" || p.Mode != "auto" {
		t.Fatalf("%+v", p)
	}
	if p.Flow != "xtls-rprx-vision" {
		t.Fatalf("flow kept for the form, got %q", p.Flow)
	}
	body := string(mustClient(t, p, "203.0.113.10", 443))
	if strings.Contains(body, "xtls-rprx-vision") {
		t.Fatal("vision flow must not be sent on xhttp")
	}
	if !strings.Contains(body, `"network":"xhttp"`) || !strings.Contains(body, `"path":"/xhttp"`) {
		t.Fatal(body)
	}
}

func TestParseTCPRealityVision(t *testing.T) {
	raw := "vless://" + sampleUUID + "@vpn.example.com:8443?encryption=none&security=reality&type=tcp&headerType=none&flow=xtls-rprx-vision&sni=www.cloudflare.com&fp=chrome&pbk=" + samplePBK + "&sid=0123456789abcdef#Sweden"
	got, err := ParseLink(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.Address != "vpn.example.com:8443" || got.Label != "Sweden" {
		t.Fatalf("%+v", got)
	}
	if got.Params.Network != "tcp" || got.Params.DialFlow() != "xtls-rprx-vision" {
		t.Fatalf("%+v", got.Params)
	}
	body := string(mustClient(t, got.Params, "vpn.example.com", 8443))
	if !strings.Contains(body, `"flow":"xtls-rprx-vision"`) || !strings.Contains(body, `"security":"reality"`) {
		t.Fatal(body)
	}
}

func TestParseGRPCAndWS(t *testing.T) {
	grpc := "vless://" + sampleUUID + "@203.0.113.8:443?encryption=none&security=reality&type=grpc&serviceName=mygrpc&mode=multi&sni=www.microsoft.com&fp=qq&pbk=" + samplePBK + "&sid=aa#grpc"
	got, err := ParseLink(grpc)
	if err != nil {
		t.Fatal(err)
	}
	if got.Params.Network != "grpc" || got.Params.ServiceName != "mygrpc" || got.Params.Mode != "multi" {
		t.Fatalf("%+v", got.Params)
	}
	if got.Params.Fingerprint != "qq" || got.Params.DialFlow() != "" {
		t.Fatalf("%+v", got.Params)
	}
	body := string(mustClient(t, got.Params, "203.0.113.8", 443))
	if !strings.Contains(body, `"serviceName":"mygrpc"`) || !strings.Contains(body, `"multiMode":true`) {
		t.Fatal(body)
	}

	ws := "vless://" + sampleUUID + "@203.0.113.9:443?encryption=none&security=tls&type=ws&path=%2Fws&host=cdn.example&sni=cdn.example&fp=firefox&alpn=h2%2Chttp%2F1.1#ws-tls"
	got, err = ParseLink(ws)
	if err != nil {
		t.Fatal(err)
	}
	if got.Params.Network != "ws" || got.Params.Security != "tls" || got.Params.Path != "/ws" || got.Params.Host != "cdn.example" {
		t.Fatalf("%+v", got.Params)
	}
	if got.Params.ALPN != "h2,http/1.1" || got.Params.SNI != "cdn.example" {
		t.Fatalf("%+v", got.Params)
	}
	body = string(mustClient(t, got.Params, "203.0.113.9", 443))
	if !strings.Contains(body, `"security":"tls"`) || !strings.Contains(body, `"path":"/ws"`) {
		t.Fatal(body)
	}
}

func TestParseDefaultsAndIPv6(t *testing.T) {
	raw := "vless://" + strings.ToUpper(sampleUUID) + "@[2001:db8::10]?type=tcp&security=none"
	got, err := ParseLink(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.Address != "[2001:db8::10]:443" || got.Params.UUID != sampleUUID {
		t.Fatalf("%+v", got)
	}
	if got.Params.Network != "tcp" || got.Params.Security != "none" || got.Params.Encryption != "none" {
		t.Fatalf("%+v", got.Params)
	}
}

func TestParseRejectsBadLinks(t *testing.T) {
	cases := []string{
		"",
		"https://example",
		"vless://not-a-uuid@203.0.113.10:443?type=tcp&security=none",
		"vless://" + sampleUUID + "@:443?type=tcp",
		"vless://" + sampleUUID + "@203.0.113.10:443?type=tcp&security=reality&sni=www.microsoft.com",
		"vless://" + sampleUUID + "@203.0.113.10:443?type=tcp&security=reality&sni=www.microsoft.com&pbk=AAAA&sid=ab",
		"vless://" + sampleUUID + "@203.0.113.10:443?type=tcp&security=reality&pbk=" + samplePBK + "&sid=abc",
		"vless://" + sampleUUID + "@203.0.113.10:443?type=quic&security=none",
		"vless://" + sampleUUID + "@203.0.113.10:443?type=grpc&security=tls&sni=a.example",
	}
	for _, raw := range cases {
		_, err := ParseLink(raw)
		if err == nil || !strings.Contains(err.Error(), "Не удалось разобрать ссылку VLESS") {
			t.Fatalf("%q -> %v", raw, err)
		}
	}
}

func TestManualNormalizeKeepsEditedFields(t *testing.T) {
	p := Params{
		UUID:      sampleUUID,
		Network:   "XHTTP",
		Security:  "REALITY",
		SNI:       "www.microsoft.com",
		PublicKey: samplePBK,
		ShortID:   "ab",
		Flow:      "xtls-rprx-vision",
		Path:      "/",
		Mode:      "packet-up",
	}
	if err := p.Normalize(); err != nil {
		t.Fatal(err)
	}
	if p.Network != "xhttp" || p.Security != "reality" || p.Fingerprint != "chrome" {
		t.Fatalf("%+v", p)
	}
	if p.DialFlow() != "" {
		t.Fatal("edited xhttp row must not keep vision")
	}
}

func TestClientJSONHasLocalSOCKS(t *testing.T) {
	p := Params{UUID: sampleUUID, Network: "tcp", Security: "none"}
	b, err := ClientJSON("127.0.0.1", 10808, "203.0.113.10", 443, p)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{`"port":10808`, `"protocol":"socks"`, `"auth":"noauth"`, `"udp":true`, `"listen":"127.0.0.1"`} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %s\n%s", want, s)
		}
	}
	if strings.Contains(s, `"protocol":"http"`) {
		t.Fatalf("unexpected http inbound\n%s", s)
	}
}

func mustClient(t *testing.T, p Params, host string, port int) []byte {
	t.Helper()
	b, err := ClientJSON("127.0.0.1", 1080, host, port, p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
