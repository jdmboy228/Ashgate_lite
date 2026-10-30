// Package lite is the Ashgate Lite backend behind the UI: it keeps the
// settings, builds the State the windows draw, runs the commands of the
// Go ↔ JS bridge, and drives the proxy core.
package lite

import (
	"context"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"ashgate-lite/internal/proxycore"
)

// Host controls the native windows. window is "main" or "manage".
type Host interface {
	Minimize(window string)
	Close(window string)
	OpenManage()
	StartDrag(window string)
	StartResize(window, edge string)
}

// Discoverer finds applications on this PC.
type Discoverer interface {
	// Suggest lists matches for query, running processes first, then Start
	// menu shortcuts, then Program Files.
	Suggest(query string) []Suggestion
	// Running returns the exe paths of running processes, keyed by
	// proxycore.StatKey.
	Running() map[string]bool
}

// Core is the part of proxycore.Core the service uses.
type Core interface {
	Configure(proxycore.Config) error
	Connect() error
	Disconnect()
	Status() proxycore.Status
	Stats() map[string]proxycore.Stat
}

// Options wires a Service.
type Options struct {
	SettingsPath string
	Core         Core
	Discover     Discoverer
	Host         Host
	// CopyText puts text on the clipboard.
	CopyText func(string) error
	// Ping checks one proxy; nil uses proxycore.Ping.
	Ping func(context.Context, proxycore.Proxy) proxycore.PingResult
	Now  func() time.Time
}

const maxProxies = 3

// Service is safe for concurrent use by the bridge.
type Service struct {
	opt    Options
	events *broadcaster

	mu      sync.Mutex
	set     settings
	proxies map[string]proxycore.Proxy // parsed links, by proxy ID
	health  map[string]*health
	apps    map[string]*appRuntime
	down    *proxyDown
	running map[string]bool
	speeds  map[string]rates // by app ID
	all     *rates
	last    map[string]proxycore.Stat
	found   map[string]Suggestion // last suggestions, by lower-case exe path

	applyMu sync.Mutex

	pubMu      sync.Mutex
	pubPending bool
	pubAt      time.Time
}

// publishGap keeps state events at most 10 per second (design bridge-api.md).
const publishGap = 100 * time.Millisecond

type health struct {
	pingMs    *int64
	available bool
	since     time.Time
	lastErr   string
	checking  bool
}

type appRuntime struct {
	checking  bool
	err       *appError
	clearedAt time.Time
}

// New loads settings and returns a service. Call Start to begin.
func New(opt Options) (*Service, error) {
	if opt.Ping == nil {
		opt.Ping = proxycore.Ping
	}
	if opt.Now == nil {
		opt.Now = time.Now
	}
	set, err := loadSettings(opt.SettingsPath)
	if err != nil {
		log.Printf("settings: %v; starting with defaults", err)
	}
	s := &Service{
		opt:     opt,
		events:  newBroadcaster(),
		set:     set,
		proxies: map[string]proxycore.Proxy{},
		health:  map[string]*health{},
		apps:    map[string]*appRuntime{},
		running: map[string]bool{},
		speeds:  map[string]rates{},
		found:   map[string]Suggestion{},
	}
	s.loadProxies()
	return s, nil
}

func (s *Service) loadProxies() {
	kept := s.set.Proxies[:0]
	for _, sp := range s.set.Proxies {
		p, err := proxycore.ParseLink(sp.Link)
		if err != nil {
			log.Printf("settings: proxy %d dropped: %v", sp.Slot, err)
			continue
		}
		s.proxies[sp.ID] = p
		s.health[sp.ID] = &health{available: true}
		kept = append(kept, sp)
	}
	s.set.Proxies = kept
	for _, a := range s.set.Apps {
		s.apps[a.ID] = &appRuntime{}
	}
}

// Start applies the saved state and runs the background loops until ctx ends.
func (s *Service) Start(ctx context.Context) {
	s.mu.Lock()
	enabled := s.set.Enabled && len(s.set.Proxies) > 0
	s.set.Enabled = false
	s.mu.Unlock()
	s.apply()
	if enabled {
		_ = s.setEnabled(true)
	}
	go s.pingAll(ctx, "")
	go s.trafficLoop(ctx)
	go s.healthLoop(ctx)
}

// Events streams encoded bridge events to one window.
func (s *Service) Events() (<-chan []byte, func()) { return s.events.Subscribe() }

// Theme is the saved theme, for the page before its first paint.
func (s *Service) Theme() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.set.Theme
}

// Shutdown stops capture.
func (s *Service) Shutdown() { s.opt.Core.Disconnect() }

// State builds the current State.
func (s *Service) State() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stateLocked()
}

func (s *Service) stateLocked() State {
	st := State{
		Enabled: s.set.Enabled,
		Proxies: make([]proxyView, 0, len(s.set.Proxies)),
		Apps:    make([]appView, 0, len(s.set.Apps)),
		Others:  "direct",
		Theme:   s.set.Theme,
	}
	if id := s.set.CurrentProxyID; id != "" {
		st.CurrentProxyID = &id
	}
	if id := s.set.OthersProxyID; id != "" {
		st.Others = othersTarget{ProxyID: id}
		st.AllTraffic = s.all
		if st.AllTraffic == nil {
			st.AllTraffic = &rates{}
		}
	}
	for _, sp := range s.set.Proxies {
		st.Proxies = append(st.Proxies, s.proxyViewLocked(sp))
	}
	for _, a := range s.set.Apps {
		st.Apps = append(st.Apps, s.appViewLocked(a))
	}
	if s.set.Enabled {
		st.ProxyDown = s.down
	}
	return st
}

