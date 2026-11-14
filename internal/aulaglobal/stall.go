package aulaglobal

import (
	"io"
	"sync"
	"time"
)

// Closing a net/http response body interrupts a stalled Read. Reset only on bytes.
type stallBody struct {
	io.ReadCloser
	mu     sync.Mutex
	timer  *time.Timer
	idle   time.Duration
	closed bool
}

func newStallBody(b io.ReadCloser, d time.Duration) *stallBody {
	s := &stallBody{ReadCloser: b, idle: d}
	s.mu.Lock()
	s.timer = time.AfterFunc(d, func() { s.Close() })
	s.mu.Unlock()
	return s
}
func (s *stallBody) Read(p []byte) (int, error) {
	n, e := s.ReadCloser.Read(p)
	s.mu.Lock()
	if n > 0 && !s.closed {
		s.timer.Reset(s.idle)
	}
	s.mu.Unlock()
	return n, e
}
func (s *stallBody) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.timer.Stop()
	s.mu.Unlock()
	return s.ReadCloser.Close()
}
