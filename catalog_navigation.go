package main

import (
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/foursmith/player/library"
)

type catalogFolder struct{ id, name string }

func (a *application) setCatalogItems(saved library.Source, items []library.Item) {
	if !sourceSnapshot(saved) {
		a.catalog.items = items
		return
	}
	if len(a.catalog.directoryPath) == 0 {
		a.catalog.directoryPath = []catalogFolder{{}}
	}
	// A non-nil snapshot also distinguishes an empty local library from a
	// remote directory, whose children are fetched on demand.
	a.catalog.sourceItems = append(make([]library.Item, 0, len(items)), items...)
	a.catalog.tree = library.LocalTree(saved, items)
	a.currentCatalogNodes()
}

func (a *application) currentCatalogNodes() []library.Node {
	nodes := a.catalog.tree
	for depth, folder := range a.catalog.directoryPath[min(1, len(a.catalog.directoryPath)):] {
		i := slices.IndexFunc(nodes, func(node library.Node) bool { return node.Folder && node.ID == folder.id })
		if i < 0 {
			// A refresh may remove a directory. Keep its nearest surviving parent.
			a.catalog.directoryPath = a.catalog.directoryPath[:depth+1]
			a.catalog.list = ui.ListState{}
			break
		}
		nodes = nodes[i].Children
	}
	return nodes
}

func (a *application) catalogDirectoryItems(recursive bool) []library.Item {
	var items []library.Item
	var visit func([]library.Node)
	visit = func(nodes []library.Node) {
		for _, node := range nodes {
			if !node.Folder {
				items = append(items, a.catalog.sourceItems[node.Index])
			} else {
				item := library.Item{Entry: library.Entry{Name: node.Name, Path: node.ID}, Folder: true}
				if node.Index >= 0 {
					item = a.catalog.sourceItems[node.Index]
				}
				if !recursive || node.Index >= 0 && (len(node.Children) == 0 || item.Error != nil) {
					items = append(items, item)
				}
				if recursive {
					visit(node.Children)
				}
			}
		}
	}
	visit(a.currentCatalogNodes())
	return items
}

func (a *application) enterCatalogFolder(item library.Item) {
	if !item.Folder || item.Error != nil {
		return
	}
	id := item.Path
	if item.SourceID != "" {
		id = item.ItemID
	}
	a.catalog.directoryPath = append(a.catalog.directoryPath, catalogFolder{id: id, name: item.Name()})
	a.changeCatalogDirectory()
}

func (a *application) catalogParent() {
	if len(a.catalog.directoryPath) <= 1 {
		return
	}
	a.catalog.directoryPath = a.catalog.directoryPath[:len(a.catalog.directoryPath)-1]
	a.changeCatalogDirectory()
}

func (a *application) catalogRoot() {
	if len(a.catalog.directoryPath) <= 1 {
		return
	}
	a.catalog.directoryPath = a.catalog.directoryPath[:1]
	a.changeCatalogDirectory()
}

func (a *application) changeCatalogDirectory() {
	a.catalog.search = ""
	a.catalog.list = ui.ListState{}
	a.startCatalogThumbnails()
	if a.catalog.sourceItems != nil {
		a.filterCatalog()
		a.refreshWindows()
		return
	}
	a.catalog.items, a.catalog.visible, a.catalog.playable = nil, nil, nil
	a.refreshCatalog()
}

func (a *application) catalogDirectoryName() string {
	var names []string
	if saved := a.selectedCatalog(); saved != nil {
		names = append(names, saved.Name)
	}
	for _, folder := range a.catalog.directoryPath[min(1, len(a.catalog.directoryPath)):] {
		names = append(names, folder.name)
	}
	return strings.Join(names, " / ")
}