func (s *Service) proxyViewLocked(sp savedProxy) proxyView {
	p := s.proxies[sp.ID]
	h := s.health[sp.ID]
	v := proxyView{
		ID: sp.ID, Slot: sp.Slot, Type: string(p.Type),
		// TODO: insufficient data: no country source (GeoIP); the link's #label stands in.
		Country:    p.Label,
		HostMasked: maskHost(p.Host),
		Port:       p.Port,
		PingMs:     h.pingMs,
		Available:  h.available,
		LastError:  h.lastErr,
	}
	if !h.available {
		v.UnavailableSince = h.since.UnixMilli()
	}
	return v
}

func (s *Service) appViewLocked(a savedApp) appView {
	rt := s.apps[a.ID]
	v := appView{ID: a.ID, Name: a.Name, ExePath: a.ExePath, Status: statusNotRunning}
	running := s.running[proxycore.StatKey(a.ExePath)]
	switch {
	case a.Paused:
		v.Status = statusPaused
	case rt.checking:
		v.Status = statusChecking
	case running && s.set.Enabled && s.down != nil:
		v.Status = statusChecking // kill switch: paused until the proxy answers
	case rt.err != nil:
		v.Status, v.Error = statusError, rt.err
	case running:
		v.Status = statusRunning
	}
	if r, ok := s.speeds[a.ID]; ok {
		v.DownBps, v.UpBps = r.DownBps, r.UpBps
	}
	return v
}

// changed saves settings, reapplies the core and publishes the new state.
func (s *Service) changed() {
	s.mu.Lock()
	if err := s.set.save(s.opt.SettingsPath); err != nil {
		log.Printf("settings: %v", err)
	}
	s.mu.Unlock()
	s.apply()
	s.publish()
}

// publish sends the new state to every window, coalescing bursts.
func (s *Service) publish() {
	s.pubMu.Lock()
	defer s.pubMu.Unlock()
	if s.pubPending {
		return
	}
	wait := publishGap - time.Since(s.pubAt)
	if wait <= 0 {
		s.pubAt = time.Now()
		s.events.emit("state", s.State())
		return
	}
	s.pubPending = true
	time.AfterFunc(wait, func() {
		s.pubMu.Lock()
		s.pubPending, s.pubAt = false, time.Now()
		s.pubMu.Unlock()
		s.events.emit("state", s.State())
	})
}

// apply hands the current routes to the proxy core.
func (s *Service) apply() {
	s.applyMu.Lock()
	defer s.applyMu.Unlock()
	s.mu.Lock()
	cfg := s.coreConfigLocked()
	s.mu.Unlock()
	if err := s.opt.Core.Configure(cfg); err != nil {
		log.Printf("core: %v", err)
	}
}

// coreConfigLocked maps the Lite model to routes: every listed app goes
// through the current proxy unless paused, or sent direct in all-traffic
// mode; everything else follows the footer.
func (s *Service) coreConfigLocked() proxycore.Config {
	cfg := proxycore.Config{Proxies: map[string]proxycore.Proxy{}, Others: s.set.OthersProxyID}
	for id, p := range s.proxies {
		cfg.Proxies[id] = p
	}
	for _, a := range s.set.Apps {
		via := s.set.CurrentProxyID
		if a.Paused || (a.Direct && cfg.Others != "") {
			via = ""
		}
		cfg.Apps = append(cfg.Apps, proxycore.Route{ExePath: a.ExePath, ProxyID: via})
	}
	return cfg
}

// setEnabled connects or disconnects capture.
func (s *Service) setEnabled(on bool) error {
	s.mu.Lock()
	if on && len(s.set.Proxies) == 0 {
		s.mu.Unlock()
		return cmdErr("NO_PROXY", "Сначала вставьте ссылку прокси")
	}
	s.mu.Unlock()
	var err error
	if on {
		err = s.opt.Core.Connect()
	} else {
		s.opt.Core.Disconnect()
	}
	s.mu.Lock()
	s.set.Enabled = on && err == nil
	if !s.set.Enabled {
		s.down = nil
		s.speeds, s.all, s.last = map[string]rates{}, nil, nil
	}
	s.mu.Unlock()
	s.changed()
	if err != nil {
		return cmdErr("CAPTURE", err.Error())
	}
	return nil
}

func (s *Service) proxySlot(id string) int {
	for _, sp := range s.set.Proxies {
		if sp.ID == id {
			return sp.Slot
		}
	}
	return 0
}

func (s *Service) sortProxiesLocked() {
	sort.Slice(s.set.Proxies, func(i, j int) bool { return s.set.Proxies[i].Slot < s.set.Proxies[j].Slot })
}

// freeSlot is the lowest unused proxy number 1..3, or 0 when all are taken.
func (s *Service) freeSlotLocked() int {
	used := map[int]bool{}
	for _, sp := range s.set.Proxies {
		used[sp.Slot] = true
	}
	for slot := 1; slot <= maxProxies; slot++ {
		if !used[slot] {
			return slot
		}
	}
	return 0
}

func lowerPath(p string) string { return strings.ToLower(strings.TrimSpace(p)) }
