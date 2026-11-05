package library

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
)

func TestCollectionFolderReferencesRefreshDeduplicateAndRecover(t *testing.T) {
	dir := t.TempDir()
	folder, nested := filepath.Join(dir, "Series"), filepath.Join(dir, "Series", "Season 2")
	empty, offline := filepath.Join(dir, "Empty"), filepath.Join(dir, "Offline")
	for _, path := range []string{nested, empty} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	write := func(path string) {
		t.Helper()
		if err := os.WriteFile(path, []byte("sample"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	direct, first := filepath.Join(dir, "direct.mp4"), filepath.Join(folder, "first.mp4")
	clip, subtitle := filepath.Join(nested, "clip.mp4"), filepath.Join(nested, "clip.srt")
	for _, path := range []string{direct, first, clip, subtitle} {
		write(path)
	}
	saved := Saved{Paths: []string{direct, clip}, Folders: []string{folder, nested, empty, offline}}
	original := CloneSaved([]Saved{saved})[0]
	read := func(wantVideos int) []SavedItem {
		t.Helper()
		items, err := ReadSaved(context.Background(), saved)
		if err != nil || len(items) != wantVideos+len(saved.Folders) {
			t.Fatalf("folder expansion lost members or duplicated overlapping videos: %+v %v", items, err)
		}
		if !reflect.DeepEqual(saved, original) {
			t.Fatal("refresh rewrote collection locations")
		}
		return items
	}
	items := read(3)
	i := slices.IndexFunc(items, func(item SavedItem) bool { return item.Path == clip })
	if i < 0 || items[i].Subtitle != subtitle || items[i].Root != dir || items[i].Group != nested {
		t.Fatal("overlapping explicit video lost the folder's subtitle match", items)
	}
	i = slices.IndexFunc(items, func(item SavedItem) bool { return item.Path == offline })
	if i < 0 || !items[i].Folder || !errors.Is(items[i].Error, os.ErrNotExist) {
		t.Fatal("offline folder was dropped or lost its error", items)
	}
	tree := SavedTree(saved, items)
	i = slices.IndexFunc(tree, func(node Node) bool { return node.ID == folder })
	if i < 0 || tree[i].Index < 0 || len(tree[i].Children) != 2 {
		t.Fatal("explicit folder identity or nested members were flattened", tree)
	}
	if err := os.Remove(first); err != nil {
		t.Fatal(err)
	}
	added := filepath.Join(folder, "new.mp4")
	write(added)
	items = read(3)
	if slices.ContainsFunc(items, func(item SavedItem) bool { return item.Path == first }) || !slices.ContainsFunc(items, func(item SavedItem) bool { return item.Path == added }) {
		t.Fatal("refresh missed folder additions or retained deleted scanned videos", items)
	}
	if err := os.RemoveAll(folder); err != nil {
		t.Fatal(err)
	}
	// The explicitly saved clip remains as an unavailable video; folder references
	// remain separately recoverable without hiding the other available file.
	items = read(2)
	i = slices.IndexFunc(items, func(item SavedItem) bool { return item.Path == folder })
	if i < 0 || !errors.Is(items[i].Error, os.ErrNotExist) {
		t.Fatal("disconnected folder was treated as empty", items)
	}
	if err := os.MkdirAll(nested, 0700); err != nil {
		t.Fatal(err)
	}
	write(clip)
	items = read(2)
	i = slices.IndexFunc(items, func(item SavedItem) bool { return item.Path == folder })
	if i < 0 || items[i].Error != nil {
		t.Fatal("reconnected folder stayed unavailable", items)
	}
}

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
	tree := SavedTree(saved, items)
	if err != nil || len(items) != 2 || len(tree) != 2 || items[tree[0].Index].Path != two || items[tree[1].Index].Path != ten || items[tree[0].Index].Entry.Subtitle != subtitle {
		t.Fatalf("folder scan lost natural ordering or subtitles: %+v %v", items, err)
	}
	if err := os.Remove(two); err != nil {
		t.Fatal(err)
	}
	one := write("Episode 1.mkv")
	items, err = ReadSaved(context.Background(), saved)
	tree = SavedTree(saved, items)
	if err != nil || len(items) != 2 || len(tree) != 2 || items[tree[0].Index].Path != one || items[tree[1].Index].Path != ten {
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
		{ID: "folder", Name: " Offline ", Folder: folder, Paths: []string{video}, Folders: []string{dir}},
		{ID: "duplicate-folder", Name: "Duplicate", Folder: folder + string(filepath.Separator)},
		{ID: "folder", Name: "Duplicate ID", Folder: dir},
		{ID: "relative", Name: "Invalid", Folder: "relative/path"},
		{ID: "blank", Name: " "},
		{ID: "collection", Name: " Collection ", Paths: []string{video, video, "relative.mp4", "https://nas/clip.mp4", filepath.Join(dir, "._clip.mp4"), filepath.Join(dir, "captions.srt")}, Folders: []string{folder, folder + string(filepath.Separator), "relative", "https://nas/folder", filepath.Join(dir, "._folder")}},
		{ID: "empty", Name: "Empty"},
	})
	want := []Saved{
		{ID: "folder", Name: "Offline", Folder: folder},
		{ID: "collection", Name: "Collection", Paths: []string{video}, Folders: []string{folder}},
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
	for _, saved := range []Saved{{Folder: t.TempDir()}, {Paths: []string{filepath.Join(t.TempDir(), "clip.mp4")}}, {Folders: []string{t.TempDir()}}} {
		if items, err := ReadSaved(ctx, saved); !errors.Is(err, context.Canceled) || len(items) != 0 {
			t.Fatalf("canceled scan published partial results: %+v %v", items, err)
		}
	}
}

func TestDLNASourceNormalizationRetainsOfflineIdentityWithoutStreamURLs(t *testing.T) {
	got := NormalizeSaved([]Saved{
		{ID: "nas", Name: " NAS ", DeviceID: "device-a", Location: "http://nas.local/device.xml", Folder: "/old/path", Paths: []string{"http://nas.local/stream?token=temporary"}, Folders: []string{t.TempDir()}, ShareDLNA: true},
		{ID: "duplicate", Name: "Duplicate NAS", DeviceID: "device-a", Location: "http://new-address/device.xml"},
		{ID: "invalid", Name: "Invalid", DeviceID: "device-b", Location: "file:///device.xml"},
		{ID: "credentials", Name: "Credentials", DeviceID: "device-c", Location: "http://user:secret@nas/device.xml"},
	})
	want := []Saved{{ID: "nas", Name: "NAS", DeviceID: "device-a", Location: "http://nas.local/device.xml"}}
	if !reflect.DeepEqual(got, want) || got[0].Collection() {
		t.Fatal("DLNA source was dropped, duplicated or retained obsolete stream data", got)
	}
}
