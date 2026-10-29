package capture

import (
	"net"
	"sync"
)

// seqAlloc hands out free loopback ports for tests. A fixed starting port
// failed with "address already in use" when tests ran back to back, because
// the previous run's socket on that port was still in TIME_WAIT.
type seqAlloc struct {
	mu sync.Mutex
}

func (s *seqAlloc) Reserve(ver int) (uint16, func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for {
		ln, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			return 0, nil, err
		}
		port := ln.Addr().(*net.TCPAddr).Port
		ln.Close()
		if port >= 20000 {
			return uint16(port), func() {}, nil
		}
	}
}
