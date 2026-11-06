package main

import (
	"context"
	"slices"

	"github.com/foursmith/player/library"
)

// A plugin playlist retains the source, rather than scanning all of its videos.
// Each folder owns a session-only snapshot loaded when it is expanded.
type playlistRemoteSource struct {
	saved   library.Source
	folders map[string]*playlistRemoteFolder
	flat    bool // A directly opened video keeps its original flat queue row.
}

type playlistRemoteFolder struct {
	itemID string
	items  []library.Item
	loaded bool // Ancestor links alone do not make a complete directory snapshot.
	cancel context.CancelFunc
}

func (source *playlistRemoteSource) folder(itemID string) *playlistRemoteFolder {
	id := playlistRemoteFolderID(source.saved.ID, itemID)
	if source.folders[id] == nil {
		source.folders[id] = &playlistRemoteFolder{itemID: itemID}
	}
	return source.folders[id]
}

func playlistRemoteFolderID(sourceID, itemID string) string {
	return "source-folder:" + (library.Entry{SourceID: sourceID, ItemID: itemID}).Key()
}

func (a *application) clearPlaylistRemote() {
	for _, source := range a.playlistRemote {
		for _, folder := range source.folders {
			if folder.cancel != nil {
				folder.cancel()
			}
		}
	}
	a.playlistRemote = nil
}

// Keep already browsed content, including the path back to the device root.
// Adding a source performs no network requests.
func (source *playlistRemoteSource) remember(path []catalogFolder) *playlistRemoteFolder {
	folder := source.folder(sourceRoot(source.saved))
	for _, child := range path[min(1, len(path)):] {
		if !slices.ContainsFunc(folder.items, func(item library.Item) bool { return item.Folder && item.ItemID == child.id }) {
			folder.items = append(folder.items, library.Item{Entry: library.Entry{Name: child.name, SourceID: source.saved.ID, ItemID: child.id}, Folder: true})
		}
		folder = source.folder(child.id)
	}
	return folder
}

func (source *playlistRemoteSource) update(folder *playlistRemoteFolder, items []library.Item) {
	if folder.cancel != nil {
		folder.cancel()
		folder.cancel = nil
	}
	folder.items, folder.loaded = slices.Clone(items), true
	for _, item := range items {
		if item.Folder {
			source.folder(item.ItemID)
		}
	}
}

func (a *application) addRemotePlaylist(saved library.Source, appendOnly bool) {
	a.cancelImports()
	a.applyImportedEntries(library.PlayableEntries(a.catalog.items), nil, appendOnly, "")
	if a.playlistRemote == nil {
		a.playlistRemote = map[string]*playlistRemoteSource{}
	}
	source := a.playlistRemote[saved.ID]
	if source == nil {
		source = &playlistRemoteSource{saved: saved, folders: map[string]*playlistRemoteFolder{}}
		a.playlistRemote[saved.ID] = source
	}
	source.saved = saved
	source.flat = false
	folder := source.remember(a.catalog.directoryPath)
	if a.catalog.error == "" && (!a.catalog.loading || len(a.catalog.items) > 0) {
		source.update(folder, a.catalog.items)
	}
	if source.folder(sourceRoot(source.saved)).loaded {
		a.expandedFolders[playlistRemoteFolderID(saved.ID, sourceRoot(saved))] = true
	}
	a.playlistExpanded = true
	a.invalidatePlaylist()
	a.persist()
}

func (a *application) playlistRemoteFolder(id string) (*playlistRemoteSource, *playlistRemoteFolder) {
	for _, source := range a.playlistRemote {
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
	source, folder := a.playlistRemoteFolder(id)
	if a.expandedFolders[id] && folder != nil && !folder.loaded && folder.cancel == nil {
		a.loadPlaylistRemoteFolder(source, folder, a.window.Update)
	}
}

func (a *application) loadPlaylistRemoteFolder(source *playlistRemoteSource, folder *playlistRemoteFolder, dispatch func(func())) {
	folder.cancel = readCatalog(a.ctx, source.saved, folder.itemID, dispatch, func(updated library.Source, items []library.Item, err error) {
		if a.shutdown || a.playlistRemote[source.saved.ID] != source {
			return
		}
		folder.cancel = nil
		defer a.refreshWindows()
		if err != nil {
			a.notice = a.t("library.readFailed") + ": " + err.Error()
			return
		}
		a.notice = ""
		source.saved = updated
		source.update(folder, items)
		a.entries = mergeEntries(a.entries, library.PlayableEntries(items))
		a.invalidatePlaylist()
		a.persist()
	})
}

func (a *application) buildPlaylistTree() []library.Node {
	if len(a.playlistRemote) == 0 {
		return library.Tree(a.entries)
	}
	indices := make(map[string]int, len(a.entries))
	for i, entry := range a.entries {
		indices[entry.Key()] = i
	}
	owned := map[string]bool{}
	children := map[string][]library.Node{}
	for _, source := range a.playlistRemote {
		if source.flat {
			continue
		}
		rootID := playlistRemoteFolderID(source.saved.ID, sourceRoot(source.saved))
		children[""] = append(children[""], library.Node{ID: rootID, Name: source.saved.Name, Index: -1, Folder: true})
		for id, folder := range source.folders {
			for _, item := range folder.items {
				if item.Folder {
					children[id] = append(children[id], library.Node{ID: playlistRemoteFolderID(source.saved.ID, item.ItemID), Name: item.Name(), Index: -1, Folder: true})
				} else if index, ok := indices[item.Key()]; ok {
					entry := a.entries[index]
					owned[entry.Key()] = true
					children[id] = append(children[id], library.Node{ID: entry.Key(), Name: entry.Name, Index: index})
				}
			}
		}
	}
	nodes := library.Tree(a.entries)
	// Remote videos are flat in library.Tree, so local directory nodes stay intact.
	nodes = slices.DeleteFunc(nodes, func(node library.Node) bool { return !node.Folder && owned[node.ID] })
	return append(nodes, library.BuildTree(children)...)
}
