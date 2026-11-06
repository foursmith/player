package main

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/foursmith/player/internal/dlnaserver"
	"github.com/foursmith/player/internal/providers"
	"github.com/foursmith/player/library"
)

func TestDLNASharingPerLibraryPersistenceAndPreferenceReset(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	state := loadState(path)
	state.DLNAUUID = dlnaserver.NewUUID()
	state.Libraries = []library.Source{
		{ID: "folder", Name: "Folder", Plugin: library.LocalID, Config: library.EncodeConfig(library.LocalConfig{Folder: filepath.Join(t.TempDir(), "offline")})},
		{ID: "collection", Name: "Collection", ShareDLNA: true, Plugin: library.LocalID, Config: library.EncodeConfig(library.LocalConfig{})},
	}
	if err := saveState(path, state); err != nil {
		t.Fatal(err)
	}
	loaded := loadState(path)
	if !reflect.DeepEqual(loaded.Libraries, state.Libraries) || loaded.DLNAUUID != state.DLNAUUID || !dlnaserver.ValidUUID(loaded.DLNAUUID) {
		t.Fatal("individual sharing choices or stable device identity were not retained")
	}
	a := modeTestApplication(t)
	a.preferences, a.stored = loaded.Preferences, loaded
	a.resetPreferences()
	if !reflect.DeepEqual(a.stored.Libraries, loaded.Libraries) || a.stored.DLNAUUID != loaded.DLNAUUID {
		t.Fatal("resetting preferences changed the libraries' sharing choices or device identity")
	}
}

func TestEditingLibrariesCancelsPendingSharingStartup(t *testing.T) {
	a := modeTestApplication(t)
	ctx, cancel := context.WithCancel(a.ctx)
	a.dlnaSharing = dlnaSharingState{starting: true, cancel: cancel, generation: 1, sources: []library.Source{{ID: "removed", Name: "Old library", Plugin: library.LocalID, Config: library.EncodeConfig(library.LocalConfig{})}}}
	a.syncDLNASharing()
	if ctx.Err() == nil || a.dlnaSharing.starting || a.dlnaSharing.generation != 2 {
		t.Fatal("an old startup scan can still publish removed libraries")
	}
}

func TestUnsharedLibraryEditsDoNotCancelSharingStartup(t *testing.T) {
	a := modeTestApplication(t)
	shared := library.Source{ID: "shared", Name: "Shared", ShareDLNA: true, Plugin: library.LocalID, Config: library.EncodeConfig(library.LocalConfig{})}
	a.stored.Libraries = []library.Source{shared, {ID: "private", Name: "Private renamed", Plugin: library.LocalID, Config: library.EncodeConfig(library.LocalConfig{})}}
	ctx, cancel := context.WithCancel(a.ctx)
	defer cancel()
	a.dlnaSharing = dlnaSharingState{starting: true, cancel: cancel, generation: 1, sources: []library.Source{shared}}
	a.syncDLNASharing()
	if ctx.Err() != nil || !a.dlnaSharing.starting || a.dlnaSharing.generation != 1 {
		t.Fatal("editing an unshared library interrupted another library's startup")
	}
	a.setCatalogDLNASharing("shared", false)
	if ctx.Err() == nil || a.dlnaSharing.starting || a.stored.Libraries[0].ShareDLNA {
		t.Fatal("disabling the last shared library did not cancel startup")
	}
}

func TestDLNASharingControlAtMinimumLibraryWindowSize(t *testing.T) {
	if err := loadAssets(); err != nil {
		t.Fatal(err)
	}
	for _, locale := range []string{"en", "zh-CN", "ja"} {
		t.Run(locale, func(t *testing.T) {
			a := modeTestApplication(t)
			a.preferences.Locale = locale
			a.stored.Libraries = []library.Source{{ID: "a", Name: "Library A", Plugin: library.LocalID, Config: library.EncodeConfig(library.LocalConfig{})}, {ID: "b", Name: "Library B", Plugin: library.LocalID, Config: library.EncodeConfig(library.LocalConfig{})}, {ID: "remote", Name: "NAS", Plugin: providers.DLNAID, Config: library.EncodeConfig(providers.DLNAConfig{DeviceID: "nas", Location: "http://nas/device.xml"})}}
			a.catalog.selected, a.catalog.name = "a", "Library A"
			view := ui.NewTester(a.catalogView, 800, 460)
			view.Frame()
			label := a.t("sharing.title")
			if rect, ok := view.Find(label); !ok || rect.X < 0 || rect.Y < 0 || rect.X+rect.W > 800 || rect.Y+rect.H > 460 {
				t.Fatalf("sharing control is outside the minimum library window: %+v", rect)
			}
			sharing, _ := view.Find(label)
			play, ok := view.Find(a.t("library.playAll"))
			if !ok || sharing.Y+sharing.H/2 != play.Y+play.H/2 || sharing.X+sharing.W > play.X {
				t.Fatalf("sharing control is not beside the top playback actions: sharing=%+v play=%+v", sharing, play)
			}
			// No native window is attached: clicking tests UI and persistence,
			// while network startup remains confined to the real app lifecycle.
			if err := view.Click(label); err != nil {
				t.Fatal(err)
			}
			if !a.stored.Libraries[0].ShareDLNA || a.stored.Libraries[1].ShareDLNA {
				t.Fatal("enabling one library changed another library's sharing choice")
			}
			a.catalog.selected, a.catalog.name = "b", "Library B"
			view.Frame()
			if err := view.Click(label); err != nil {
				t.Fatal(err)
			}
			if !a.stored.Libraries[0].ShareDLNA || !a.stored.Libraries[1].ShareDLNA {
				t.Fatal("individual libraries cannot be shared together")
			}
			a.catalog.selected, a.catalog.name = "a", "Library A"
			view.Frame()
			if err := view.Click(label); err != nil {
				t.Fatal(err)
			}
			if a.stored.Libraries[0].ShareDLNA || !a.stored.Libraries[1].ShareDLNA {
				t.Fatal("disabling one library disabled another library")
			}
			a.catalog.selected, a.catalog.name = "remote", "NAS"
			view.Frame()
			if _, ok := view.Find(label); ok {
				t.Fatal("remote DLNA sources must not show a sharing control")
			}
			a.setCatalogDLNASharing("remote", true)
			if a.stored.Libraries[2].ShareDLNA {
				t.Fatal("remote sources cannot be shared")
			}
		})
	}
}
