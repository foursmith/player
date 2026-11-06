package main

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/foursmith/player/library"
)

func TestCollectionFolderActionsLayoutAndOfflineControls(t *testing.T) {
	if err := loadAssets(); err != nil {
		t.Fatal(err)
	}
	for _, locale := range []string{"zh-CN", "en", "ja"} {
		t.Run(locale, func(t *testing.T) {
			a := modeTestApplication(t)
			a.preferences.Locale = locale
			root := t.TempDir()
			series, empty, offline := filepath.Join(root, "Series"), filepath.Join(root, "Empty"), filepath.Join(root, "Offline")
			direct, clip := filepath.Join(root, "direct.mp4"), filepath.Join(series, "clip.mp4")
			a.stored.Libraries = []library.Source{{ID: "collection", Name: "Weekend videos", Plugin: library.LocalID, Config: library.EncodeConfig(library.LocalConfig{Paths: []string{direct}})}}
			a.appendCatalogFolders("collection", []string{series, empty, offline, series})
			if !reflect.DeepEqual(a.stored.Libraries[0].Local().Folders, []string{series, empty, offline}) || len(a.stored.Libraries[0].Local().Paths) != 1 {
				t.Fatal("adding folders lost explicit files or retained duplicates", a.stored.Libraries)
			}
			saved := a.stored.Libraries[0]
			items := []library.Item{
				{Entry: library.Entry{Path: direct}},
				{Entry: library.Entry{Path: series}, Folder: true},
				{Entry: library.Entry{Path: clip}},
				{Entry: library.Entry{Path: empty}, Folder: true},
				{Entry: library.Entry{Path: offline}, Folder: true, Error: os.ErrNotExist},
			}
			a.catalog.selected, a.catalog.name = saved.ID, saved.Name
			a.setCatalogItems(saved, items)
			a.filterCatalog()
			if len(a.catalog.playable) != 2 || a.catalog.total != 2 {
				t.Fatal("folder references were counted as videos")
			}
			view := ui.NewTester(a.catalogView, 800, 460)
			for _, key := range []string{"media.addFiles", "media.addFolder", "library.addPlaylist"} {
				if r, ok := view.Find(a.t(key)); !ok || r.W == 0 || r.X < 0 || r.Y < 0 || r.X+r.W > 800 || r.Y+r.H > 460 {
					t.Fatalf("collection action is missing or outside the minimum window: %s %+v", key, r)
				}
			}
			for _, label := range []string{a.t("library.relocateItem", "Offline"), a.t("library.removeItem", "Offline"), a.t("library.removeItem", "Series")} {
				if _, ok := view.Find(label); !ok {
					t.Fatal("folder reference lost its management control", label)
				}
			}
			writeUISnapshot(t, locale+"-collection-folders.png", view)
			if err := view.Click(a.t("library.openFolder", "Series")); err != nil {
				t.Fatal(err)
			}
			if _, ok := view.Find(a.t("library.removeItem", "clip.mp4")); ok || len(a.catalog.playable) != 1 || a.catalog.playable[0].Path != clip {
				t.Fatal("folder-derived video acquired ineffective collection editing or escaped its scope")
			}
			a.catalogRoot()
			a.preferences.LibraryFilter = catalogUnavailable
			a.filterCatalog()
			if len(a.catalog.visible) != 1 || a.catalog.items[a.catalog.visible[0]].Path != offline || len(a.catalog.playable) != 0 {
				t.Fatal("unavailable filter hid offline folders or retained healthy folders", a.catalog.items)
			}
			a.catalog.selected = ""
			a.removeCatalogPath(saved.ID, offline)
			if slices.Contains(a.stored.Libraries[0].Local().Folders, offline) || len(a.stored.Libraries[0].Local().Folders) != 2 || len(a.stored.Libraries[0].Local().Paths) != 1 {
				t.Fatal("removing one folder changed other collection members", a.stored.Libraries)
			}
		})
	}
}

