package main

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/foursmith/player/internal/library"
)

func TestPlaylistModelTracksExpansionAndLibraryChanges(t *testing.T) {
	a := &application{
		entries:         []library.Entry{{Name: "Episode 10.mp4", Path: "10", Group: "folder", Root: "."}, {Name: "Episode 2.mp4", Path: "2", Group: "folder", Root: "."}},
		expandedFolders: map[string]bool{},
	}
	ids := func() []string {
		var out []string
		for _, row := range a.visiblePlaylist() {
			out = append(out, row.node.ID)
		}
		return out
	}
	if got := ids(); !reflect.DeepEqual(got, []string{"folder"}) {
		t.Fatal(got)
	}
	a.expandedFolders["folder"] = true
	a.playlistRowsValid = false
	if got := ids(); !reflect.DeepEqual(got, []string{"folder", "2", "10"}) {
		t.Fatal(got)
	}
	if got := a.visiblePlaylist()[1].depth; got != 1 {
		t.Fatalf("folder indentation lost: %v", got)
	}
	a.entries = a.entries[:1]
	a.invalidatePlaylist()
	if got := ids(); !reflect.DeepEqual(got, []string{"folder", "10"}) {
		t.Fatal("removed entries survived cache invalidation", got)
	}
	a.entries = nil
	a.invalidatePlaylist()
	if len(a.visiblePlaylist()) != 0 {
		t.Fatal("cleared playlist retained old rows")
	}
}

func BenchmarkPlaylistModel(b *testing.B) {
	a := &application{expandedFolders: map[string]bool{}}
	for i := range 10000 {
		a.entries = append(a.entries, library.Entry{Name: fmt.Sprintf("Episode %d.mp4", 10000-i), Path: fmt.Sprint(i)})
	}
	b.Run("rebuild", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			a.invalidatePlaylist()
			a.visiblePlaylist()
		}
	})
	b.Run("cached", func(b *testing.B) {
		a.visiblePlaylist()
		b.ReportAllocs()
		for b.Loop() {
			a.visiblePlaylist()
		}
	})
}

func TestOpenReplacesQueueWhileAppendKeepsSelection(t *testing.T) {
	current := []library.Entry{{Path: "old.mp4"}, {Path: "http://nas/video", DeviceID: "dlna-nas"}}
	incoming := []library.Entry{{Name: "Episode 10.mp4", Path: "10.mp4"}, {Name: "Episode 2.mp4", Path: "2.mp4"}, {Name: "Episode 2.mp4", Path: "2.mp4"}}
	queue, selected, play := importedPlaylist(current, incoming, 0, false)
	if len(queue) != 2 || selected != 1 || !play || queue[selected].Path != "2.mp4" {
		t.Fatalf("open didn't replace and play the requested files: %+v, %d, %v", queue, selected, play)
	}
	// The decision depends on selection, never on playing vs paused state.
	queue, selected, play = importedPlaylist(current, incoming, 0, true)
	if len(queue) != 4 || selected != 0 || play || queue[0].Path != "old.mp4" {
		t.Fatalf("append interrupted the selection: %+v, %d, %v", queue, selected, play)
	}
	queue, selected, play = importedPlaylist(nil, incoming, -1, true)
	if len(queue) != 2 || selected != 1 || !play {
		t.Fatalf("empty queue didn't start its first video: %+v, %d, %v", queue, selected, play)
	}
}
