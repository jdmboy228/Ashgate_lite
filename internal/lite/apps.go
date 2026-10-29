package lite

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	"ashgate-lite/internal/proxycore"
)

// errorWindow is how long a failed connection keeps an app in «Ошибка»
// while nothing else gets through.
const errorWindow = 30 * time.Second

// errShort is the second line of an app row in «Ошибка» (design copy.md).
const errShort = "Соединения не проходят"

func (s *Service) suggest(query string) []Suggestion {
	list := s.opt.Discover.Suggest(query)
	s.mu.Lock()
	for _, sg := range list {
		s.found[lowerPath(sg.ExePath)] = sg
	}
	s.mu.Unlock()
	return list
}

func (s *Service) addApp(exePath string) (any, error) {
	exePath = strings.TrimSpace(exePath)
	if !strings.EqualFold(filepath.Ext(exePath), ".exe") {
		return nil, cmdErr("BAD_APP", "нужен путь к .exe")
	}
	s.mu.Lock()
	for _, a := range s.set.Apps {
		if strings.EqualFold(a.ExePath, exePath) {
			s.mu.Unlock()
			return nil, cmdErr("APP_EXISTS", "приложение уже в списке")
		}
	}
	name := s.found[lowerPath(exePath)].Name
	if name == "" {
		name = exeName(exePath)
	}
	a := savedApp{ID: newID(), Name: name, ExePath: exePath}
	s.set.Apps = append(s.set.Apps, a)
	s.apps[a.ID] = &appRuntime{}
	view := s.appViewLocked(a)
	s.mu.Unlock()
	s.changed()
	return view, nil
}

// exeName is «Telegram» for C:\Apps\Telegram.exe, on any host OS.
func exeName(path string) string {
	base := path[strings.LastIndexAny(path, `\/`)+1:]
	return strings.TrimSuffix(base, filepath.Ext(base))
}

func (s *Service) appIndexLocked(id string) int {
	for i, a := range s.set.Apps {
		if a.ID == id {
			return i
		}
	}
	return -1
}

func (s *Service) editApp(id string, fn func(*savedApp)) error {
	s.mu.Lock()
	i := s.appIndexLocked(id)
	if i < 0 {
		s.mu.Unlock()
		return cmdErr("NOT_FOUND", "приложение не найдено")
	}
	fn(&s.set.Apps[i])
	s.mu.Unlock()
	s.changed()
	return nil
}

func (s *Service) pauseApp(id string, paused bool) error {
	return s.editApp(id, func(a *savedApp) { a.Paused = paused })
}

// directApp is «Пустить напрямую»: only in all-traffic mode. It toggles.
// TODO: insufficient data: the design has no way back named; the item switches it off again.
func (s *Service) directApp(id string) error {
	s.mu.Lock()
	allTraffic := s.set.OthersProxyID != ""
	s.mu.Unlock()
	if !allTraffic {
		return cmdErr("NOT_ALL_TRAFFIC", "только в режиме «Всё остальное → через прокси»")
	}
	return s.editApp(id, func(a *savedApp) { a.Direct = !a.Direct })
}

func (s *Service) removeApp(id string) error {
	s.mu.Lock()
	i := s.appIndexLocked(id)
	if i < 0 {
		s.mu.Unlock()
		return cmdErr("NOT_FOUND", "приложение не найдено")
	}
	s.set.Apps = append(s.set.Apps[:i], s.set.Apps[i+1:]...)
	delete(s.apps, id)
	delete(s.speeds, id)
	s.mu.Unlock()
	s.changed()
	return nil
}

func (s *Service) setTheme(theme string) error {
	if theme != themeLight && theme != themeDark {
		return cmdErr("BAD_ARGS", "тема: light или dark")
	}
	s.mu.Lock()
	s.set.Theme = theme
	s.mu.Unlock()
	s.changed()
	return nil
}

