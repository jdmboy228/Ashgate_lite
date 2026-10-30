package capture

import (
	"context"
	"sync"
)

// Session is one running capture engine.
type Session struct {
	eng    *Engine
	cancel context.CancelFunc
	done   chan struct{}

	mu  sync.Mutex
	err error
}

// Apply installs a new plan. Existing TCP flows keep their proxy.
func (s *Session) Apply(plan Plan) error {
	if err := s.eng.Apply(plan); err != nil {
		return err
	}
	go s.eng.RefreshBypass(context.Background())
	return nil
}

// Stats returns per-rule counters; see Engine.Stats.
func (s *Session) Stats() map[string]Stat { return s.eng.Stats() }

// Stop asks the engine to exit. It is safe to call more than once.
func (s *Session) Stop() { s.cancel() }

// Done is closed after the driver handles have been released.
func (s *Session) Done() <-chan struct{} { return s.done }

// Err is the first background failure, if the capture loop stopped by itself.
func (s *Session) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

func (s *Session) setErr(err error) {
	s.mu.Lock()
	if s.err == nil {
		s.err = err
	}
	s.mu.Unlock()
}
