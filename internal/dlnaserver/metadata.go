package dlnaserver

import (
	"context"
	"log"
	"slices"
	"strings"
	"time"

	"github.com/foursmith/player/internal/library"
)

type metadataIndex struct {
	load     func(context.Context, library.Entry) (library.MediaInfo, error)
	queue    chan object
	priority chan object
	wake     chan struct{}
	known    map[string]library.MediaInfo
	active   map[string]bool
	failed   map[string]time.Time
	byKey    map[string][]string
}

func (o object) metadataEntry() library.Entry {
	return library.Entry{Path: o.MetadataPath, Size: o.Size, Modified: o.Modified}
}

// Called with the catalog lock held, or before workers start.
func (s *Server) indexMetadata(objects catalog) {
	m := s.metadata
	if m == nil {
		return
	}
	m.byKey = map[string][]string{}
	for id, item := range objects {
		if item.Container {
			continue
		}
		key := library.Identity(item.metadataEntry())
		m.byKey[key] = append(m.byKey[key], id)
		item.Info = m.known[key]
		objects[id] = item
	}
	for key := range m.known {
		if len(m.byKey[key]) == 0 {
			delete(m.known, key)
		}
	}
	for key := range m.failed {
		if len(m.byKey[key]) == 0 {
			delete(m.failed, key)
		}
	}
}

func (s *Server) startMetadata(load func(context.Context, library.Entry) (library.MediaInfo, error)) {
	if load == nil {
		return
	}
	s.metadata = &metadataIndex{load: load, queue: make(chan object, 32), priority: make(chan object, 64), wake: make(chan struct{}, 1), known: map[string]library.MediaInfo{}, active: map[string]bool{}, failed: map[string]time.Time{}}
	s.indexMetadata(s.objects)
	for range 2 {
		s.wg.Go(s.metadataWorker)
	}
	s.wg.Go(func() {
		for {
			select {
			case <-s.ctx.Done():
				return
			case <-s.metadata.wake:
			}
			items := s.pendingMetadata()
			slices.SortFunc(items, func(a, b object) int { return strings.Compare(a.ID, b.ID) })
			for _, item := range items {
				select {
				case <-s.ctx.Done():
					return
				case s.metadata.queue <- item:
				}
			}
		}
	})
	s.scheduleMetadata()
}

func (s *Server) pendingMetadata() []object {
	s.mu.RLock()
	defer s.mu.RUnlock()
	m := s.metadata
	if m == nil {
		return nil
	}
	now := time.Now()
	var items []object
	for key, ids := range m.byKey {
		if _, known := m.known[key]; known || m.active[key] || now.Before(m.failed[key]) || len(ids) == 0 {
			continue
		}
		// A file shared by several libraries needs only one queued probe.
		items = append(items, s.objects[ids[0]])
	}
	return items
}

func (s *Server) scheduleMetadata() {
	if s.metadata != nil {
		select {
		case s.metadata.wake <- struct{}{}:
		default:
		}
	}
}

func (s *Server) prioritizeMetadata(items []object) {
	if s.metadata == nil {
		return
	}
	for _, item := range items {
		if !item.Container {
			select {
			case s.metadata.priority <- item:
			default:
				return
			}
		}
	}
}

func (s *Server) metadataWorker() {
	m := s.metadata
	for {
		var item object
		select {
		case item = <-m.priority:
		default:
			select {
			case <-s.ctx.Done():
				return
			case item = <-m.priority:
			case item = <-m.queue:
			}
		}
		entry := item.metadataEntry()
		key := library.Identity(entry)
		s.mu.Lock()
		_, known := m.known[key]
		if s.ctx.Err() != nil {
			s.mu.Unlock()
			return
		}
		if known || m.active[key] || len(m.byKey[key]) == 0 || time.Now().Before(m.failed[key]) {
			s.mu.Unlock()
			continue
		}
		m.active[key] = true
		s.mu.Unlock()
		ctx, cancel := context.WithTimeout(s.ctx, 2*time.Second)
		info, err := m.load(ctx, entry)
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		cancel()
		info = info.Normalized()
		s.mu.Lock()
		delete(m.active, key)
		if s.ctx.Err() != nil || len(m.byKey[key]) == 0 {
			s.mu.Unlock()
			continue
		}
		if err != nil {
			m.failed[key] = time.Now().Add(30 * time.Second)
			s.mu.Unlock()
			log.Printf("[dlna-server] metadata unavailable object=%s error=%v", item.ID, err)
			continue
		}
		m.known[key] = info
		changed := false
		for _, id := range m.byKey[key] {
			current := s.objects[id]
			if current.Info != info {
				current.Info = info
				s.objects[id] = current
				changed = true
			}
		}
		if changed {
			s.updateID++
		}
		s.mu.Unlock()
		if changed {
			s.queueEvents()
		}
	}
}
