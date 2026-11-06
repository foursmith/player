package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/foursmith/player/internal/media"
	"github.com/foursmith/player/internal/providers"
	"github.com/foursmith/player/library"
)

func TestStartupPlaybackRestoresPlaylistWithoutRecentMenuAfterRestart(t *testing.T) {
	for _, scenario := range []string{"local", "dlna", "dlna-flat"} {
		t.Run(scenario, func(t *testing.T) {
			remote := scenario != "local"
			device, revision := dlnaCatalogFixture(t)
			items, err := browseDLNAFixture(context.Background(), device, "0")
			if err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			for _, name := range []string{"first.mp4", "last.mp4"} {
				if err := os.WriteFile(filepath.Join(dir, name), nil, 0600); err != nil {
					t.Fatal(err)
				}
			}
			local, _, err := library.FromPaths(context.Background(), []string{dir})
			if err != nil {
				t.Fatal(err)
			}
			a := modeTestApplication(t)
			a.preferences.AutoResume = true
			a.preferences.RecentFilesEnabled = false
			a.entries = []library.Entry{local[0], items[1].Entry, local[1]}
			if remote {
				a.selected = 1
			}
			saved := library.Source{ID: device.ID, Name: device.Name, Plugin: providers.DLNAID, Config: library.EncodeConfig(providers.DLNAConfig{DeviceID: device.ID, Location: device.Location})}
			source := &playlistRemoteSource{saved: saved, folders: map[string]*playlistRemoteFolder{}}
			source.update(source.folder("0"), items)
			a.playlistRemote = map[string]*playlistRemoteSource{device.ID: source}
			if scenario == "dlna-flat" {
				a.playlistRemote = nil
				a.stored.Libraries = []library.Source{saved}
			}
			a.expandedFolders = map[string]bool{dir: true, playlistRemoteFolderID(device.ID, "0"): true}
			a.invalidatePlaylist()
			a.updateHistoryRecording()
			entry := a.entries[a.selected]
			a.recordPlaybackHistory(entry, media.State{URL: entry.Path, Position: 42})
			if a.stored.LastPlayback == nil || a.stored.LastPlayback.key() != entry.Key() || len(a.stored.RecentMedia) != 0 {
				t.Fatal("startup tracking depends on the recent menu or lost the active selection")
			}
			path := filepath.Join(t.TempDir(), "state.json")
			if err := saveState(path, a.stored); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(data, []byte("token=")) || bytes.Contains(data, []byte("captions.srt")) {
				t.Fatal("startup tracking persisted temporary URLs")
			}
			reloaded := loadState(path)
			if reloaded.LastPlayback == nil || reloaded.Preferences.RecentFilesEnabled || len(reloaded.RecentMedia) != 0 {
				t.Fatal("startup playback did not survive a restart independently of recent files")
			}
			revision.Add(1)
			restored, err := resolveSavedPlayback(context.Background(), *reloaded.LastPlayback, nil)
			if err != nil || len(restored.entries) != 3 || restored.selected != a.selected {
				t.Fatal("startup playback did not restore the previous queue and selection", restored, err)
			}
			a.stored = reloaded
			a.stored.LastPlayback, a.stored.Libraries = nil, nil
			a.installPlaylist(restored)
			a.selected = restored.selected
			if start := a.prepareVideoMode(a.entries[a.selected]); start != 42 {
				t.Fatal("startup playlist restoration lost the saved playback position", start)
			}
			if a.entries[1].Path == entry.Path && remote || a.entries[1].Subtitle == "" {
				t.Fatal("startup playback reused a stale DLNA URL or lost subtitles")
			}
			a.rememberStartupPlayback(a.entries[a.selected])
			if a.stored.LastPlayback == nil {
				t.Fatal("restored DLNA source information was lost when recording the next startup")
			}
			if scenario == "dlna-flat" {
				for _, node := range a.playlistModel() {
					if node.ID == playlistRemoteFolderID(device.ID, "0") {
						t.Fatal("startup restoration replaced a direct video row with a device tree")
					}
				}
			}
			if _, err := resolveSavedPlayback(context.Background(), *a.stored.LastPlayback, nil); err != nil {
				t.Fatal("the restored playlist could not be used on the following startup", err)
			}
		})
	}
}

func TestStartupPlaybackSnapshotTracksEditsAndClearingWithoutRecentMenu(t *testing.T) {
	a := modeTestApplication(t)
	a.preferences.AutoResume = true
	a.preferences.RecentFilesEnabled = false
	a.entries[0].Path = filepath.Join(t.TempDir(), "first.mp4")
	a.rememberStartupPlayback(a.entries[0])
	original := a.stored.LastPlayback
	a.entries = append(a.entries, library.Entry{Name: "next.mp4", Path: filepath.Join(t.TempDir(), "next.mp4")})
	a.invalidatePlaylist()
	a.persist()
	if len(original.Playlist.Entries) != 1 || a.stored.LastPlayback == original || len(a.stored.LastPlayback.Playlist.Entries) != 2 {
		t.Fatal("queue edits did not update startup playback with an independent immutable snapshot")
	}
	a.clearRecentFiles()
	a.persist()
	if a.stored.LastPlayback != nil {
		t.Fatal("periodic saves repopulated cleared startup playback")
	}
	for _, disabled := range []string{"autoResume", "history"} {
		a.preferences.AutoResume, a.preferences.RecordPlaybackHistory = true, true
		a.rememberStartupPlayback(a.entries[0])
		if disabled == "autoResume" {
			a.preferences.AutoResume = false
		} else {
			a.preferences.RecordPlaybackHistory = false
		}
		a.persist()
		if a.stored.LastPlayback != nil {
			t.Fatalf("disabling %s retained startup playback", disabled)
		}
	}
}

func TestStateWriterOwnsStartupPlaybackRecord(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	original := filepath.Join(dir, "original.mp4")
	state := storedState{Preferences: defaultPreferences(), LastPlayback: &savedPlayback{Name: "original.mp4", Path: original, Playlist: &playlistSnapshot{Entries: []playlistEntryReference{{Name: "original.mp4", Path: original}}}}}
	state.Preferences.AutoResume = true
	w := newStateWriter(path, nil)
	w.Submit(state)
	state.LastPlayback.Path = filepath.Join(dir, "changed.mp4")
	state.LastPlayback.Playlist = nil
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if got := loadState(path).LastPlayback; got == nil || got.Path != original || len(got.Playlist.Entries) != 1 {
		t.Fatal("the state writer shared mutable startup playback with the UI", got)
	}
}
