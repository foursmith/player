package main

import (
	"github.com/foursmith/vr/app/internal/projection"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestStoredStateRoundTripAndDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	state := loadState(path)
	if !reflect.DeepEqual(state.Preferences, defaultPreferences()) {
		t.Fatal(state)
	}
	state.History["file:example"] = videoHistory{Projection: projection.Mono360, Position: 12.5, UpdatedAt: 100}
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
	if err := os.WriteFile(path, []byte(`{"Preferences":{"volume":2,"qualityId":100,"splitCount":0,"locale":"unknown"},"History":{"bad":{"Projection":42,"Position":10},"good":{"Projection":0,"Position":3}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	got := loadState(path)
	if got.Preferences.Volume != 1 || got.Preferences.QualityID != 3 || got.Preferences.SplitCount != 1 || got.Preferences.Locale != "en" || len(got.History) != 1 {
		t.Fatal(got)
	}
}
