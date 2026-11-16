package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/foursmith/player/internal/providers"
	"github.com/foursmith/player/library"
)

func TestLocalSubdirectoryRefreshReadsSnapshot(t *testing.T) {
	root := t.TempDir()
	subdir := filepath.Join(root, "Series")
	if err := os.Mkdir(subdir, 0700); err != nil {
		t.Fatal(err)
	}
	clip := filepath.Join(subdir, "clip.mp4")
	if err := os.WriteFile(clip, []byte("sample"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, config := range []library.LocalConfig{{Folder: root}, {Folders: []string{root}}} {
		source := library.Source{ID: "local", Name: "Videos", Plugin: library.LocalID, Config: library.EncodeConfig(config)}
		a := modeTestApplication(t)
		a.stored.Libraries = []library.Source{source}
		a.catalog.selected = source.ID
		items, err := library.ReadLocal(a.ctx, source)
		if err != nil {
			t.Fatal(err)
		}
		a.setCatalogItems(source, items)
		a.filterCatalog()
		for len(a.catalog.items) == 1 && a.catalog.items[0].Folder {
			a.enterCatalogFolder(a.catalog.items[0])
		}
		path := append([]catalogFolder(nil), a.catalog.directoryPath...)
		if len(path) < 2 {
			t.Fatal("test did not enter a subdirectory")
		}
		for range 2 {
			queued := make(chan func(), 1)
			cancel := readCatalog(a.ctx, source, path[len(path)-1].id, func(f func()) { queued <- f }, func(updated library.Source, items []library.Item, err error) {
				if err != nil {
					t.Fatal("subdirectory refresh failed", err)
				}
				a.setCatalogItems(updated, items)
				a.filterCatalog()
			})
			runQueued(t, queued)
			cancel()
			if len(a.catalog.playable) != 1 || a.catalog.playable[0].Path != clip || !slices.Equal(a.catalog.directoryPath, path) {
				t.Fatal("refresh lost directory or playable video", a.catalog.items, a.catalog.directoryPath)
			}
		}
	}
}

func TestDefaultCatalogPrefersLocalSources(t *testing.T) {
	remote := library.Source{ID: "remote", Plugin: providers.DLNAID}
	collection := library.Source{ID: "collection", Plugin: library.LocalID, Config: library.EncodeConfig(library.LocalConfig{})}
	folder := library.Source{ID: "folder", Plugin: library.LocalID, Config: library.EncodeConfig(library.LocalConfig{Folder: t.TempDir()})}
	for _, tc := range []struct {
		sources []library.Source
		want    string
	}{{nil, ""}, {[]library.Source{remote}, "remote"}, {[]library.Source{remote, collection}, "collection"}, {[]library.Source{remote, collection, folder}, "folder"}} {
		a := &application{stored: storedState{Libraries: tc.sources}}
		if got := a.defaultCatalogID(); got != tc.want {
			t.Fatalf("default catalog = %q, want %q", got, tc.want)
		}
	}
}
