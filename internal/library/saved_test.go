package library

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestSavedFolderRefreshUsesCurrentFilesAndPreservesSubtitleMatching(t *testing.T) {
	dir := t.TempDir()
	write := func(name string) string {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("sample"), 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	ten, two := write("Episode 10.mp4"), write("Episode 2.mp4")
	subtitle := write("Episode 2.srt")
	saved := Saved{ID: "folder", Name: "Folder", Folder: dir}
	items, err := ReadSaved(context.Background(), saved)
	if err != nil || len(items) != 2 || items[0].Path != two || items[1].Path != ten || items[0].Entry.Subtitle != subtitle {
		t.Fatalf("folder scan lost natural ordering or subtitles: %+v %v", items, err)
	}
	if err := os.Remove(two); err != nil {
		t.Fatal(err)
	}
	one := write("Episode 1.mkv")
	items, err = ReadSaved(context.Background(), saved)
	if err != nil || len(items) != 2 || items[0].Path != one || items[1].Path != ten {
		t.Fatalf("refresh retained removed files or missed additions: %+v %v", items, err)
	}
}

func TestCollectionKeepsMissingMembersAndReadsFreshMetadata(t *testing.T) {
	dir := t.TempDir()
	available := filepath.Join(dir, "available.mp4")
	missing := filepath.Join(dir, "missing.mp4")
	if err := os.WriteFile(available, []byte("updated video"), 0600); err != nil {
		t.Fatal(err)
	}
	saved := Saved{ID: "collection", Name: "Collection", Paths: []string{missing, available}}
	items, err := ReadSaved(context.Background(), saved)
	if err != nil || len(items) != 2 || !errors.Is(items[0].Error, os.ErrNotExist) || items[0].Path != missing || items[1].Error != nil || items[1].Entry.Size != 13 {
		t.Fatalf("one missing file prevented the collection from loading: %+v %v", items, err)
	}
	if len(saved.Paths) != 2 || saved.Paths[0] != missing {
		t.Fatal("a read silently removed a missing collection member")
	}
	if err := os.WriteFile(missing, []byte("back"), 0600); err != nil {
		t.Fatal(err)
	}
	items, err = ReadSaved(context.Background(), saved)
	if err != nil || items[0].Error != nil || items[0].Entry.Path != missing {
		t.Fatal("reconnected media did not become available", err)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadSaved(context.Background(), Saved{Folder: dir}); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("disconnected folder was treated as an empty library", err)
	}
}

func TestSavedLibrariesNormalizeLocationsWithoutRequiringConnectedDisks(t *testing.T) {
	dir := t.TempDir()
	video := filepath.Join(dir, "clip.mp4")
	folder := filepath.Join(dir, "offline")
	got := NormalizeSaved([]Saved{
		{ID: "folder", Name: " Offline ", Folder: folder, Paths: []string{video}},
		{ID: "duplicate-folder", Name: "Duplicate", Folder: folder + string(filepath.Separator)},
		{ID: "folder", Name: "Duplicate ID", Folder: dir},
		{ID: "relative", Name: "Invalid", Folder: "relative/path"},
		{ID: "blank", Name: " "},
		{ID: "collection", Name: " Collection ", Paths: []string{video, video, "relative.mp4", "https://nas/clip.mp4", filepath.Join(dir, "._clip.mp4"), filepath.Join(dir, "captions.srt")}},
		{ID: "empty", Name: "Empty"},
	})
	want := []Saved{
		{ID: "folder", Name: "Offline", Folder: folder},
		{ID: "collection", Name: "Collection", Paths: []string{video}},
		{ID: "empty", Name: "Empty"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("invalid records survived or offline locations were removed: %+v", got)
	}
	paths := LocalPaths([]Entry{{Path: video}, {Path: video}, {Path: "https://nas/clip.mp4", DeviceID: "nas"}})
	if !reflect.DeepEqual(paths, []string{video}) {
		t.Fatal("saving a local playlist duplicated files or retained a DLNA URL", paths)
	}
}

func TestCanceledLibraryScanStopsWithoutReturningPartialResults(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, saved := range []Saved{{Folder: t.TempDir()}, {Paths: []string{filepath.Join(t.TempDir(), "clip.mp4")}}} {
		if items, err := ReadSaved(ctx, saved); !errors.Is(err, context.Canceled) || len(items) != 0 {
			t.Fatalf("canceled scan published partial results: %+v %v", items, err)
		}
	}
}

func TestDLNASourceNormalizationRetainsOfflineIdentityWithoutStreamURLs(t *testing.T) {
	got := NormalizeSaved([]Saved{
		{ID: "nas", Name: " NAS ", DeviceID: "device-a", Location: "http://nas.local/device.xml", Folder: "/old/path", Paths: []string{"http://nas.local/stream?token=temporary"}, ShareDLNA: true},
		{ID: "duplicate", Name: "Duplicate NAS", DeviceID: "device-a", Location: "http://new-address/device.xml"},
		{ID: "invalid", Name: "Invalid", DeviceID: "device-b", Location: "file:///device.xml"},
		{ID: "credentials", Name: "Credentials", DeviceID: "device-c", Location: "http://user:secret@nas/device.xml"},
	})
	want := []Saved{{ID: "nas", Name: "NAS", DeviceID: "device-a", Location: "http://nas.local/device.xml"}}
	if !reflect.DeepEqual(got, want) || got[0].Collection() {
		t.Fatal("DLNA source was dropped, duplicated or retained obsolete stream data", got)
	}
}
