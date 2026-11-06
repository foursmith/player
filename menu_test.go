package main

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/egoist/mygo"
	"github.com/foursmith/player/internal/library"
)

func TestDockAndMenuBarShareRecentPlayback(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Dock menus are macOS-only")
	}
	if err := loadAssets(); err != nil {
		t.Fatal(err)
	}
	a := modeTestApplication(t)
	dir := t.TempDir()
	a.stored.Libraries = []library.Saved{{ID: "nas", Name: "NAS", DeviceID: "nas", Location: "http://nas.local/device.xml"}}
	a.entries = []library.Entry{
		{Name: "local.mp4", Path: filepath.Join(dir, "local.mp4")},
		{Name: "missing.mp4", Path: filepath.Join(dir, "missing.mp4")},
		{Name: "NAS video.mp4", DeviceID: "nas", ObjectID: "movie", ParentID: "0"},
	}
	// Keep one local file unavailable: both menus must retain the same
	// records and let reopening report availability, including for DLNA.
	if err := os.WriteFile(a.entries[0].Path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	a.invalidatePlaylist()
	for _, entry := range a.entries {
		if !a.rememberRecentMedia(entry) {
			t.Fatal("playback did not enter recent history", entry)
		}
	}
	want := []string{"NAS video.mp4", "missing.mp4", "local.mp4"}
	for _, locale := range []string{"en", "zh-CN", "ja"} {
		t.Run(locale, func(t *testing.T) {
			a.preferences.Locale = locale
			checkRecentMenus(t, a, want)
		})
	}
	a.rememberRecentMedia(a.entries[0])
	want = []string{"local.mp4", "NAS video.mp4", "missing.mp4"}
	checkRecentMenus(t, a, want)
	a.preferences.RecentFilesEnabled = false
	checkRecentMenus(t, a, nil)
	a.preferences.RecentFilesEnabled = true
	checkRecentMenus(t, a, want)
	a.clearRecentFiles()
	checkRecentMenus(t, a, []string{})
}

func checkRecentMenus(t *testing.T, a *application, want []string) {
	t.Helper()
	menuBar, dockMenu := a.newMenus()
	var recent []*mygo.MenuItem
	for _, menu := range menuBar.Items() {
		if menu.Label == a.t("menu.file") {
			for _, item := range menu.Submenu {
				if item.Label == a.t("media.recentFiles") {
					recent = item.Submenu
				}
			}
		}
	}
	dock := dockMenu.Items()
	if !a.preferences.RecentFilesEnabled {
		if recent != nil || len(dock) != 4 || dock[0].Label != a.t("media.showPlayer") {
			t.Fatal("disabled history remained in a menu", recent, dock)
		}
		return
	}
	if len(want) == 0 {
		if len(recent) != 1 || !recent[0].Disabled || recent[0].Label != a.t("media.noRecent") {
			t.Fatal("empty history lost its localized placeholder", recent)
		}
	} else {
		if len(recent) != len(want)+2 {
			t.Fatal("recent menu has the wrong number of entries", len(recent), want)
		}
		var got []string
		for _, item := range recent[:len(want)] {
			got = append(got, item.Label)
			if item.Click == nil || item.Disabled {
				t.Fatal("recent playback cannot be reopened", item)
			}
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatal("recent playback order differs", got, want)
		}
		if recent[len(want)].Type != mygo.MenuItemSeparator || recent[len(want)+1].Label != a.t("media.clearRecent") || recent[len(want)+1].Click == nil {
			t.Fatal("recent menu lost its clear action", recent)
		}
	}
	if len(dock) != len(recent)+5 || dock[len(recent)].Type != mygo.MenuItemSeparator || dock[len(recent)+1].Label != a.t("media.showPlayer") {
		t.Fatal("Dock history or player actions are missing", dock)
	}
	for i, item := range recent {
		other := dock[i]
		if item == other || item.Label != other.Label || item.Type != other.Type || item.Disabled != other.Disabled || (item.Click == nil) != (other.Click == nil) {
			t.Fatal("Dock and menu bar history differ or reuse a native item", i, item, other)
		}
	}
}
