package main

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/foursmith/player/internal/library"
)

func TestPlaylistModelTracksExpansionAndLibraryChanges(t *testing.T) {
	a := &application{
		entries:         []library.Entry{{Name: "Episode 10.mp4", Path: "10", Group: "folder", SourceID: "remote"}, {Name: "Episode 2.mp4", Path: "2", Group: "folder", SourceID: "remote"}},
		folders:         []library.Folder{{ID: "folder", Name: "Remote", SourceID: "remote"}},
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
	a.entries, a.folders = nil, nil
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
