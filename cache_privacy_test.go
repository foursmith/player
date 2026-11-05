package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/foursmith/player/internal/library"
	"github.com/foursmith/player/internal/projection"
)

func TestPlaybackHistoryRetainsOnlyCurrentOpaqueIdentities(t *testing.T) {
	entry := library.Entry{Name: "private-video.mp4", Path: "/private/media/private-video.mp4", Size: 12345, Modified: 67890}
	key := library.Identity(entry)
	saved := videoHistory{Position: 42.5, Projection: projection.Mono360, ProjectionManual: true, AutoProjection: "sbs_180_eqr", UpdatedAt: 123}
	state := storedState{
		Preferences: defaultPreferences(), History: map[string]videoHistory{
			key: saved,
			fmt.Sprintf("%s:%d:%d", entry.Path, entry.Size, entry.Modified): {Position: 99},
			strings.Repeat("z", 64): {Position: 100},
		},
		LastFile: entry.Path, RecentFiles: []string{entry.Path},
	}
	path := filepath.Join(t.TempDir(), "state.json")
	if err := saveState(path, state); err != nil {
		t.Fatal(err)
	}
	normalized := loadState(path)
	if len(normalized.History) != 1 || normalized.History[key] != saved {
		t.Fatal("history retained an invalid key or changed a current entry")
	}
	if normalized.LastFile != "" {
		t.Fatal("startup restoration is disabled but its plaintext path was retained")
	}
	a := modeTestApplication(t)
	a.entries = []library.Entry{entry}
	a.preferences, a.stored = normalized.Preferences, normalized
	if start := a.prepareVideoMode(entry); start != 42.5 || !a.modeManual || a.mode != projection.Mono360 {
		t.Fatal("hashed cache did not restore the saved position and manual video type")
	}
	a.clearRecentFiles()
	if a.stored.History[key] != saved {
		t.Fatal("clearing recent history deleted the hashed cache")
	}
	if err := saveState(path, a.stored); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte(entry.Path)) || bytes.Contains(data, []byte(entry.Name)) {
		t.Fatal("clearing recent history left a plaintext filename or path in the settings file")
	}
	if reloaded := loadState(path); reloaded.History[key] != saved || len(reloaded.History) != 1 {
		t.Fatal("loading the hashed cache again changed its identity or contents")
	}
}

func TestStartupPathIsSavedOnlyWhenExplicitlyEnabled(t *testing.T) {
	a := modeTestApplication(t)
	entry := a.entries[0]
	a.stored.LastFile = entry.Path
	a.persist()
	if a.stored.LastFile != "" {
		t.Fatal("disabled startup restoration retained a filename")
	}
	a.preferences.AutoResume = true
	a.updateHistoryRecording()
	if a.stored.LastFile != entry.Path {
		t.Fatal("enabled startup restoration did not retain its required path")
	}
	a.clearRecentFiles()
	a.persist()
	if a.stored.LastFile != "" {
		t.Fatal("periodic saves repopulated a cleared startup restoration path")
	}
	a.updateHistoryRecording()
	a.preferences.AutoResume = false
	a.updateHistoryRecording()
	if a.stored.LastFile != "" {
		t.Fatal("turning startup restoration off left its plaintext path behind")
	}
}
