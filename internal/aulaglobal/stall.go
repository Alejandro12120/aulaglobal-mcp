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

func newStallBody(body io.ReadCloser, idle time.Duration) *stallBody {
	watched := &stallBody{ReadCloser: body, idle: idle}
	watched.mu.Lock()
	watched.timer = time.AfterFunc(idle, func() { watched.Close() })
	watched.mu.Unlock()
	return watched
}
func (s *stallBody) Read(buffer []byte) (int, error) {
	count, err := s.ReadCloser.Read(buffer)
	s.mu.Lock()
	if count > 0 && !s.closed {
		s.timer.Reset(s.idle)
	}
	s.mu.Unlock()
	return count, err
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
