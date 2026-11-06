package providers_test

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/foursmith/player/internal/providers"
	. "github.com/foursmith/player/library"
)

func TestSavedLibrariesNormalizeLocationsWithoutRequiringConnectedDisks(t *testing.T) {
	dir := t.TempDir()
	video := filepath.Join(dir, "clip.mp4")
	folder := filepath.Join(dir, "offline")
	got := providers.NewRegistry().NormalizeSources([]Source{
		{ID: "folder", Name: " Offline ", Plugin: LocalID, Config: EncodeConfig(LocalConfig{Folder: folder, Paths: []string{video}, Folders: []string{dir}})},
		{ID: "duplicate-folder", Name: "Duplicate", Plugin: LocalID, Config: EncodeConfig(LocalConfig{Folder: folder + string(filepath.Separator)})},
		{ID: "folder", Name: "Duplicate ID", Plugin: LocalID, Config: EncodeConfig(LocalConfig{Folder: dir})},
		{ID: "relative", Name: "Invalid", Plugin: LocalID, Config: EncodeConfig(LocalConfig{Folder: "relative/path"})},
		{ID: "blank", Name: " ", Plugin: LocalID, Config: EncodeConfig(LocalConfig{})},
		{ID: "collection", Name: " Collection ", Plugin: LocalID, Config: EncodeConfig(LocalConfig{Paths: []string{video, video, "relative.mp4", "https://nas/clip.mp4", filepath.Join(dir, "._clip.mp4"), filepath.Join(dir, "captions.srt")}, Folders: []string{folder, folder + string(filepath.Separator), "relative", "https://nas/folder", filepath.Join(dir, "._folder")}})},
		{ID: "empty", Name: "Empty", Plugin: LocalID, Config: EncodeConfig(LocalConfig{})},
	})
	want := []Source{
		{ID: "folder", Name: "Offline", Plugin: LocalID, Config: EncodeConfig(LocalConfig{Folder: folder})},
		{ID: "collection", Name: "Collection", Plugin: LocalID, Config: EncodeConfig(LocalConfig{Paths: []string{video}, Folders: []string{folder}})},
		{ID: "empty", Name: "Empty", Plugin: LocalID, Config: EncodeConfig(LocalConfig{})},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("invalid records survived or offline locations were removed: %+v", got)
	}
	paths := LocalPaths([]Entry{{Path: video}, {Path: video}, {Path: "https://nas/clip.mp4", SourceID: "nas"}})
	if !reflect.DeepEqual(paths, []string{video}) {
		t.Fatal("saving a local playlist duplicated files or retained a DLNA URL", paths)
	}
}

func TestDLNASourceNormalizationRetainsOfflineIdentityWithoutStreamURLs(t *testing.T) {
	got := providers.NewRegistry().NormalizeSources([]Source{
		{ID: "nas", Name: " NAS ", ShareDLNA: true, Plugin: providers.DLNAID, Config: EncodeConfig(providers.DLNAConfig{DeviceID: "device-a", Location: "http://nas.local/device.xml"})},
		{ID: "duplicate", Name: "Duplicate NAS", Plugin: providers.DLNAID, Config: EncodeConfig(providers.DLNAConfig{DeviceID: "device-a", Location: "http://new-address/device.xml"})},
		{ID: "invalid", Name: "Invalid", Plugin: providers.DLNAID, Config: EncodeConfig(providers.DLNAConfig{DeviceID: "device-b", Location: "file:///device.xml"})},
		{ID: "credentials", Name: "Credentials", Plugin: providers.DLNAID, Config: EncodeConfig(providers.DLNAConfig{DeviceID: "device-c", Location: "http://user:secret@nas/device.xml"})},
	})
	want := []Source{{ID: "nas", Name: "NAS", Plugin: providers.DLNAID, Config: EncodeConfig(providers.DLNAConfig{DeviceID: "device-a", Location: "http://nas.local/device.xml"})}}
	if !reflect.DeepEqual(got, want) || got[0].Collection() {
		t.Fatal("DLNA source was dropped, duplicated or retained obsolete stream data", got)
	}
}
