package proxy

import (
	"sync"

	"github.com/chainreactors/aiscan/core/eventbus"
)

// bodyStream is the AIScan observation side of an io.TeeReader. Bytes passed
// to the bus are borrowed until Emit returns; async subscribers must clone
// them on admission. File limits and subscriber failures never limit the
// stream itself. The bounded preview is available even without subscribers.
type bodyStream struct {
	mu      sync.Mutex
	events  eventbus.Bus[[]byte]
	preview []byte
	size    int64
	closed  bool
}

func (s *bodyStream) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := len(p)
	if s.closed {
		return n, nil
	}
	s.size += int64(n)
	s.preview = appendPreview(s.preview, p, maxBodySnip)
	for len(p) > 0 {
		count := min(len(p), 32<<10)
		s.events.Emit(p[:count])
		p = p[count:]
	}
	return n, nil // observation must never change forwarding
}

// Close freezes observation, not consumers. It never waits for file or
// network I/O. The adapter subsequently drains each consumer independently.
func (s *bodyStream) Close() {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
}

func (s *bodyStream) snapshot() ([]byte, int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]byte(nil), s.preview...), s.size
}
