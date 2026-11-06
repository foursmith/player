package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/foursmith/player/internal/library"
)

func TestRecentPlaylistRestoresMixedQueueTreeOrderAndSelectionAfterRestart(t *testing.T) {
	device, revision := dlnaCatalogFixture(t)
	root, err := library.BrowseDLNA(context.Background(), device, "0")
	if err != nil {
		t.Fatal(err)
	}
	season, err := library.BrowseDLNA(context.Background(), device, "season")
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
	a.entries = []library.Entry{local[0], root[1].Entry, season[0].Entry, local[1]}
	a.selected = 2
	saved := library.Saved{ID: device.ID, Name: "My NAS", DeviceID: device.ID, Location: device.Location}
	source := &playlistDLNASource{saved: saved, folders: map[string]*playlistDLNAFolder{}}
	source.update(source.folder("0"), root)
	source.update(source.folder("season"), season)
	a.playlistDLNA = map[string]*playlistDLNASource{device.ID: source}
	a.expandedFolders = map[string]bool{dir: true, playlistDLNAFolderID(device.ID, "0"): true, playlistDLNAFolderID(device.ID, "season"): true}
	// Retain a root order that differs from natural name ordering.
	a.playlistTree = []library.Node{{ID: playlistDLNAFolderID(device.ID, "0")}, {ID: dir}}
	a.invalidatePlaylist()
	before := a.playlistModel()
	if !a.rememberRecentMedia(a.entries[a.selected]) {
		t.Fatal("selected video did not enter recent playback")
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
		t.Fatal("playlist snapshot retained temporary stream or subtitle URLs")
	}
	item := loadState(path).RecentMedia[0]
	revision.Add(1)
	restored, err := resolveSavedPlayback(context.Background(), item, nil)
	if err != nil || restored.selected != 2 || len(restored.entries) != 4 {
		t.Fatal("previous mixed playlist or selected row was not restored", restored.selected, restored.entries, err)
	}
	if restored.entries[1].Path == a.entries[1].Path || restored.entries[1].Subtitle == "" {
		t.Fatal("restoring the queue did not refresh remote URLs and subtitles")
	}
	for i, entry := range restored.entries {
		if entry.Key() != a.entries[i].Key() || entry.Group != a.entries[i].Group || entry.Root != a.entries[i].Root {
			t.Fatal("previous queue membership, order or local grouping changed", restored.entries)
		}
	}
	// Reopening replaces a completely different current queue.
	a.entries = []library.Entry{{Name: "unrelated.mp4", Path: "/other/unrelated.mp4"}}
	a.installPlaylist(restored)
	if !reflect.DeepEqual(a.playlistModel(), before) || !reflect.DeepEqual(a.expandedFolders, restored.expanded) || !a.playlistExpanded {
		t.Fatal("restoring recent playback lost device ancestry, root order or expanded folders", a.playlistModel(), before)
	}
	if folder := a.playlistDLNA[device.ID].folder("season"); !folder.loaded {
		t.Fatal("restored directory was not hydrated for continued playback")
	}
}

func TestRecentPlaylistSnapshotFollowsEditsWithoutRewritingOlderPlayback(t *testing.T) {
	a := modeTestApplication(t)
	dir := t.TempDir()
	first := library.Entry{Name: "first.mp4", Path: filepath.Join(dir, "first.mp4")}
	second := library.Entry{Name: "second.mp4", Path: filepath.Join(dir, "second.mp4")}
	a.entries = []library.Entry{first}
	a.invalidatePlaylist()
	a.rememberRecentMedia(first)
	original := a.stored.RecentMedia[0].Playlist
	a.entries = append(a.entries, second)
	a.invalidatePlaylist()
	a.updatePlaybackPlaylists()
	if len(original.Entries) != 1 || len(a.stored.RecentMedia[0].Playlist.Entries) != 2 {
		t.Fatal("editing a queue mutated the immutable previous snapshot or failed to save the new queue")
	}
	a.entries = []library.Entry{second}
	a.invalidatePlaylist()
	a.rememberRecentMedia(second)
	if len(a.stored.RecentMedia[0].Playlist.Entries) != 1 || len(a.stored.RecentMedia[1].Playlist.Entries) != 2 {
		t.Fatal("different recent videos did not retain their own previous playlists")
	}
	a.selected = 0
	a.clearRecentFiles()
	a.persist()
	if len(a.stored.RecentMedia) != 0 {
		t.Fatal("periodic snapshot saving repopulated cleared recent playback")
	}
}

func TestRecentPlaylistSkipsMissingNeighborButRequiresSelectedVideo(t *testing.T) {
	dir := t.TempDir()
	present, missing := filepath.Join(dir, "present.mp4"), filepath.Join(dir, "missing.mp4")
	if err := os.WriteFile(present, nil, 0600); err != nil {
		t.Fatal(err)
	}
	playlist := &playlistSnapshot{Entries: []playlistEntryReference{{Name: "missing.mp4", Path: missing}, {Name: "present.mp4", Path: present}}}
	item := savedPlayback{Name: "present.mp4", Path: present, Playlist: playlist}
	restored, err := resolveSavedPlayback(context.Background(), item, nil)
	if err != nil || restored.selected != 0 || len(restored.entries) != 1 || restored.entries[0].Path != present {
		t.Fatal("an unavailable neighbor prevented reopening the selected video", restored, err)
	}
	item.Path = missing
	if _, err := resolveSavedPlayback(context.Background(), item, nil); !os.IsNotExist(err) {
		t.Fatal("an unavailable selection opened a different video", err)
	}
}
