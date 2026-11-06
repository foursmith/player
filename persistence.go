package main

import (
	"maps"
	"slices"
	"sort"
	"time"

	"github.com/foursmith/player/internal/library"
	"github.com/foursmith/player/internal/media"
	"github.com/foursmith/player/internal/projection"
)

// stateWriter serializes disk writes and retains only the latest pending save.
// Submit and Close belong to the UI thread; each submitted map is a snapshot.
type stateWriter struct {
	pending chan storedState
	done    chan struct{}
	err     error
}

func newStateWriter(path string, failed func(error)) *stateWriter {
	w := &stateWriter{pending: make(chan storedState, 1), done: make(chan struct{})}
	go func() {
		defer close(w.done)
		for state := range w.pending {
			w.err = saveState(path, state)
			if w.err != nil && failed != nil {
				failed(w.err)
			}
		}
	}()
	return w
}

func (w *stateWriter) Submit(state storedState) {
	state.History = maps.Clone(state.History)
	if state.LastPlayback != nil {
		item := *state.LastPlayback
		state.LastPlayback = &item
	}
	state.RecentMedia = slices.Clone(state.RecentMedia)
	state.Libraries = library.CloneSaved(state.Libraries)
	state.Preferences.Shortcuts = maps.Clone(state.Preferences.Shortcuts)
	select {
	case <-w.pending:
	default:
	}
	w.pending <- state
}

// Close flushes the final snapshot before the process exits.
func (w *stateWriter) Close() error {
	close(w.pending)
	<-w.done
	return w.err
}

func (a *application) persist() {
	if a.shutdown {
		return
	}
	if !a.preferences.RecordPlaybackHistory || !a.preferences.AutoResume {
		a.stored.LastPlayback = nil
	}
	if a.selected >= 0 && a.selected < len(a.entries) {
		a.recordPlaybackHistory(a.entries[a.selected], a.player.Snapshot())
		a.updatePlaybackPlaylists()
	}
	if len(a.stored.History) > 10000 {
		keys := make([]string, 0, len(a.stored.History))
		for key := range a.stored.History {
			keys = append(keys, key)
		}
		sort.Slice(keys, func(i, j int) bool { return a.stored.History[keys[i]].UpdatedAt < a.stored.History[keys[j]].UpdatedAt })
		for _, key := range keys[:len(keys)-10000] {
			delete(a.stored.History, key)
		}
	}
	a.filterCatalog()
	a.stored.Preferences = a.preferences
	a.syncDLNASharing()
	if a.window != nil && a.menuLocale != a.preferences.Locale {
		a.refreshMenu()
		if a.settingsWindow != nil {
			a.settingsWindow.SetTitle(a.t("common.settings"))
		}
		if a.catalogWindow != nil {
			a.catalogWindow.SetTitle(a.t("library.title"))
		}
	}
	a.refreshWindows()
	a.stateWriter.Submit(a.stored)
	a.lastSaved = time.Now()
}

func (a *application) recordPlaybackHistory(entry library.Entry, state media.State) {
	if !a.preferences.RecordPlaybackHistory || entry.DeviceID != "" && entry.ObjectID == "" || state.URL != entry.Path || state.Loading || state.Error != "" {
		return
	}
	key := library.Identity(entry)
	history := a.stored.History[key]
	if !a.recognitionTest {
		history.ProjectionManual = a.modeManual
		if a.modeManual {
			history.Projection = a.mode
		} else {
			history.Projection = 0
			if !a.detectMode {
				history.AutoProjection = projection.IDs[a.mode]
			}
		}
	}
	history.Position, history.UpdatedAt = state.Position, time.Now().UnixMilli()
	if state.Metadata.Duration > 0 {
		history.Duration = state.Metadata.Duration
		if state.Ended || state.Position >= state.Metadata.Duration*.95 {
			history.Watched = true
		}
	}
	a.stored.History[key] = history
}
