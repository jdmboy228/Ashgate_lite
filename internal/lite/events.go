package lite

import (
	"encoding/json"
	"sync"
)

// broadcaster fans events out to every open window. A slow window drops
// events instead of blocking the service; the next state catches it up.
type broadcaster struct {
	mu   sync.Mutex
	subs map[chan []byte]struct{}
}

func newBroadcaster() *broadcaster {
	return &broadcaster{subs: map[chan []byte]struct{}{}}
}

// Subscribe returns a channel of encoded events and a func that ends it.
func (b *broadcaster) Subscribe() (<-chan []byte, func()) {
	ch := make(chan []byte, 32)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	return ch, func() {
		b.mu.Lock()
		if _, ok := b.subs[ch]; ok {
			delete(b.subs, ch)
			close(ch)
		}
		b.mu.Unlock()
	}
}

func (b *broadcaster) emit(event string, data any) {
	msg, err := json.Marshal(struct {
		Event string `json:"event"`
		Data  any    `json:"data"`
	}{event, data})
	if err != nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs {
		select {
		case ch <- msg:
		default:
		}
	}
}
