package lite

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"ashgate-lite/internal/proxycore"
)

type fakeCore struct {
	cfg        proxycore.Config
	connected  bool
	connectErr error
	stats      map[string]proxycore.Stat
}

func (f *fakeCore) Configure(c proxycore.Config) error { f.cfg = c; return nil }
func (f *fakeCore) Connect() error {
	f.connected = f.connectErr == nil
	return f.connectErr
}
func (f *fakeCore) Disconnect()                      { f.connected = false }
func (f *fakeCore) Status() proxycore.Status         { return proxycore.Status{Connected: f.connected} }
func (f *fakeCore) Stats() map[string]proxycore.Stat { return f.stats }

type fakeFinder struct{ running map[string]bool }

func (f fakeFinder) Suggest(string) []Suggestion { return nil }
func (f fakeFinder) Running() map[string]bool    { return f.running }

type nopHost struct{}

func (nopHost) Minimize(string)            {}
func (nopHost) Close(string)               {}
func (nopHost) OpenManage()                {}
func (nopHost) StartDrag(string)           {}
func (nopHost) StartResize(string, string) {}

func newTestService(t *testing.T, ping func(context.Context, proxycore.Proxy) proxycore.PingResult) (*Service, *fakeCore) {
	t.Helper()
	core := &fakeCore{}
	if ping == nil {
		ping = func(context.Context, proxycore.Proxy) proxycore.PingResult {
			return proxycore.PingResult{OK: true, Millis: 48}
		}
	}
	s, err := New(Options{
		SettingsPath: filepath.Join(t.TempDir(), "settings.json"),
		Core:         core, Discover: fakeFinder{}, Host: nopHost{}, Ping: ping,
	})
	if err != nil {
		t.Fatal(err)
	}
	return s, core
}

func do(t *testing.T, s *Service, cmd string, args any) (any, error) {
	t.Helper()
	raw, _ := json.Marshal(args)
	return s.Handle(context.Background(), "main", cmd, raw)
}

func code(err error) string {
	var ce *CmdError
	if errors.As(err, &ce) {
		return ce.Code
	}
	return ""
}

func TestFirstProxyBecomesCurrentAndTurnsOn(t *testing.T) {
	s, core := newTestService(t, nil)
	res, err := do(t, s, "proxy.paste", map[string]string{"text": "socks5://185.12.40.7:1080#Германия"})
	if err != nil {
		t.Fatal(err)
	}
	v := res.(proxyView)
	if v.Slot != 1 || v.Type != "SOCKS5" || v.HostMasked != "185.12.•••.7" || v.Country != "Германия" || v.Port != 1080 {
		t.Fatalf("%+v", v)
	}
	st := s.State()
	if !st.Enabled || st.CurrentProxyID == nil || *st.CurrentProxyID != v.ID || !core.connected {
		t.Fatalf("state %+v connected=%v", st, core.connected)
	}
}

