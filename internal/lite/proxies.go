package lite

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"ashgate-lite/internal/proxycore"
)

const (
	// healthEvery is how often the proxies in use are checked while enabled.
	// TODO: insufficient data: the design gives no interval; 30 s, and 5 s while down.
	healthEvery     = 30 * time.Second
	healthEveryDown = 5 * time.Second
)

func newID() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func parseErr(err error) error {
	return cmdErr("PROXY_PARSE", err.Error())
}

// pasteProxy adds a proxy from a pasted link. The first proxy becomes
// current and turns proxying on (design behavior.md §8).
func (s *Service) pasteProxy(text string) (any, error) {
	p, err := proxycore.ParseLink(text)
	if err != nil {
		return nil, parseErr(err)
	}
	s.mu.Lock()
	slot := s.freeSlotLocked()
	if slot == 0 {
		s.mu.Unlock()
		return nil, cmdErr("PROXY_LIMIT", "3 из 3 — удалите прокси, чтобы вставить новый")
	}
	id := newID()
	s.proxies[id] = p
	s.health[id] = &health{available: true}
	s.set.Proxies = append(s.set.Proxies, savedProxy{ID: id, Slot: slot, Link: p.Link})
	s.sortProxiesLocked()
	first := len(s.set.Proxies) == 1
	if first {
		s.set.CurrentProxyID = id
	}
	view := s.proxyViewLocked(s.set.Proxies[s.indexLocked(id)])
	s.mu.Unlock()
	s.changed()
	go s.pingAll(context.Background(), id)
	if first {
		_ = s.setEnabled(true)
	}
	return view, nil
}

func (s *Service) indexLocked(id string) int {
	for i, sp := range s.set.Proxies {
		if sp.ID == id {
			return i
		}
	}
	return -1
}

func (s *Service) updateProxy(id, text string) (any, error) {
	p, err := proxycore.ParseLink(text)
	if err != nil {
		return nil, parseErr(err)
	}
	s.mu.Lock()
	i := s.indexLocked(id)
	if i < 0 {
		s.mu.Unlock()
		return nil, cmdErr("NOT_FOUND", "прокси не найден")
	}
	s.proxies[id] = p
	s.health[id] = &health{available: true}
	s.set.Proxies[i].Link = p.Link
	if s.down != nil && s.down.ProxyID == id {
		s.down = nil
	}
	view := s.proxyViewLocked(s.set.Proxies[i])
	s.mu.Unlock()
	s.changed()
	go s.pingAll(context.Background(), id)
	return view, nil
}

// removeProxy deletes a proxy. If it was current, the next available
// proxy takes over; a footer pointing at it goes back to direct.
func (s *Service) removeProxy(id string) error {
	s.mu.Lock()
	i := s.indexLocked(id)
	if i < 0 {
		s.mu.Unlock()
		return cmdErr("NOT_FOUND", "прокси не найден")
	}
	s.set.Proxies = append(s.set.Proxies[:i], s.set.Proxies[i+1:]...)
	delete(s.proxies, id)
	delete(s.health, id)
	if s.set.CurrentProxyID == id {
		s.set.CurrentProxyID = s.firstAvailableLocked()
	}
	if s.set.OthersProxyID == id {
		s.set.OthersProxyID = ""
	}
	if s.down != nil && s.down.ProxyID == id {
		s.down = nil
	}
	empty := len(s.set.Proxies) == 0
	s.mu.Unlock()
	if empty {
		return s.setEnabled(false)
	}
	s.changed()
	return nil
}

func (s *Service) firstAvailableLocked() string {
	for _, sp := range s.set.Proxies {
		if s.health[sp.ID].available {
			return sp.ID
		}
	}
	return ""
}

// selectProxy makes a proxy current; nil means «Напрямую». An unavailable
// proxy cannot be chosen.
func (s *Service) selectProxy(id *string) error {
	s.mu.Lock()
	target := ""
	if id != nil {
		h, ok := s.health[*id]
		if !ok {
			s.mu.Unlock()
			return cmdErr("NOT_FOUND", "прокси не найден")
		}
		if !h.available {
			s.mu.Unlock()
			return cmdErr("PROXY_UNAVAILABLE", "прокси недоступен")
		}
		target = *id
	}
	s.set.CurrentProxyID = target
	s.down = nil
	s.mu.Unlock()
	s.changed()
	return nil
}

func (s *Service) switchFastest() error {
	s.mu.Lock()
	var id string
	if s.down != nil && s.down.Fastest != nil {
		id = s.down.Fastest.ProxyID
	}
	s.mu.Unlock()
	if id == "" {
		return cmdErr("NO_FASTEST", "нет рабочего прокси")
	}
	return s.selectProxy(&id)
}

