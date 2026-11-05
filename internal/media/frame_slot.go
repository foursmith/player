package media

import "sync"

// FrameSlot retains one frame and replaces it atomically. Callers own references
// returned by Frame; Store takes ownership. It bounds export-preview memory.
type FrameSlot struct {
	mu    sync.Mutex
	frame *Frame
}

func (s *FrameSlot) Frame() *Frame { s.mu.Lock(); defer s.mu.Unlock(); return s.frame.Clone() }
func (s *FrameSlot) Store(frame *Frame) {
	s.mu.Lock()
	old := s.frame
	s.frame = frame
	s.mu.Unlock()
	old.Close()
}
func (s *FrameSlot) Close() { s.Store(nil) }
