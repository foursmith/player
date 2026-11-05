package main

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/foursmith/player/internal/library"
)

func TestCatalogReadDeliversQueuedResultsUnlessCanceled(t *testing.T) {
	path := filepath.Join(t.TempDir(), "clip.mp4")
	if err := os.WriteFile(path, []byte("video"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, canceled := range []bool{false, true} {
		queued := make(chan func(), 1)
		completed := false
		cancel := readCatalog(context.Background(), library.Saved{Paths: []string{path}}, func(update func()) { queued <- update }, func(items []library.SavedItem, err error) {
			completed = true
			if err != nil || len(items) != 1 || items[0].Error != nil || items[0].Entry.Path != path {
				t.Errorf("scan result was lost: %+v %v", items, err)
			}
		})
		select {
		case update := <-queued:
			if canceled {
				cancel()
			}
			update()
		case <-time.After(5 * time.Second):
			t.Fatal("scan never queued its result")
		}
		cancel()
		if completed == canceled {
			t.Fatalf("queued result delivery: canceled=%v completed=%v", canceled, completed)
		}
	}
}

func TestSavedLibrariesSurviveHistoryClearingAndPreferenceReset(t *testing.T) {
	a := modeTestApplication(t)
	dir := t.TempDir()
	want := []library.Saved{
		{ID: "folder", Name: "Offline disk", Folder: filepath.Join(dir, "offline")},
		{ID: "collection", Name: "Favorites", Paths: []string{filepath.Join(dir, "clip.mp4")}},
		{ID: "empty", Name: "Empty collection"},
	}
	a.stored.Libraries = library.CloneSaved(want)
	a.clearRecentFiles()
	a.resetPreferences()
	path := filepath.Join(dir, "state.json")
	if err := saveState(path, a.stored); err != nil {
		t.Fatal(err)
	}
	if got := loadState(path).Libraries; !reflect.DeepEqual(got, want) {
		t.Fatalf("history or preference actions changed saved libraries: %+v", got)
	}
}

func TestCatalogSearchAndRenameAcrossLocales(t *testing.T) {
	if err := loadAssets(); err != nil {
		t.Fatal(err)
	}
	for _, locale := range []string{"zh-CN", "en", "ja"} {
		t.Run(locale, func(t *testing.T) {
			a := modeTestApplication(t)
			a.preferences.Locale = locale
			dir := t.TempDir()
			a.stored.Libraries = []library.Saved{{ID: "collection", Name: "Favorites", Paths: []string{filepath.Join(dir, "Cloud.mp4"), filepath.Join(dir, "missing.mp4")}}}
			a.catalog.selected, a.catalog.name = "collection", "Favorites"
			a.catalog.items = []library.SavedItem{
				{Entry: library.Entry{Path: a.stored.Libraries[0].Paths[0]}},
				{Entry: library.Entry{Path: a.stored.Libraries[0].Paths[1]}, Error: os.ErrNotExist},
			}
			a.filterCatalog()
			view := ui.NewTester(a.catalogView, 800, 460)
			a.catalog.name = " Renamed "
			view.Frame()
			if err := view.Click(a.t("library.rename")); err != nil {
				t.Fatal(err)
			}
			if a.stored.Libraries[0].Name != "Renamed" || a.catalog.name != "Renamed" {
				t.Fatal("renaming did not persist the trimmed name")
			}
			if err := view.Click(a.t("library.search")); err != nil {
				t.Fatal(err)
			}
			view.Type("CLOUD")
			if !reflect.DeepEqual(a.catalog.visible, []int{0}) {
				t.Fatal("search did not match filenames without case sensitivity", a.catalog.visible)
			}
			view.TypeKey(ui.Cmd, ui.KeyA, "")
			view.Type("missing")
			if !reflect.DeepEqual(a.catalog.visible, []int{1}) || len(a.stored.Libraries[0].Paths) != 2 {
				t.Fatal("search discarded an unavailable file")
			}
			for _, key := range []string{"library.playAll", "library.append", "library.addPlaylist", "library.savePlaylist"} {
				if rect, ok := view.Find(a.t(key)); !ok || rect.X < 0 || rect.Y < 0 || rect.X+rect.W > 800 || rect.Y+rect.H > 460 {
					t.Fatalf("library control is outside the minimum window size: %s %+v", key, rect)
				}
			}
		})
	}
}
