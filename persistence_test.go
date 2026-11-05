package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStateWriterFlushesLatestIndependentSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	w := newStateWriter(path, nil)
	state := storedState{Preferences: defaultPreferences(), History: map[string]videoHistory{}}
	for i := range 100 {
		state.History["video"] = videoHistory{Position: float64(i)}
		w.Submit(state)
	}
	// Mutating UI state after submission cannot change the saved snapshot.
	state.History["video"] = videoHistory{Position: 999}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if got := loadState(path, "").History["video"].Position; got != 99 {
		t.Fatalf("final snapshot was lost or shared mutable UI state: %v", got)
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
