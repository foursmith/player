package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/foursmith/player/internal/dlna"
	"github.com/foursmith/player/internal/library"
	"github.com/foursmith/player/internal/media"
)

func TestRecentMediaMixesLocalAndDLNAWithoutTemporaryURLs(t *testing.T) {
	device, revision := dlnaCatalogFixture(t)
	items, err := library.BrowseDLNA(context.Background(), device, "0")
	if err != nil {
		t.Fatal(err)
	}
	a := modeTestApplication(t)
	a.stored.Libraries, _ = rememberDLNALibraries(nil, []dlna.Device{device})
	local := library.Entry{Name: "local.mp4", Path: filepath.Join(t.TempDir(), "local.mp4")}
	remote := items[1].Entry
	a.entries = []library.Entry{local, remote}
	a.invalidatePlaylist()
	if !a.rememberRecentMedia(remote) || !a.rememberRecentMedia(local) {
		t.Fatal("local and DLNA playback did not enter the shared recent list")
	}
	revision.Add(1)
	items, err = library.BrowseDLNA(context.Background(), device, "0")
	if err != nil || items[1].Path == remote.Path {
		t.Fatal("fixture did not renew its stream URL", err)
	}
	if !a.rememberRecentMedia(items[1].Entry) || len(a.stored.RecentMedia) != 2 || a.stored.RecentMedia[0].ObjectID != remote.ObjectID || a.stored.RecentMedia[1].Path != local.Path {
		t.Fatal("replaying a remote object duplicated it or lost mixed playback order", a.stored.RecentMedia)
	}
	a.recordPlaybackHistory(remote, media.State{URL: remote.Path, Position: 120, Metadata: media.Metadata{Duration: 600}})
	path := filepath.Join(t.TempDir(), "state.json")
	if err := saveState(path, a.stored); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("token=")) || bytes.Contains(data, []byte("captions.srt")) {
		t.Fatal("recent playback persisted a temporary stream or subtitle URL")
	}
	reloaded := loadState(path)
	if !reflect.DeepEqual(reloaded.RecentMedia, a.stored.RecentMedia) {
		t.Fatal("mixed recent playback was lost after restart", reloaded.RecentMedia)
	}
	a.preferences.RecentFilesEnabled = false
	if a.rememberRecentMedia(remote) || !reflect.DeepEqual(a.stored.RecentMedia, reloaded.RecentMedia) {
		t.Fatal("disabling recent playback recorded or discarded a DLNA item")
	}
	a.preferences.RecentFilesEnabled = true
	a.preferences.RecordPlaybackHistory = false
	if !a.rememberRecentMedia(remote) {
		t.Fatal("the independent playback cache switch disabled recent DLNA playback")
	}
	a.preferences.RecordPlaybackHistory = true
	a.clearRecentFiles()
	if len(a.stored.RecentMedia) != 0 || a.stored.History[library.Identity(remote)].Position != 120 {
		t.Fatal("clearing recent playback left remote records or deleted resume data")
	}
}

func TestRecentDLNAReopensFreshObjectAndSubtitlesAfterRestart(t *testing.T) {
	device, revision := dlnaCatalogFixture(t)
	for _, parent := range []string{"0", "season"} {
		t.Run(parent, func(t *testing.T) {
			a := modeTestApplication(t)
			a.stored.Libraries, _ = rememberDLNALibraries(nil, []dlna.Device{device})
			items, err := library.BrowseDLNA(context.Background(), device, parent)
			if err != nil {
				t.Fatal(err)
			}
			entry := items[len(items)-1].Entry
			a.entries = []library.Entry{entry}
			a.invalidatePlaylist()
			a.rememberRecentMedia(entry)
			a.recordPlaybackHistory(entry, media.State{URL: entry.Path, Position: 42})
			path := filepath.Join(t.TempDir(), "state.json")
			if err := saveState(path, a.stored); err != nil {
				t.Fatal(err)
			}
			a.stored = loadState(path)
			// A recent record remains usable after its library is removed.
			a.stored.Libraries = nil
			revision.Add(1)
			queued := make(chan func(), 1)
			completed := false
			cancel := readSavedPlayback(a.ctx, a.stored.RecentMedia[0], nil, func(update func()) { queued <- update }, func(restored restoredPlaylist, err error) {
				if err != nil || restored.selected < 0 {
					t.Error("recent playlist could not be read", err)
					return
				}
				reopened := restored.entries[restored.selected]
				completed = true
				if err != nil || reopened.DeviceID != device.ID || reopened.Key() != entry.Key() || reopened.ParentID != parent || a.prepareVideoMode(reopened) != 42 {
					t.Error("recent DLNA video lost its identity, folder or resume position", reopened, err)
				}
				if parent == "0" && (reopened.Path == entry.Path || !strings.HasSuffix(reopened.Subtitle, "/captions.srt")) {
					t.Error("reopened DLNA video did not refresh its URL and sibling subtitles", reopened)
				}
				if !a.rememberRecentMedia(reopened) || len(a.stored.RecentMedia) != 1 {
					t.Error("reopening a video from a removed library lost or duplicated its recent record")
				}
			})
			defer cancel()
			select {
			case update := <-queued:
				update()
			case <-time.After(3 * time.Second):
				t.Fatal("recent DLNA read did not finish")
			}
			if !completed {
				t.Fatal("recent DLNA read did not deliver its result")
			}
		})
	}
}