// checkApp is «Проверить снова» on an app row: the row shows «Проверяем…»
// while a tunnel through the current proxy is tried.
func (s *Service) checkApp(ctx context.Context, id string) error {
	s.mu.Lock()
	rt, ok := s.apps[id]
	p, hasProxy := s.proxies[s.set.CurrentProxyID]
	if ok {
		rt.checking = true
	}
	s.mu.Unlock()
	if !ok {
		return cmdErr("NOT_FOUND", "приложение не найдено")
	}
	s.emitAppStatus(id)
	res := proxycore.PingResult{Err: errNoProxy}
	if hasProxy {
		res = s.opt.Ping(ctx, p)
	}
	s.mu.Lock()
	rt.checking = false
	rt.clearedAt = s.opt.Now()
	rt.err = nil
	if !res.OK {
		rt.err = &appError{Short: errShort, Detail: res.Err.Error()}
	}
	s.mu.Unlock()
	s.emitAppStatus(id)
	s.publish()
	return nil
}

func (s *Service) emitAppStatus(id string) {
	s.mu.Lock()
	i := s.appIndexLocked(id)
	if i < 0 {
		s.mu.Unlock()
		return
	}
	v := s.appViewLocked(s.set.Apps[i])
	s.mu.Unlock()
	s.events.emit("appStatus", appStatusEvent{AppID: id, Status: v.Status, Error: v.Error})
}

// trafficLoop publishes speeds once a second and keeps app statuses current.
func (s *Service) trafficLoop(ctx context.Context) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		running := s.opt.Discover.Running()
		stats := s.opt.Core.Stats()
		if s.tick(running, stats) {
			s.publish()
		}
	}
}

// tick folds one second of traffic into speeds and statuses. It reports
// whether any app changed status.
func (s *Service) tick(running map[string]bool, stats map[string]proxycore.Stat) bool {
	s.mu.Lock()
	before := s.statusesLocked()
	s.running = running
	now := s.opt.Now()
	ev := speedsEvent{Apps: map[string]rates{}}
	for _, a := range s.set.Apps {
		key := proxycore.StatKey(a.ExePath)
		r := delta(s.last[key], stats[key])
		s.speeds[a.ID] = r
		ev.Apps[a.ID] = r
		s.noteErrorLocked(a, stats[key], r, now)
	}
	if s.set.OthersProxyID != "" {
		r := delta(s.last[proxycore.OthersKey], stats[proxycore.OthersKey])
		s.all, ev.AllTraffic = &r, &r
	}
	s.last = stats
	changed := !equalStatuses(before, s.statusesLocked())
	enabled := s.set.Enabled
	s.mu.Unlock()
	if enabled {
		s.events.emit("speeds", ev)
	}
	return changed
}

// noteErrorLocked puts an app in «Ошибка» while its connections fail and
// nothing gets through, and clears it once traffic flows again.
func (s *Service) noteErrorLocked(a savedApp, st proxycore.Stat, r rates, now time.Time) {
	rt := s.apps[a.ID]
	if r.DownBps > 0 {
		rt.err = nil
		return
	}
	if st.Err == "" || !st.ErrAt.After(rt.clearedAt) || now.Sub(st.ErrAt) > errorWindow {
		return
	}
	// TODO: insufficient data: the design's detail («Прокси 1 не пропускает UDP…») needs a
	// classified cause; the proxy's own error text is shown instead.
	rt.err = &appError{Short: errShort, Detail: st.Err}
}

func delta(prev, cur proxycore.Stat) rates {
	if cur.Down < prev.Down || cur.Up < prev.Up {
		return rates{DownBps: cur.Down, UpBps: cur.Up} // counters restarted
	}
	return rates{DownBps: cur.Down - prev.Down, UpBps: cur.Up - prev.Up}
}

func (s *Service) statusesLocked() map[string]appStatus {
	out := make(map[string]appStatus, len(s.set.Apps))
	for _, a := range s.set.Apps {
		out[a.ID] = s.appViewLocked(a).Status
	}
	return out
}

func equalStatuses(a, b map[string]appStatus) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
