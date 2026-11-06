package main

import (
	"context"
	"sync"
	"time"

	"github.com/foursmith/player/internal/media"
	"github.com/foursmith/player/library"
)

// Resolving a remote row never blocks the UI and does not replace a playing
// video until the requested item is ready. A later selection cancels this work.
func (a *application) playEntry(index int) {
	a.playbackRefreshAttempt = ""
	position := 0.0
	if index >= 0 && index < len(a.entries) && a.preferences.ResumePlaybackPosition && a.preferences.RecordPlaybackHistory {
		position = a.stored.History[library.Identity(a.entries[index])].Position
	}
	a.playEntryAt(index, position)
}
func (a *application) playEntryAt(index int, position float64) {
	if index < 0 || index >= len(a.entries) {
		return
	}
	a.cancelPlaybackRestore()
	entry := a.entries[index]
	if entry.SourceID == "" {
		a.startEntry(index, library.Source{}, 0)
		return
	}
	source, found := a.playbackSource(entry.SourceID)
	if !found {
		a.notice = a.t("media.playbackUnavailable")
		a.refreshWindows()
		return
	}
	generation := a.importGeneration
	a.playbackRestoreCancel = readPlayback(a.ctx, source, entry, position, a.window.Update, func(updated library.Source, resolved library.Entry, err error) {
		if a.shutdown || generation != a.importGeneration || index >= len(a.entries) || a.entries[index].Key() != entry.Key() {
			if err == nil {
				discardPreparedPlayback(updated, resolved)
			}
			return
		}
		a.playbackRestoreCancel = nil
		if err != nil {
			a.notice = a.t("library.readFailed") + ": " + err.Error()
			a.refreshWindows()
			return
		}
		a.entries[index] = resolved
		a.playlistSnapshot = nil
		if current := a.playlistRemote[source.ID]; current != nil {
			current.saved = updated
		}
		if i := a.catalogIndex(source.ID); i >= 0 {
			a.stored.Libraries[i].Config = updated.Config
		}
		a.startEntry(index, updated, position)
	})
}

func readPlayback(parent context.Context, source library.Source, entry library.Entry, position float64, dispatch func(func()), complete func(library.Source, library.Entry, error)) context.CancelFunc {
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	source = library.CloneSources([]library.Source{source})[0]
	go func() {
		var err error
		source, entry, err = resolveSourceEntry(ctx, source, entry, &library.PlayRequest{Reference: entry.Reference(), Position: position})
		// Cancellation also releases a known session if its queued UI update is
		// never delivered, for example when the window closes during shutdown.
		var handoff sync.Once
		discard := func() { discardPreparedPlayback(source, entry) }
		stop := context.AfterFunc(ctx, func() { handoff.Do(discard) })
		dispatch(func() {
			defer cancel()
			defer stop()
			if parent.Err() == nil && ctx.Err() != context.Canceled {
				if ctx.Err() != nil && err == nil {
					err = ctx.Err()
				}
				if err != nil {
					handoff.Do(discard)
					complete(source, entry, err)
					return
				}
				accepted := false
				handoff.Do(func() { accepted = true })
				if accepted {
					complete(source, entry, nil)
				} else if ctx.Err() == context.DeadlineExceeded {
					complete(source, entry, ctx.Err())
				}
			} else {
				handoff.Do(discard)
			}
		})
	}()
	return cancel
}

func resolveSourceEntry(ctx context.Context, source library.Source, entry library.Entry, request *library.PlayRequest) (library.Source, library.Entry, error) {
	// An old prepared resource belongs to its existing monitor, never to this request.
	entry.Playback = nil
	session, err := libraryPlugins.Open(ctx, source)
	if err != nil {
		return source, entry, err
	}
	var playback library.Playback
	if request == nil {
		playback, err = library.ResolvePlayback(ctx, session, entry.Reference())
	} else {
		playback, err = library.PreparePlayback(ctx, session, *request)
	}
	source = library.CloneSources([]library.Source{session.Source()})[0]
	if err == nil || request != nil && len(playback.SessionData) > 0 {
		entry.Path = playback.URL
		entry.Subtitle = playback.Subtitle.URL
		entry.Playback = &playback
	}
	closeErr := session.Close()
	if err == nil {
		err = closeErr
	}
	return source, entry, err
}

// Renew a failed session once per explicit selection. Opening a different row
// cancels the refresh through the normal import/request generation boundary.
func (a *application) refreshFailedPlayback(state media.State) {
	if state.PlaybackID != a.playbackID || state.Error == "" || a.selected < 0 || a.selected >= len(a.entries) || a.playbackRestoreCancel != nil {
		return
	}
	entry := a.entries[a.selected]
	if entry.SourceID == "" || entry.Playback == nil || state.URL != entry.Path || a.playbackRefreshAttempt == entry.Key() {
		return
	}
	a.playbackRefreshAttempt = entry.Key()
	a.playEntryAt(a.selected, state.Position)
}
