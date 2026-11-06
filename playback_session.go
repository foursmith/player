package main

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/foursmith/player/internal/media"
	"github.com/foursmith/player/library"
)

type playbackSession struct {
	stop     chan media.State
	done     chan struct{}
	once     sync.Once
	id       uint64
	position float64
}

func (a *application) stopPlaybackSession() <-chan struct{} {
	session := a.playbackSession
	if session == nil {
		return nil
	}
	a.playbackSession = nil
	state := a.player.Snapshot()
	session.once.Do(func() { session.stop <- state })
	return session.done
}
func (a *application) startPlaybackSession(source library.Source, entry library.Entry, position float64) {
	if entry.Playback == nil || entry.SourceID == "" {
		return
	}
	plugin, err := libraryPlugins.Plugin(source.Plugin)
	if err != nil {
		return
	}
	if _, ok := plugin.(library.PlaybackObserver); !ok {
		return
	}
	source = library.CloneSources([]library.Source{source})[0]
	session := &playbackSession{id: a.playbackID, position: position, stop: make(chan media.State, 1), done: make(chan struct{})}
	a.playbackSession = session
	player := a.player
	ctx := a.ctx
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		session.run(ctx, source, entry, ticker.C, player.Snapshot, libraryPlugins)
	}()
}
func (s *playbackSession) run(ctx context.Context, source library.Source, entry library.Entry, ticks <-chan time.Time, snapshot func() media.State, registry *library.Registry) {
	defer close(s.done)
	playback := *entry.Playback
	started := false
	last := media.State{URL: entry.Path, PlaybackID: s.id, Position: s.position}
	var lastReport time.Time
	report := func(kind string, state media.State) {
		requestCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		event := library.PlaybackEvent{Type: kind, Position: state.Position, Duration: state.Metadata.Duration, Paused: !state.Playing, Ended: state.Ended, Failed: state.Error != ""}
		if err := registry.Report(requestCtx, source, entry.Reference(), playback, event); err != nil {
			log.Printf("[plugin] playback report plugin=%s event=%s: %v", source.Plugin, kind, err)
		}
	}
	stop := func(state media.State) {
		if state.URL != entry.Path || state.PlaybackID != s.id {
			state = last
		}
		if !started {
			state.Error = "playback did not start"
		}
		report("stop", state)
	}
	for {
		select {
		case final := <-s.stop:
			stop(final)
			return
		case <-ctx.Done():
			stop(snapshot())
			return
		case now := <-ticks:
			state := snapshot()
			if state.URL != entry.Path || state.PlaybackID != s.id {
				continue
			}
			if state.Error != "" {
				stop(state)
				return
			}
			if state.Loading {
				continue
			}
			fresh := false
			if !started && state.FrameID != 0 {
				fresh = true
				report("start", state)
				started = true
				lastReport = now
			}
			if started && !fresh && (state.Playing != last.Playing || state.Ended != last.Ended || now.Sub(lastReport) >= 10*time.Second) {
				report("progress", state)
				lastReport = now
			}
			last = state
		}
	}
}
func discardPreparedPlayback(source library.Source, entry library.Entry) {
	if entry.Playback == nil || len(entry.Playback.SessionData) == 0 {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = libraryPlugins.Report(ctx, source, entry.Reference(), *entry.Playback, library.PlaybackEvent{Type: "stop", Failed: true})
	}()
}