func TestLocalCatalogNavigationSearchPlaybackAndRefresh(t *testing.T) {
	a := modeTestApplication(t)
	root := t.TempDir()
	for _, name := range []string{"root.mp4", "Series/Season 2/Episode 2.mp4", "Series/Season 2/Episode 2.srt", "Series/Season 10/Episode 10.mp4"} {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("sample"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	saved := library.Source{ID: "folder", Name: "Videos", Plugin: library.LocalID, Config: library.EncodeConfig(library.LocalConfig{Folder: root})}
	a.stored.Libraries = []library.Source{saved}
	a.catalog.selected, a.catalog.name = saved.ID, saved.Name
	reload := func() {
		t.Helper()
		items, err := library.ReadLocal(context.Background(), saved)
		if err != nil {
			t.Fatal(err)
		}
		a.setCatalogItems(saved, items)
		a.filterCatalog()
	}
	reload()
	if len(a.catalog.visible) != 2 || !a.catalog.items[a.catalog.visible[0]].Folder || len(a.catalog.playable) != 3 || a.catalog.total != 3 {
		t.Fatal("root browsing flattened descendants or bulk playback skipped them", a.catalog.items, a.catalog.playable)
	}
	a.enterCatalogFolder(a.catalog.items[a.catalog.visible[0]])
	if a.catalogDirectoryName() != "Videos / Series" || len(a.catalog.visible) != 2 || len(a.catalog.playable) != 2 {
		t.Fatal("folder navigation lost its scope", a.catalogDirectoryName(), a.catalog.items)
	}
	a.catalog.search = "EPISODE 10"
	a.filterCatalog()
	if len(a.catalog.visible) != 1 || len(a.catalog.playable) != 1 || a.catalog.playable[0].Name != "Episode 10.mp4" {
		t.Fatal("search missed a descendant or playback escaped the search", a.catalog.items, a.catalog.playable)
	}
	queue, selected, play := importedPlaylist(a.entries, a.catalog.playable, a.selected, true)
	if len(queue) != 2 || selected != a.selected || play || queue[1].Name != "Episode 10.mp4" {
		t.Fatal("appending a search result changed playback or added other folders", queue, selected, play)
	}
	a.catalog.search = ""
	a.preferences.LibraryFilter = catalogWatched
	for _, item := range a.catalog.sourceItems {
		if item.Name() == "Episode 2.mp4" {
			a.stored.History[library.Identity(item.Entry)] = videoHistory{Watched: true}
		}
	}
	a.filterCatalog()
	if len(a.catalog.visible) != 1 || len(a.catalog.playable) != 1 || a.catalog.playable[0].Name != "Episode 2.mp4" || a.catalog.playable[0].Subtitle == "" {
		t.Fatal("status filter missed descendants or lost subtitles", a.catalog.items, a.catalog.playable)
	}
	a.preferences.LibraryFilter = catalogAll
	a.filterCatalog()
	a.enterCatalogFolder(a.catalog.items[a.catalog.visible[0]])
	reload()
	if a.catalogDirectoryName() != "Videos / Series / Season 2" || len(a.catalog.playable) != 1 {
		t.Fatal("refresh reset the selected directory")
	}
	if err := os.RemoveAll(filepath.Join(root, "Series", "Season 2")); err != nil {
		t.Fatal(err)
	}
	reload()
	if a.catalogDirectoryName() != "Videos / Series" || len(a.catalog.playable) != 1 || a.catalog.playable[0].Name != "Episode 10.mp4" {
		t.Fatal("removed directory did not fall back to its surviving parent", a.catalogDirectoryName())
	}
	a.catalogRoot()
	if len(a.catalog.directoryPath) != 1 || len(a.catalog.playable) != 2 {
		t.Fatal("root navigation retained a child scope")
	}
}

func TestLocalCatalogTreeControlsAcrossSourcesAndLocales(t *testing.T) {
	if err := loadAssets(); err != nil {
		t.Fatal(err)
	}
	for _, locale := range []string{"zh-CN", "en", "ja"} {
		for _, collection := range []bool{false, true} {
			kind := "folder"
			if collection {
				kind = "collection"
			}
			t.Run(locale+"/"+kind, func(t *testing.T) {
				a := modeTestApplication(t)
				a.preferences.Locale = locale
				root := filepath.Join(string(filepath.Separator), "Media")
				items := []library.Item{
					{Entry: library.Entry{Path: filepath.Join(root, "root.mp4")}},
					{Entry: library.Entry{Path: filepath.Join(root, "Season 2", "Episode 2.mp4")}},
					{Entry: library.Entry{Path: filepath.Join(root, "Season 2", "missing.mp4")}, Error: os.ErrNotExist},
				}
				saved := library.Source{ID: kind, Name: "Weekend videos", Plugin: library.LocalID, Config: library.EncodeConfig(library.LocalConfig{Folder: root})}
				if collection {
					saved.SetLocal(library.LocalConfig{Paths: []string{items[0].Path, items[1].Path, items[2].Path}})
				}
				a.stored.Libraries = []library.Source{saved}
				a.catalog.selected, a.catalog.name = saved.ID, saved.Name
				a.setCatalogItems(saved, items)
				a.filterCatalog()
				view := ui.NewTester(a.catalogView, 800, 460)
				for _, key := range []string{"library.parent", "library.root", "library.playAll", "library.append", "library.filter", "library.sort"} {
					if r, ok := view.Find(a.t(key)); !ok || r.X < 0 || r.Y < 0 || r.X+r.W > 800 || r.Y+r.H > 460 {
						t.Fatalf("tree control outside minimum window: %s %+v", key, r)
					}
				}
				if err := view.Click(a.t("library.openFolder", "Season 2")); err != nil {
					t.Fatal(err)
				}
				if len(a.catalog.playable) != 1 || len(a.catalog.visible) != 2 || a.catalog.playable[0].Path != items[1].Path {
					t.Fatal("folder click lost available or missing members", a.catalog.items)
				}
				if collection {
					if _, ok := view.Find(a.t("library.removeItem", "missing.mp4")); !ok {
						t.Fatal("nested collection members lost their editing controls")
					}
				}
				if err := view.Click(a.t("library.parent")); err != nil {
					t.Fatal(err)
				}
				if len(a.catalog.directoryPath) != 1 || len(a.catalog.playable) != 2 || !reflect.DeepEqual(a.catalog.visible, []int{0, 1}) {
					t.Fatal("parent navigation did not restore root rows", a.catalog.visible)
				}
				writeUISnapshot(t, locale+"-tree-"+kind+".png", view)
			})
		}
	}
}