func TestRecentDLNAErrorsAndCancellationPreserveRecords(t *testing.T) {
	for _, scenario := range []string{"missing", "offline", "replaced", "cleared"} {
		t.Run(scenario, func(t *testing.T) {
			device, revision := dlnaCatalogFixture(t)
			a := modeTestApplication(t)
			item := savedPlayback{Name: "Episode 2.mp4", DeviceID: device.ID, ObjectID: "movie"}
			if scenario == "missing" {
				item.ObjectID = "deleted"
			} else if scenario == "offline" {
				revision.Store(-1)
			}
			item.Playlist = &playlistSnapshot{Entries: []playlistEntryReference{{Name: item.Name, DeviceID: item.DeviceID, ObjectID: item.ObjectID, ParentID: "0"}}, Sources: []playlistSourceSnapshot{{Saved: library.Saved{ID: device.ID, Name: device.Name, DeviceID: device.ID, Location: device.Location}}}}
			a.stored.RecentMedia = []savedPlayback{item}
			queued := make(chan func(), 1)
			completed := false
			a.playbackRestoreCancel = readSavedPlayback(a.ctx, item, nil, func(update func()) { queued <- update }, func(restored restoredPlaylist, err error) {
				completed = true
				if err == nil || len(restored.entries) != 0 || scenario == "missing" && !errors.Is(err, os.ErrNotExist) {
					t.Error("unavailable recent video returned a playable entry or lost its error", restored, err)
				}
			})
			defer a.cancelPlaybackRestore()
			select {
			case update := <-queued:
				if scenario == "replaced" {
					a.cancelImports()
				} else if scenario == "cleared" {
					a.clearRecentFiles()
				}
				update()
			case <-time.After(3 * time.Second):
				t.Fatal("recent DLNA read did not finish")
			}
			canceled := scenario == "replaced" || scenario == "cleared"
			if completed == canceled || len(a.entries) != 1 || (len(a.stored.RecentMedia) == 0) != (scenario == "cleared") {
				t.Fatal("unavailable or canceled playback changed the queue or recent records")
			}
		})
	}
}

func TestRecentDLNAAppearsOnHomeAcrossLocales(t *testing.T) {
	if err := loadAssets(); err != nil {
		t.Fatal(err)
	}
	for _, locale := range []string{"en", "zh-CN", "ja"} {
		a := modeTestApplication(t)
		a.preferences.Locale = locale
		a.width, a.height = 800, 700
		a.stored.RecentMedia = []savedPlayback{{Name: "NAS Episode.mp4", DeviceID: "nas", ObjectID: "movie"}}
		view := ui.NewTester(a.home, 800, 700)
		if _, ok := view.Find("NAS Episode.mp4"); !ok {
			t.Fatalf("home omitted recent DLNA video in %s", locale)
		}
		a.preferences.RecentFilesEnabled = false
		view = ui.NewTester(a.home, 800, 700)
		if _, ok := view.Find("NAS Episode.mp4"); ok {
			t.Fatalf("disabled recent playback remained visible in %s", locale)
		}
	}
}
