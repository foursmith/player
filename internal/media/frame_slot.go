package media

import "sync"

// frameSlot shares the player's display frame with concurrent readers.
// load returns an owned reference. store takes ownership and belongs to the
// playback worker.
type frameSlot struct {
	mu    sync.Mutex
	frame *Frame
}

func (s *frameSlot) load() *Frame { s.mu.Lock(); defer s.mu.Unlock(); return s.frame.Clone() }
func (s *frameSlot) store(frame *Frame) {
	s.mu.Lock()
	old := s.frame
	s.frame = frame
	s.mu.Unlock()
	old.Close()
}
