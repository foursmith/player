package main

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/foursmith/player/internal/media"
	"github.com/foursmith/player/library"
)

type recordingPlugin struct {
	integrationPlugin
	events      chan library.PlaybackEvent
	credentials chan string
}

func (p *recordingPlugin) Report(ctx context.Context, source library.Source, ref library.Reference, playback library.Playback, event library.PlaybackEvent, host library.Host) error {
	p.events <- event
	if p.credentials != nil {
		p.credentials <- source.CredentialRef
	}
	return nil
}

// A reconnect must not change an already prepared session's report credentials.
// Canceling before its decoder opens must not borrow the old same-URL position.
func TestPreparedPlaybackRetainsCredentialsAndRejectsStaleStop(t *testing.T) {
	plugin := &recordingPlugin{integrationPlugin: integrationPlugin{id: t.Name()}, events: make(chan library.PlaybackEvent, 1), credentials: make(chan string, 1)}
	if err := libraryPlugins.Register(plugin); err != nil {
		t.Fatal(err)
	}
	a := modeTestApplication(t)
	a.playbackID = 2
	prepared := library.Source{ID: "account", Name: "Account", Plugin: plugin.id, Config: library.EncodeConfig(map[string]string{}), CredentialRef: "original-login"}
	current := prepared
	current.CredentialRef = "new-login"
	a.stored.Libraries = []library.Source{current}
	entry := library.Entry{SourceID: prepared.ID, ItemID: "movie", Path: "https://same.test/video", Playback: &library.Playback{Seekable: true, SessionData: []byte(`{"id":"session"}`)}}
	a.startPlaybackSession(prepared, entry, 42)
	session := a.playbackSession
	if session == nil {
		t.Fatal("prepared playback has no monitor")
	}
	session.stop <- media.State{URL: entry.Path, PlaybackID: 1, Generation: 10, Position: 99}
	select {
	case <-session.done:
	case <-time.After(3 * time.Second):
		t.Fatal("stopped session did not finish")
	}
	if got := <-plugin.credentials; got != prepared.CredentialRef {
		t.Fatal("reconnection changed active session credentials", got)
	}
	if event := <-plugin.events; event.Type != "stop" || event.Position != 42 || !event.Failed {
		t.Fatal("obsolete decoder changed the canceled session position", event)
	}
}

// Playback reports belong to the selected session, including paused time and final position.
func TestPlaybackSessionFollowsPlayerAndStopsOnce(t *testing.T) {
	plugin := &recordingPlugin{integrationPlugin: integrationPlugin{id: t.Name()}, events: make(chan library.PlaybackEvent, 10)}
	registry := library.NewRegistry(library.Host{}, plugin)
	source := library.Source{ID: "account", Name: "Account", Plugin: plugin.id, Config: library.EncodeConfig(map[string]string{})}
	entry := library.Entry{SourceID: source.ID, ItemID: "movie", Path: "https://same.test/video", Playback: &library.Playback{Seekable: true, SessionData: []byte(`{"id":"session"}`)}}
	session := &playbackSession{stop: make(chan media.State, 1), done: make(chan struct{}), id: 2}
	ticks := make(chan time.Time)
	var state atomic.Pointer[media.State]
	state.Store(&media.State{URL: entry.Path, PlaybackID: 1, Generation: 10, Playing: true, FrameID: 10})
	go session.run(context.Background(), source, entry, ticks, func() media.State { return *state.Load() }, registry)
	base := time.Unix(100, 0)
	ticks <- base // A previous decoder at the same URL must not start this session.
	state.Store(&media.State{URL: entry.Path, PlaybackID: 2, Generation: 11, Loading: true})
	ticks <- base.Add(time.Second)
	state.Store(&media.State{URL: entry.Path, PlaybackID: 2, Generation: 11, Playing: true, FrameID: 20, Position: 42, Metadata: media.Metadata{Duration: 600}})
	ticks <- base.Add(2 * time.Second)
	receive := func(kind string, position float64) {
		t.Helper()
		select {
		case event := <-plugin.events:
			if event.Type != kind || event.Position != position {
				t.Fatal(event)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("playback event missing", kind)
		}
	}
	receive("start", 42)
	state.Store(&media.State{URL: entry.Path, PlaybackID: 2, Generation: 12, FrameID: 20, Position: 44, Metadata: media.Metadata{Duration: 600}})
	ticks <- base.Add(3 * time.Second)
	receive("progress", 44)
	final := *state.Load()
	final.Position = 45
	session.stop <- final
	receive("stop", 45)
	<-session.done
	select {
	case event := <-plugin.events:
		t.Fatal("duplicate playback event", event)
	default:
	}
}

// A stale state with an identical URL cannot record progress for a new account.
func TestPlaybackIdentityRejectsStaleSameURLHistory(t *testing.T) {
	a := modeTestApplication(t)
	entry := library.Entry{Name: "Movie", SourceID: "new-account", ItemID: "movie", Path: "https://same.test/video"}
	a.entries = []library.Entry{entry}
	a.playbackID = 2
	a.recordPlaybackHistory(entry, media.State{URL: entry.Path, PlaybackID: 1, Generation: 10, Position: 99})
	if _, found := a.stored.History[library.Identity(entry)]; found {
		t.Fatal("previous decoder wrote another account's progress")
	}
	a.recordPlaybackHistory(entry, media.State{URL: entry.Path, PlaybackID: 2, Generation: 11, Position: 42})
	if a.stored.History[library.Identity(entry)].Position != 42 {
		t.Fatal("current decoder progress was discarded")
	}
}
