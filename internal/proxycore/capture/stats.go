package capture

import (
	"sync"
	"sync/atomic"
	"time"
)

// ruleStat counts the traffic of one rule, including child processes that
// inherited it. A nil *ruleStat ignores every update.
type ruleStat struct {
	flows    atomic.Int64
	up, down atomic.Int64
	mu       sync.Mutex
	err      string
	errAt    time.Time
}

func (s *ruleStat) addUp(n int64) {
	if s != nil {
		s.up.Add(n)
	}
}

func (s *ruleStat) addDown(n int64) {
	if s != nil {
		s.down.Add(n)
	}
}

func (s *ruleStat) fail(msg string, at time.Time) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.err, s.errAt = msg, at
	s.mu.Unlock()
}

func (s *ruleStat) closeFlow() {
	if s != nil {
		s.flows.Add(-1)
	}
}

// Stat is a copy of one rule's counters since the engine started.
type Stat struct {
	Flows int64
	Up    int64
	Down  int64
	Err   string
	ErrAt time.Time
}

// statFor returns the counters for key. Callers hold e.mu.
func (e *Engine) statFor(key string) *ruleStat {
	s := e.stats[key]
	if s == nil {
		s = &ruleStat{}
		e.stats[key] = s
	}
	return s
}

// Stats returns every rule's counters, keyed by lower-case exe path, plus
// OthersKey for the default route.
func (e *Engine) Stats() map[string]Stat {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make(map[string]Stat, len(e.stats))
	for k, s := range e.stats {
		s.mu.Lock()
		out[k] = Stat{Flows: s.flows.Load(), Up: s.up.Load(), Down: s.down.Load(), Err: s.err, ErrAt: s.errAt}
		s.mu.Unlock()
	}
	return out
}
