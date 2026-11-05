package main

import (
	"fmt"
	"github.com/foursmith/player/internal/projection"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
)

func TestStoredStateRoundTripAndDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	state := loadState(path)
	if !reflect.DeepEqual(state.Preferences, defaultPreferences()) {
		t.Fatal(state)
	}
	state.History["file:example"] = videoHistory{AutoProjection: "mono_360_eqr", Position: 12.5, UpdatedAt: 100}
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
	if err := os.WriteFile(path, []byte(`{"Preferences":{"volume":2,"qualityId":100,"splitCount":0,"locale":"unknown"},"History":{"bad":{"Projection":42,"Position":10},"good":{"AutoProjection":"sbs_180_eqr","Position":3},"invalid-auto":{"Projection":6,"ProjectionManual":true,"AutoProjection":"unknown","Position":5}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	got := loadState(path)
	if got.Preferences.Volume != 1 || got.Preferences.QualityID != 3 || got.Preferences.SplitCount != 1 || got.Preferences.Locale != "en" || len(got.History) != 2 {
		t.Fatal(got)
	}
	if history := got.History["invalid-auto"]; history.AutoProjection != "" || history.Projection != projection.Flat || !history.ProjectionManual || history.Position != 5 {
		t.Fatalf("invalid automatic cache affected the manual choice or position: %+v", history)
	}
}

func TestRecentFilesOrderLimitAndValidation(t *testing.T) {
	recent := []string{}
	for i := range 12 {
		recent = rememberFile(recent, filepath.Join(t.TempDir(), fmt.Sprintf("%d.mp4", i)))
	}
	old := recent[5]
	recent = rememberFile(recent, old)
	if len(recent) != 10 || recent[0] != old || slices.Contains(recent[1:], old) {
		t.Fatal(recent)
	}
	path := filepath.Join(t.TempDir(), "state.json")
	state := storedState{LastFile: old, RecentFiles: []string{old, old, "https://nas/video.mp4", "relative.mp4"}}
	if err := saveState(path, state); err != nil {
		t.Fatal(err)
	}
	if got := loadState(path).RecentFiles; !reflect.DeepEqual(got, []string{old}) {
		t.Fatal(got)
	}
	state.RecentFiles = nil
	if err := saveState(path, state); err != nil {
		t.Fatal(err)
	}
	if got := loadState(path).RecentFiles; len(got) != 0 {
		t.Fatalf("LastFile repopulated cleared recent files: %v", got)
	}
}
