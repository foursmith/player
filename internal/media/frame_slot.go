package media

import "sync"

// FrameSlot retains one frame and replaces it atomically. Callers own references
// returned by Frame; Store takes ownership. It bounds export-preview memory.
type FrameSlot struct {
	mu     sync.Mutex
	frame  *Frame
	closed bool
}

func (s *FrameSlot) Frame() *Frame { s.mu.Lock(); defer s.mu.Unlock(); return s.frame.Clone() }
func (s *FrameSlot) Store(frame *Frame) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		frame.Close()
		return
	}
	old := s.frame
	s.frame = frame
	s.mu.Unlock()
	old.Close()
}

// Close also releases frames submitted by work still finishing after cancel.
func (s *FrameSlot) Close() {
	s.mu.Lock()
	s.closed = true
	old := s.frame
	s.frame = nil
	s.mu.Unlock()
	old.Close()
}