func TestPasteLimitAndParseErrors(t *testing.T) {
	s, _ := newTestService(t, nil)
	for _, link := range []string{"socks5://1.1.1.1:1", "http://2.2.2.2:2", "socks5://3.3.3.3:3"} {
		if _, err := do(t, s, "proxy.paste", map[string]string{"text": link}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := do(t, s, "proxy.paste", map[string]string{"text": "socks5://4.4.4.4:4"}); code(err) != "PROXY_LIMIT" {
		t.Fatalf("4th proxy: %v", err)
	}
	if _, err := do(t, s, "proxy.update", map[string]string{"proxyId": "x", "text": "nonsense"}); code(err) != "PROXY_PARSE" {
		t.Fatalf("bad link: %v", err)
	}
}

func TestRoutesFollowCurrentProxyPauseAndAllTraffic(t *testing.T) {
	s, core := newTestService(t, nil)
	res, _ := do(t, s, "proxy.paste", map[string]string{"text": "socks5://1.1.1.1:1080"})
	pid := res.(proxyView).ID
	chrome, _ := do(t, s, "apps.add", map[string]string{"exePath": `C:\Apps\Chrome.exe`})
	tg, _ := do(t, s, "apps.add", map[string]string{"exePath": `C:\Apps\Telegram.exe`})
	if len(core.cfg.Apps) != 2 || core.cfg.Apps[0].ProxyID != pid {
		t.Fatalf("routes %+v", core.cfg.Apps)
	}
	if _, err := do(t, s, "apps.pause", map[string]any{"appId": tg.(appView).ID, "paused": true}); err != nil {
		t.Fatal(err)
	}
	if core.cfg.Apps[1].ProxyID != "" {
		t.Fatal("paused app still proxied")
	}
	if _, err := do(t, s, "apps.direct", map[string]string{"appId": chrome.(appView).ID}); code(err) != "NOT_ALL_TRAFFIC" {
		t.Fatalf("direct outside all-traffic mode: %v", err)
	}
	if _, err := do(t, s, "others.set", map[string]any{"target": map[string]string{"proxyId": pid}}); err != nil {
		t.Fatal(err)
	}
	if _, err := do(t, s, "apps.direct", map[string]string{"appId": chrome.(appView).ID}); err != nil {
		t.Fatal(err)
	}
	if core.cfg.Others != pid || core.cfg.Apps[0].ProxyID != "" {
		t.Fatalf("all-traffic config %+v", core.cfg)
	}
	if st := s.State(); st.Others != (othersTarget{ProxyID: pid}) || st.AllTraffic == nil {
		t.Fatalf("others %+v", st.Others)
	}
	if _, err := do(t, s, "others.set", map[string]any{"target": "direct"}); err != nil || core.cfg.Others != "" {
		t.Fatalf("back to direct: %v %+v", err, core.cfg)
	}
}

func TestProxyDownSuggestsFastestAndSwitches(t *testing.T) {
	failing := map[string]bool{}
	s, _ := newTestService(t, func(_ context.Context, p proxycore.Proxy) proxycore.PingResult {
		if failing[p.Host] {
			return proxycore.PingResult{Timeout: true, Err: context.DeadlineExceeded}
		}
		return proxycore.PingResult{OK: true, Millis: int64(p.Port)}
	})
	first, _ := do(t, s, "proxy.paste", map[string]string{"text": "socks5://10.0.0.1:48"})
	second, _ := do(t, s, "proxy.paste", map[string]string{"text": "http://10.0.0.2:64"})
	s.pingAll(context.Background(), "")
	failing["10.0.0.1"] = true
	s.pingAll(context.Background(), "")
	st := s.State()
	if st.ProxyDown == nil || st.ProxyDown.ProxyID != first.(proxyView).ID || st.ProxyDown.Fastest == nil ||
		st.ProxyDown.Fastest.ProxyID != second.(proxyView).ID || st.ProxyDown.Fastest.PingMs != 64 {
		t.Fatalf("down %+v", st.ProxyDown)
	}
	if st.Proxies[0].Available || st.Proxies[0].LastError != "timeout" || st.Proxies[0].UnavailableSince == 0 {
		t.Fatalf("failed proxy view %+v", st.Proxies[0])
	}
	if _, err := do(t, s, "proxy.select", map[string]string{"proxyId": first.(proxyView).ID}); code(err) != "PROXY_UNAVAILABLE" {
		t.Fatalf("selected an unavailable proxy: %v", err)
	}
	if _, err := do(t, s, "proxy.switchFastest", nil); err != nil {
		t.Fatal(err)
	}
	if st := s.State(); *st.CurrentProxyID != second.(proxyView).ID || st.ProxyDown != nil {
		t.Fatalf("after switch %+v", st)
	}
}

func TestSpeedsAndAppStatus(t *testing.T) {
	s, _ := newTestService(t, nil)
	now := time.Unix(1000, 0)
	s.opt.Now = func() time.Time { return now }
	do(t, s, "proxy.paste", map[string]string{"text": "socks5://1.1.1.1:1080"})
	added, _ := do(t, s, "apps.add", map[string]string{"exePath": `C:\Apps\Discord.exe`})
	id := added.(appView).ID
	key := proxycore.StatKey(`C:\Apps\Discord.exe`)
	running := map[string]bool{key: true}

	s.tick(running, map[string]proxycore.Stat{key: {Down: 1000, Up: 100}})
	s.tick(running, map[string]proxycore.Stat{key: {Down: 1468006 + 1000, Up: 122880 + 100}})
	app := s.State().Apps[0]
	if app.Status != statusRunning || app.DownBps != 1468006 || app.UpBps != 122880 {
		t.Fatalf("running app %+v", app)
	}

	failed := map[string]proxycore.Stat{key: {Down: 1469006, Up: 123080, Err: "socks5: connection refused", ErrAt: now}}
	if !s.tick(running, failed) {
		t.Fatal("status change not reported")
	}
	app = s.State().Apps[0]
	if app.Status != statusError || app.Error == nil || app.Error.Short != "Соединения не проходят" {
		t.Fatalf("error app %+v", app)
	}

	if _, err := do(t, s, "apps.check", map[string]string{"appId": id}); err != nil {
		t.Fatal(err)
	}
	if app := s.State().Apps[0]; app.Status != statusRunning {
		t.Fatalf("after a good check %+v", app)
	}
	s.tick(map[string]bool{}, failed)
	if app := s.State().Apps[0]; app.Status != statusNotRunning {
		t.Fatalf("closed app %+v", app)
	}
}

func TestThemePersists(t *testing.T) {
	s, _ := newTestService(t, nil)
	if s.Theme() != "light" {
		t.Fatal("first run must be light")
	}
	if _, err := do(t, s, "settings.setTheme", map[string]string{"theme": "dark"}); err != nil {
		t.Fatal(err)
	}
	again, err := New(Options{SettingsPath: s.opt.SettingsPath, Core: &fakeCore{}, Discover: fakeFinder{}, Host: nopHost{}})
	if err != nil || again.Theme() != "dark" {
		t.Fatalf("theme %q %v", again.Theme(), err)
	}
	if _, err := do(t, s, "settings.setTheme", map[string]string{"theme": "auto"}); code(err) != "BAD_ARGS" {
		t.Fatalf("theme auto: %v", err)
	}
}

func TestMaskHost(t *testing.T) {
	for in, want := range map[string]string{
		"185.12.40.7":        "185.12.•••.7",
		"nl-ams.example.net": "nl-ams.•••.net",
		"proxy.net":          "•••.net",
		"2001:db8:1:2::abcd": "2001:•••:abcd",
		"localhost":          "localhost",
	} {
		if got := maskHost(in); got != want {
			t.Fatalf("%s: %s want %s", in, got, want)
		}
	}
}