func (s *Service) setOthers(raw json.RawMessage) error {
	var target string
	if string(raw) != `"direct"` {
		var t othersTarget
		if err := decode(raw, &t); err != nil {
			return err
		}
		target = t.ProxyID
	}
	s.mu.Lock()
	if _, ok := s.proxies[target]; target != "" && !ok {
		s.mu.Unlock()
		return cmdErr("NOT_FOUND", "прокси не найден")
	}
	s.set.OthersProxyID = target
	s.mu.Unlock()
	s.changed()
	return nil
}

func (s *Service) copyProxy(id string) error {
	link, err := s.proxyLink(id)
	if err != nil {
		return err
	}
	if s.opt.CopyText == nil {
		return cmdErr("CLIPBOARD", "буфер обмена недоступен")
	}
	return s.opt.CopyText(link)
}

// proxyLink returns the share link for the edit screen (k).
func (s *Service) proxyLink(id string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.proxies[id]
	if !ok {
		return "", cmdErr("NOT_FOUND", "прокси не найден")
	}
	return p.Link, nil
}

// checkProxy pings one proxy and waits for the result («Проверить»).
func (s *Service) checkProxy(ctx context.Context, id string) error {
	s.mu.Lock()
	p, ok := s.proxies[id]
	s.mu.Unlock()
	if !ok {
		return cmdErr("NOT_FOUND", "прокси не найден")
	}
	s.recordPing(id, s.opt.Ping(ctx, p))
	s.updateDown()
	s.publish()
	return nil
}

// pingAll refreshes the ping of one proxy, or of all when id is empty.
func (s *Service) pingAll(ctx context.Context, id string) {
	s.mu.Lock()
	targets := map[string]proxycore.Proxy{}
	for pid, p := range s.proxies {
		if id == "" || pid == id {
			targets[pid] = p
		}
	}
	s.mu.Unlock()
	var wg sync.WaitGroup
	for pid, p := range targets {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.recordPing(pid, s.opt.Ping(ctx, p))
		}()
	}
	wg.Wait()
	s.updateDown()
	s.publish()
}

func (s *Service) recordPing(id string, r proxycore.PingResult) {
	s.mu.Lock()
	h, ok := s.health[id]
	if !ok {
		s.mu.Unlock()
		return
	}
	if r.OK {
		ms := r.Millis
		h.pingMs, h.available, h.lastErr = &ms, true, ""
	} else {
		if h.available {
			h.since = s.opt.Now()
		}
		h.pingMs, h.available = nil, false
		h.lastErr = "error"
		if r.Timeout {
			h.lastErr = "timeout"
		}
	}
	ev := pingEvent{ProxyID: id, PingMs: h.pingMs, Available: h.available}
	s.mu.Unlock()
	s.events.emit("ping", ev)
}

// updateDown sets or clears «Прокси N не отвечает» (screen g) from the
// current proxy's health, with the fastest working proxy as the way out.
func (s *Service) updateDown() {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur := s.set.CurrentProxyID
	h, ok := s.health[cur]
	if !s.set.Enabled || !ok || h.available {
		s.down = nil
		return
	}
	s.down = &proxyDown{ProxyID: cur, SinceMs: h.since.UnixMilli(), Fastest: s.fastestLocked(cur)}
}

func (s *Service) fastestLocked(except string) *fastest {
	var best *fastest
	for _, sp := range s.set.Proxies {
		h := s.health[sp.ID]
		if sp.ID == except || !h.available || h.pingMs == nil {
			continue
		}
		if best == nil || *h.pingMs < best.PingMs {
			best = &fastest{ProxyID: sp.ID, PingMs: *h.pingMs}
		}
	}
	return best
}

// healthLoop checks the proxies in use while proxying is on.
func (s *Service) healthLoop(ctx context.Context) {
	wait := healthEvery
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		s.mu.Lock()
		enabled, ids := s.set.Enabled, s.inUseLocked()
		s.mu.Unlock()
		if !enabled {
			continue
		}
		for _, id := range ids {
			s.pingAll(ctx, id)
		}
		s.mu.Lock()
		down := s.down != nil
		s.mu.Unlock()
		wait = healthEvery
		if down {
			// While the current proxy is down, the others are re-measured too,
			// so «Быстрее всех» stays right.
			s.pingAll(ctx, "")
			wait = healthEveryDown
		}
	}
}

func (s *Service) inUseLocked() []string {
	var ids []string
	if s.set.CurrentProxyID != "" {
		ids = append(ids, s.set.CurrentProxyID)
	}
	if o := s.set.OthersProxyID; o != "" && o != s.set.CurrentProxyID {
		ids = append(ids, o)
	}
	return ids
}

var errNoProxy = errors.New("нет текущего прокси")
