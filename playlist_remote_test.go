package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/foursmith/player/internal/media"
	"github.com/foursmith/player/internal/providers"
	"github.com/foursmith/player/library"
)

func TestDLNAPlaylistAddsWholeSourceWithoutReading(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Error(w, "adding a source must not read it", http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)
	a := modeTestApplication(t)
	a.expandedFolders = map[string]bool{}
	saved := library.Source{ID: "nas", Name: "My NAS", Plugin: providers.DLNAID, Config: library.EncodeConfig(providers.DLNAConfig{DeviceID: "device", Location: server.URL})}
	video := library.Entry{Name: "Episode 2.mp4", Path: server.URL + "/video", SourceID: saved.ID, ItemID: "video"}
	a.catalog.directoryPath = []catalogFolder{{id: "0"}, {id: "series", name: "Series"}}
	a.catalog.items = []library.Item{{Entry: video}, {Entry: library.Entry{Name: "Season 3", SourceID: saved.ID, ItemID: "season3"}, Folder: true}}
	for range 2 {
		a.addRemotePlaylist(saved, true)
	}
	if requests.Load() != 0 || len(a.playlistRemote) != 1 || len(a.entries) != 2 || a.selected != 0 || !a.playlistExpanded {
		t.Fatal("adding a whole source read the NAS, duplicated content, or interrupted playback", requests.Load(), a.entries)
	}
	nodes := a.playlistModel()
	if len(nodes) != 2 || nodes[0].ID != "current.mp4" || nodes[1].ID != playlistRemoteFolderID(saved.ID, "0") || nodes[1].Name != saved.Name {
		t.Fatal("a nested catalog added only its current directory instead of the device root", nodes)
	}
	root := nodes[1]
	if len(root.Children) != 1 || root.Children[0].Name != "Series" || len(root.Children[0].Children) != 2 {
		t.Fatal("cached directory ancestry or unloaded children were lost", root)
	}
	source := a.playlistRemote[saved.ID]
	if source.folder("0").loaded || !source.folder("series").loaded || source.folder("season3").loaded {
		t.Fatal("unknown directories were treated as fully loaded")
	}
	if a.expandedFolders[root.ID] {
		t.Fatal("appending again expanded an unknown root without loading it")
	}
}

func TestDLNAPlaylistFolderOnlySourceRemainsVisibleAndPlayable(t *testing.T) {
	if err := loadAssets(); err != nil {
		t.Fatal(err)
	}
	a := modeTestApplication(t)
	a.entries, a.selected = nil, -1
	a.expandedFolders = map[string]bool{}
	a.width, a.height = 800, 600
	saved := library.Source{ID: "nas", Name: "My NAS", Plugin: providers.DLNAID, Config: library.EncodeConfig(providers.DLNAConfig{DeviceID: "device", Location: "http://nas/device.xml"})}
	a.stored.Libraries = []library.Source{saved}
	a.catalog.selected, a.catalog.name = saved.ID, saved.Name
	a.catalog.directoryPath = []catalogFolder{{id: "0"}}
	a.catalog.items = []library.Item{{Entry: library.Entry{Name: "Series", SourceID: saved.ID, ItemID: "series"}, Folder: true}}
	a.filterCatalog()
	view := ui.NewTester(a.catalogView, 800, 600)
	for _, key := range []string{"library.playAll", "library.append"} {
		if info, ok := view.Access(a.t(key)); !ok || info.Disabled {
			t.Fatal("a source containing only directories disabled its playlist action", key, info)
		}
	}
	a.addRemotePlaylist(saved, true)
	if len(a.playlistModel()) == 0 || len(a.entries) != 0 {
		t.Fatal("an unloaded source disappeared because it had no cached videos")
	}
	view = ui.NewTester(func(c *ui.Context) { a.playlist(c, media.State{}) }, 800, 600)
	for _, name := range []string{saved.Name, "Series"} {
		if _, ok := view.Find(name); !ok {
			t.Fatal("playlist omitted the DLNA device or its directory", name)
		}
	}
	// Empty folders are still loaded snapshots and must not be fetched again.
	source := a.playlistRemote[saved.ID]
	source.update(source.folder("series"), nil)
	a.togglePlaylistFolder(playlistRemoteFolderID(saved.ID, "series"))
	if !source.folder("series").loaded || source.folder("series").cancel != nil {
		t.Fatal("an empty folder was read again")
	}
}

