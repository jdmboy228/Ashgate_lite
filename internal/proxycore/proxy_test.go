package proxycore

import (
	"errors"
	"testing"
)

const testUUID = "11111111-1111-4111-8111-111111111111"

func TestParseSOCKSAndHTTPLinks(t *testing.T) {
	cases := []struct {
		raw  string
		want Proxy
		link string
	}{
		{"socks5://185.12.0.7:1080", Proxy{Type: SOCKS5, Host: "185.12.0.7", Port: 1080}, "socks5://185.12.0.7:1080"},
		{" socks://user:p%40ss@proxy.example:1081#Германия ", Proxy{Type: SOCKS5, Host: "proxy.example", Port: 1081, Username: "user", Password: "p@ss", Label: "Германия"}, "socks5://user:p%40ss@proxy.example:1081#%D0%93%D0%B5%D1%80%D0%BC%D0%B0%D0%BD%D0%B8%D1%8F"},
		{"http://[2001:db8::1]:8080", Proxy{Type: HTTP, Host: "2001:db8::1", Port: 8080}, "http://[2001:db8::1]:8080"},
	}
	for _, c := range cases {
		got, err := ParseLink(c.raw)
		if err != nil {
			t.Fatalf("%s: %v", c.raw, err)
		}
		if got.Link != c.link {
			t.Fatalf("%s: link %q want %q", c.raw, got.Link, c.link)
		}
		got.Link = ""
		if got != c.want {
			t.Fatalf("%s: %+v", c.raw, got)
		}
	}
}

func TestParseVLESSLink(t *testing.T) {
	raw := "vless://" + testUUID + "@nl-ams.example.net:443?type=tcp&security=tls&sni=nl-ams.example.net#Нидерланды"
	p, err := ParseLink(raw)
	if err != nil {
		t.Fatal(err)
	}
	if p.Type != VLESS || p.Address() != "nl-ams.example.net:443" || p.Label != "Нидерланды" || p.VLESS == nil || p.Link != raw {
		t.Fatalf("%+v", p)
	}
	if !p.CarriesUDP() {
		t.Fatal("VLESS carries UDP through its bridge")
	}
}

func TestParseRejectsBadLinks(t *testing.T) {
	for _, raw := range []string{"", "185.12.0.7:1080", "https://proxy:443", "socks5://host", "socks5://host:0", "socks5://:1080", "vless://not-a-uuid@host:443"} {
		if _, err := ParseLink(raw); !errors.Is(err, ErrParse) {
			t.Fatalf("%q: %v", raw, err)
		}
	}
}
