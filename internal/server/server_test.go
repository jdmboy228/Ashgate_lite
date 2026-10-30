package server

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/fstest"
)

type fakeSvc struct {
	events chan []byte
	window string
}

func (f *fakeSvc) Handle(_ context.Context, window, cmd string, args json.RawMessage) (any, error) {
	f.window = window
	if cmd == "fail" {
		return nil, io.ErrUnexpectedEOF
	}
	return map[string]string{"cmd": cmd, "args": string(args)}, nil
}
func (f *fakeSvc) Events() (<-chan []byte, func()) { return f.events, func() {} }
func (f *fakeSvc) Theme() string                   { return "dark" }

func start(t *testing.T) (*Server, *fakeSvc) {
	t.Helper()
	svc := &fakeSvc{events: make(chan []byte, 1)}
	ui := fstest.MapFS{"index.html": {Data: []byte(`<html data-theme="light"></html>`)}, "js/main.js": {Data: []byte("x")}}
	s := New(svc, ui)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	return s, svc
}

func post(t *testing.T, s *Server, token, body string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, "http://"+s.addr+"/api/cmd", strings.NewReader(body))
	req.Header.Set("X-Ashgate-Token", token)
	req.Header.Set("X-Ashgate-Window", "manage")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestIndexCarriesSavedTheme(t *testing.T) {
	s, _ := start(t)
	res, err := http.Get(s.URL("main"))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	if !strings.Contains(string(body), `data-theme="dark"`) {
		t.Fatalf("%s", body)
	}
}

func TestCommandNeedsToken(t *testing.T) {
	s, svc := start(t)
	if res := post(t, s, "wrong", `{"id":"c-1","cmd":"state.get"}`); res.StatusCode != http.StatusForbidden {
		t.Fatalf("status %d", res.StatusCode)
	}
	res := post(t, s, s.token, `{"id":"c-2","cmd":"proxy.select","args":{"proxyId":"p3"}}`)
	var out struct {
		ID     string
		OK     bool
		Result map[string]string
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.ID != "c-2" || !out.OK || out.Result["cmd"] != "proxy.select" || svc.window != "manage" {
		t.Fatalf("%+v window=%s", out, svc.window)
	}
	res = post(t, s, s.token, `{"id":"c-3","cmd":"fail"}`)
	var bad struct {
		OK    bool
		Error struct{ Code string }
	}
	_ = json.NewDecoder(res.Body).Decode(&bad)
	if bad.OK || bad.Error.Code != "INTERNAL" {
		t.Fatalf("%+v", bad)
	}
}

func TestForeignHostIsRefused(t *testing.T) {
	s, _ := start(t)
	req, _ := http.NewRequest(http.MethodGet, "http://"+s.addr+"/", nil)
	req.Host = "evil.example:80"
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("status %d", res.StatusCode)
	}
}

func TestEventsStream(t *testing.T) {
	s, svc := start(t)
	res, err := http.Get("http://" + s.addr + "/api/events?token=" + s.token)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	svc.events <- []byte(`{"event":"state","data":{}}`)
	line, err := bufio.NewReader(res.Body).ReadString('\n')
	if err != nil || line != "data: {\"event\":\"state\",\"data\":{}}\n" {
		t.Fatalf("%q %v", line, err)
	}
}