func TestDLNAPlaylistLoadsExpandedFolderAndDiscardsClearedResults(t *testing.T) {
	for _, cancel := range []bool{false, true} {
		t.Run(map[bool]string{false: "loaded", true: "cleared"}[cancel], func(t *testing.T) {
			device, _ := dlnaCatalogFixture(t)
			items, err := browseDLNAFixture(context.Background(), device, "0")
			if err != nil {
				t.Fatal(err)
			}
			a := modeTestApplication(t)
			a.expandedFolders = map[string]bool{}
			a.catalog.directoryPath = []catalogFolder{{id: "0"}}
			a.catalog.items = items
			a.addRemotePlaylist(library.Source{ID: device.ID, Name: device.Name, Plugin: providers.DLNAID, Config: library.EncodeConfig(providers.DLNAConfig{DeviceID: device.ID, Location: device.Location})}, true)
			source := a.playlistRemote[device.ID]
			queued := make(chan func(), 1)
			a.loadPlaylistRemoteFolder(source, source.folder("season"), func(update func()) { queued <- update })
			select {
			case update := <-queued:
				if cancel {
					a.clearPlaylistRemote()
					a.entries = nil
					a.invalidatePlaylist()
				}
				update()
			case <-time.After(3 * time.Second):
				t.Fatal("expanded directory did not finish loading")
			}
			if cancel {
				if len(a.playlistModel()) > 0 {
					t.Fatal("a queued directory response repopulated a cleared playlist")
				}
				return
			}
			if len(a.entries) != 3 || a.selected != 0 || !source.folder("season").loaded || source.folder("season").cancel != nil {
				t.Fatal("loading a directory changed playback or missed its videos", a.entries)
			}
			root := a.playlistModel()[1]
			if len(root.Children) != 2 || !root.Children[0].Folder || len(root.Children[0].Children) != 1 || a.entries[root.Children[0].Children[0].Index].ItemID != "episode10" {
				t.Fatal("expanded videos did not retain their directory and queue index", root)
			}
			if a.entries[root.Children[1].Index].Subtitle == "" {
				t.Fatal("cached subtitle was dropped")
			}
			if got := library.PlaybackOrder(a.playlistModel(), 2); !reflect.DeepEqual(got, []int{2}) {
				t.Fatal("directory playback order included unrelated videos", got)
			}
		})
	}
}

func TestDLNAPlaylistTreeRejectsCyclesAndDuplicateObjects(t *testing.T) {
	a := modeTestApplication(t)
	a.expandedFolders = map[string]bool{}
	saved := library.Source{ID: "nas", Name: "My NAS", Plugin: providers.DLNAID, Config: library.EncodeConfig(providers.DLNAConfig{DeviceID: "device", Location: "http://nas/device.xml"})}
	video := library.Item{Entry: library.Entry{Name: "Video.mp4", Path: "http://nas/video", SourceID: saved.ID, ItemID: "video"}}
	a.catalog.directoryPath = []catalogFolder{{id: "0"}}
	a.catalog.items = []library.Item{
		{Entry: library.Entry{Name: "Back to root", SourceID: saved.ID, ItemID: "0"}, Folder: true},
		{Entry: library.Entry{Name: "Season 10", SourceID: saved.ID, ItemID: "season10"}, Folder: true},
		{Entry: library.Entry{Name: "Season 2", SourceID: saved.ID, ItemID: "season2"}, Folder: true},
		video, video,
	}
	a.addRemotePlaylist(saved, true)
	root := a.playlistModel()[1]
	if len(root.Children) != 3 || root.Children[0].Name != "Season 2" || root.Children[1].Name != "Season 10" || root.Children[2].Folder || root.Children[2].Index != 1 {
		t.Fatal("a directory cycle or duplicate object produced extra playlist rows", root)
	}
}

func TestDLNAPlaylistOfflineFolderCanBeRetried(t *testing.T) {
	device, revision := dlnaCatalogFixture(t)
	a := modeTestApplication(t)
	a.expandedFolders = map[string]bool{}
	a.catalog.directoryPath = []catalogFolder{{id: "0"}}
	a.catalog.error = "offline"
	a.addRemotePlaylist(library.Source{ID: device.ID, Name: device.Name, Plugin: providers.DLNAID, Config: library.EncodeConfig(providers.DLNAConfig{DeviceID: device.ID, Location: device.Location})}, true)
	source := a.playlistRemote[device.ID]
	folder := source.folder("0")
	if folder.loaded {
		t.Fatal("an offline catalog was cached as an empty directory")
	}
	queued := make(chan func(), 1)
	for _, offline := range []bool{true, false} {
		if offline {
			revision.Store(-1)
		} else {
			revision.Store(0)
		}
		a.loadPlaylistRemoteFolder(source, folder, func(update func()) { queued <- update })
		select {
		case update := <-queued:
			update()
		case <-time.After(3 * time.Second):
			t.Fatal("directory read did not complete")
		}
		if folder.cancel != nil || folder.loaded == offline || a.selected != 0 {
			t.Fatal("failed directory could not be retried or changed playback", folder)
		}
		if offline && (len(a.entries) != 1 || a.notice == "") {
			t.Fatal("failed read altered the queue or hid the error")
		}
	}
	if len(a.entries) != 2 || a.notice != "" {
		t.Fatal("retry missed the video or loaded an unexpanded descendant", a.entries)
	}
}
