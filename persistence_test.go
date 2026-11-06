package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/foursmith/player/internal/media"
	"github.com/foursmith/player/internal/projection"
	"github.com/foursmith/player/library"
)

func TestStateWriterFlushesLatestIndependentSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	w := newStateWriter(path, nil)
	state := storedState{Preferences: defaultPreferences(), History: map[string]videoHistory{}}
	state.RecentMedia = []savedPlayback{{Name: "original.mp4", Path: filepath.Join(t.TempDir(), "original.mp4")}}
	state.RecentMedia[0].Playlist = &playlistSnapshot{Entries: []playlistEntryReference{{Name: "original.mp4", Path: state.RecentMedia[0].Path}}}
	originalFolder := filepath.Join(t.TempDir(), "offline")
	state.Libraries = []library.Source{{ID: "collection", Name: "Original", Plugin: library.LocalID, Config: library.EncodeConfig(library.LocalConfig{Paths: []string{state.RecentMedia[0].Path}, Folders: []string{originalFolder}})}}
	state.Preferences.Shortcuts = map[string]keyChord{"playPause": {Key: ui.KeyP}}
	originalRecent := state.RecentMedia[0]
	for i := range 100 {
		state.History[library.Identity(library.Entry{Path: "video"})] = videoHistory{Position: float64(i)}
		w.Submit(state)
	}
	// Mutating UI state after submission cannot change the saved snapshot.
	state.History[library.Identity(library.Entry{Path: "video"})] = videoHistory{Position: 999}
	state.RecentMedia[0].Path = filepath.Join(t.TempDir(), "mutated.mp4")
	state.RecentMedia[0].Playlist = &playlistSnapshot{Entries: []playlistEntryReference{{Name: "mutated.mp4", Path: state.RecentMedia[0].Path}}}
	state.Libraries[0].Name = "Mutated"
	state.Libraries[0].Config[0] = ' '
	state.Preferences.Shortcuts["playPause"] = keyChord{Key: ui.KeyJ}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if got := loadState(path).History[library.Identity(library.Entry{Path: "video"})].Position; got != 99 {
		t.Fatalf("final snapshot was lost or shared mutable UI state: %v", got)
	}
	if got := loadState(path).Preferences.Shortcuts["playPause"]; got.Key != ui.KeyP {
		t.Fatalf("shortcuts shared mutable state: %+v", got)
	}
	if got := loadState(path).RecentMedia; len(got) != 1 || !reflect.DeepEqual(got[0], originalRecent) {
		t.Fatalf("recent files shared mutable UI state: %v", got)
	}
	if got := loadState(path).Libraries; len(got) != 1 || got[0].Name != "Original" || len(got[0].Local().Paths) != 1 || got[0].Local().Paths[0] != originalRecent.Path || len(got[0].Local().Folders) != 1 || got[0].Local().Folders[0] != originalFolder {
		t.Fatalf("library records shared mutable UI state: %+v", got)
	}
}

func TestPendingPlaybackDoesNotOverwriteSavedHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	entry := library.Entry{Name: "new.mp4", Path: filepath.Join(t.TempDir(), "new.mp4")}
	history := videoHistory{Projection: projection.Mono360, Position: 42, UpdatedAt: 123}
	a := &application{
		preferences: defaultPreferences(),
		stored:      storedState{History: map[string]videoHistory{library.Identity(entry): history}},
		entries:     []library.Entry{entry},
		selected:    0,
		mode:        projection.SBS180,
		player:      media.NewPlayer(nil),
		stateWriter: newStateWriter(path, nil),
	}
	defer a.player.Close()
	// Selection has changed, while the worker has yet to publish that file.
	a.persist()
	if err := a.stateWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if got := loadState(path).History[library.Identity(entry)]; got != history {
		t.Fatalf("unrelated player snapshot overwrote history: %+v", got)
	}
}

func TestStateWriterReportsFinalWriteFailure(t *testing.T) {
	dir := t.TempDir()
	blocked := filepath.Join(dir, "file")
	if err := os.WriteFile(blocked, nil, 0600); err != nil {
		t.Fatal(err)
	}
	failed := make(chan error, 1)
	w := newStateWriter(filepath.Join(blocked, "state.json"), func(err error) { failed <- err })
	w.Submit(storedState{})
	if err := w.Close(); err == nil {
		t.Fatal("shutdown silently discarded a failed save")
	}
	select {
	case <-failed:
	default:
		t.Fatal("background save failure was not reported")
	}
}
