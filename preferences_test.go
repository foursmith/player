package main

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/foursmith/player/internal/projection"
	"github.com/foursmith/player/library"
)

func TestStoredStateRoundTripAndDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	state := loadState(path)
	if !reflect.DeepEqual(state.Preferences, defaultPreferences()) {
		t.Fatal(state)
	}
	state.History[library.Identity(library.Entry{Path: "file:example"})] = videoHistory{AutoProjection: "mono_360_eqr", Position: 12.5, UpdatedAt: 100}
	state.Preferences.Locale = "zh-CN"
	if err := saveState(path, state); err != nil {
		t.Fatal(err)
	}
	if got := loadState(path); !reflect.DeepEqual(got, state) {
		t.Fatalf("round trip: %+v", got)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(entries) != 1 {
		t.Fatal("temporary state files leaked", entries, err)
	}
}
func TestStoredStateRejectsCorruptionAndInvalidHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte(`{"Preferences":{"volume":.5`), 0600); err != nil {
		t.Fatal(err)
	}
	if got := loadState(path).Preferences; !reflect.DeepEqual(got, defaultPreferences()) {
		t.Fatal(got)
	}
	if err := os.WriteFile(path, []byte(`{"History":null}`), 0600); err != nil {
		t.Fatal(err)
	}
	if got := loadState(path); got.History == nil {
		t.Fatal("empty history must remain writable after loading")
	}
	data := fmt.Sprintf(`{"Preferences":{"volume":2,"splitCount":0,"locale":"unknown"},"History":{"%s":{"Projection":42,"Position":10},"%s":{"AutoProjection":"sbs_180_eqr","Position":3},"%s":{"Projection":6,"ProjectionManual":true,"AutoProjection":"unknown","Position":5}}}`, library.Identity(library.Entry{Path: "bad"}), library.Identity(library.Entry{Path: "good"}), library.Identity(library.Entry{Path: "invalid-auto"}))
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	got := loadState(path)
	if got.Preferences.Volume != 1 || got.Preferences.SplitCount != 1 || got.Preferences.Locale != "en" || len(got.History) != 2 {
		t.Fatal(got)
	}
	if history := got.History[library.Identity(library.Entry{Path: "invalid-auto"})]; history.AutoProjection != "" || history.Projection != projection.Flat || !history.ProjectionManual || history.Position != 5 {
		t.Fatalf("invalid automatic cache affected the manual choice or position: %+v", history)
	}
}

func TestRecentFilesOrderLimitAndValidation(t *testing.T) {
	recent := []savedPlayback{}
	for i := range 12 {
		path := filepath.Join(t.TempDir(), fmt.Sprintf("%d.mp4", i))
		item := savedPlayback{Path: path, Playlist: &playlistSnapshot{Entries: []playlistEntryReference{{Path: path}}}}
		recent = normalizeRecentMedia(append([]savedPlayback{item}, recent...))
	}
	old := recent[5]
	recent = normalizeRecentMedia(append([]savedPlayback{old}, recent...))
	if len(recent) != 10 || recent[0] != old || slices.Contains(recent[1:], old) {
		t.Fatal(recent)
	}
	path := filepath.Join(t.TempDir(), "state.json")
	state := storedState{LastPlayback: &old, RecentMedia: []savedPlayback{old, old, {Path: "https://nas/video.mp4"}, {Path: "relative.mp4"}}}
	if err := saveState(path, state); err != nil {
		t.Fatal(err)
	}
	if got := loadState(path).RecentMedia; !reflect.DeepEqual(got, []savedPlayback{old}) {
		t.Fatal(got)
	}
	state.RecentMedia = nil
	if err := saveState(path, state); err != nil {
		t.Fatal(err)
	}
	if got := loadState(path).RecentMedia; len(got) != 0 {
		t.Fatalf("Startup playback repopulated cleared recent files: %v", got)
	}
}
