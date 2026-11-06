package main

import (
	"context"
	"slices"

	"github.com/foursmith/player/internal/dlna"
	"github.com/foursmith/player/internal/library"
)

// A DLNA playlist retains the source, rather than scanning all of its videos.
// Each folder owns a session-only snapshot loaded when it is expanded.
type playlistDLNASource struct {
	saved   library.Saved
	folders map[string]*playlistDLNAFolder
	flat    bool // A directly opened video keeps its original flat queue row.
}

type playlistDLNAFolder struct {
	objectID string
	items    []library.SavedItem
	loaded   bool // Ancestor links alone do not make a complete directory snapshot.
	cancel   context.CancelFunc
}

func (source *playlistDLNASource) folder(objectID string) *playlistDLNAFolder {
	id := playlistDLNAFolderID(source.saved.DeviceID, objectID)
	if source.folders[id] == nil {
		source.folders[id] = &playlistDLNAFolder{objectID: objectID}
	}
	return source.folders[id]
}

func playlistDLNAFolderID(deviceID, objectID string) string {
	return "dlna-folder:" + (library.Entry{DeviceID: deviceID, ObjectID: objectID}).Key()
}

func (a *application) clearPlaylistDLNA() {
	for _, source := range a.playlistDLNA {
		for _, folder := range source.folders {
			if folder.cancel != nil {
				folder.cancel()
			}
		}
	}
	a.playlistDLNA = nil
}

// Keep already browsed content, including the path back to the device root.
// Adding a source performs no network requests.
func (source *playlistDLNASource) remember(path []catalogFolder) *playlistDLNAFolder {
	folder := source.folder("0")
	for _, child := range path[min(1, len(path)):] {
		if !slices.ContainsFunc(folder.items, func(item library.SavedItem) bool { return item.Folder && item.ObjectID == child.id }) {
			folder.items = append(folder.items, library.SavedItem{Entry: library.Entry{Name: child.name, DeviceID: source.saved.DeviceID, ObjectID: child.id}, Folder: true})
		}
		folder = source.folder(child.id)
	}
	return folder
}

func (source *playlistDLNASource) update(folder *playlistDLNAFolder, items []library.SavedItem) {
	if folder.cancel != nil {
		folder.cancel()
		folder.cancel = nil
	}
	folder.items, folder.loaded = slices.Clone(items), true
	for _, item := range items {
		if item.Folder {
			source.folder(item.ObjectID)
		}
	}
}

func (a *application) addDLNAPlaylist(saved library.Saved, appendOnly bool) {
	a.cancelImports()
	a.applyImportedEntries(library.PlayableEntries(a.catalog.items), nil, appendOnly, "")
	if a.playlistDLNA == nil {
		a.playlistDLNA = map[string]*playlistDLNASource{}
	}
	source := a.playlistDLNA[saved.DeviceID]
	if source == nil {
		source = &playlistDLNASource{saved: saved, folders: map[string]*playlistDLNAFolder{}}
		a.playlistDLNA[saved.DeviceID] = source
	}
	source.saved = saved
	source.flat = false
	folder := source.remember(a.catalog.directoryPath)
	if a.catalog.error == "" && (!a.catalog.loading || len(a.catalog.items) > 0) {
		source.update(folder, a.catalog.items)
	}
	if source.folder("0").loaded {
		a.expandedFolders[playlistDLNAFolderID(saved.DeviceID, "0")] = true
	}
	a.playlistExpanded = true
	a.invalidatePlaylist()
	a.persist()
}

func (a *application) playlistDLNAFolder(id string) (*playlistDLNASource, *playlistDLNAFolder) {
	for _, source := range a.playlistDLNA {
		if folder := source.folders[id]; folder != nil {
			return source, folder
		}
	}
	return nil, nil
}

func (a *application) togglePlaylistFolder(id string) {
	a.playlistSnapshot = nil
	a.expandedFolders[id] = !a.expandedFolders[id]
	a.playlistRowsValid = false
	source, folder := a.playlistDLNAFolder(id)
	if a.expandedFolders[id] && folder != nil && !folder.loaded && folder.cancel == nil {
		a.loadPlaylistDLNAFolder(source, folder, a.window.Update)
	}
}

func (a *application) loadPlaylistDLNAFolder(source *playlistDLNASource, folder *playlistDLNAFolder, dispatch func(func())) {
	folder.cancel = readDLNACatalog(a.ctx, source.saved, folder.objectID, dispatch, func(device dlna.Device, items []library.SavedItem, err error) {
		if a.shutdown || a.playlistDLNA[source.saved.DeviceID] != source {
			return
		}
		folder.cancel = nil
		defer a.refreshWindows()
		if err != nil {
			a.notice = a.t("library.dlnaUnavailable") + ": " + err.Error()
			return
		}
		a.notice = ""
		source.saved.Location = device.Location
		source.update(folder, items)
		a.entries = mergeEntries(a.entries, library.PlayableEntries(items))
		a.invalidatePlaylist()
		a.persist()
	})
}

func (a *application) buildPlaylistTree() []library.Node {
	if len(a.playlistDLNA) == 0 {
		return library.Tree(a.entries)
	}
	indices := make(map[string]int, len(a.entries))
	for i, entry := range a.entries {
		indices[entry.Key()] = i
	}
	owned := map[string]bool{}
	children := map[string][]library.Node{}
	for _, source := range a.playlistDLNA {
		if source.flat {
			continue
		}
		rootID := playlistDLNAFolderID(source.saved.DeviceID, "0")
		children[""] = append(children[""], library.Node{ID: rootID, Name: source.saved.Name, Index: -1, Folder: true})
		for id, folder := range source.folders {
			for _, item := range folder.items {
				if item.Folder {
					children[id] = append(children[id], library.Node{ID: playlistDLNAFolderID(source.saved.DeviceID, item.ObjectID), Name: item.Name(), Index: -1, Folder: true})
				} else if index, ok := indices[item.Key()]; ok {
					entry := a.entries[index]
					owned[entry.Key()] = true
					children[id] = append(children[id], library.Node{ID: entry.Key(), Name: entry.Name, Index: index})
				}
			}
		}
	}
	nodes := library.Tree(a.entries)
	// DLNA videos are flat in library.Tree, so local directory nodes stay intact.
	nodes = slices.DeleteFunc(nodes, func(node library.Node) bool { return !node.Folder && owned[node.ID] })
	return append(nodes, library.BuildTree(children)...)
}
