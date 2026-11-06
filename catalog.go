package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/foursmith/player/library"
)

type catalogState struct {
	selected, name, search, error string
	items                         []library.Item
	sourceItems                   []library.Item
	tree                          []library.Node
	playable                      []library.Entry
	total                         int
	visible                       []int
	list                          ui.ListState
	sidebar                       ui.ScrollState
	loading                       bool
	generation                    uint64
	cancel                        context.CancelFunc
	nextRefresh                   time.Time
	thumbnails                    *catalogThumbnails
	directoryPath                 []catalogFolder
}

func (a *application) catalogIndex(id string) int {
	return slices.IndexFunc(a.stored.Libraries, func(saved library.Source) bool { return saved.ID == id })
}

func (a *application) selectedCatalog() *library.Source {
	if i := a.catalogIndex(a.catalog.selected); i >= 0 {
		return &a.stored.Libraries[i]
	}
	return nil
}

func (a *application) openCatalog() {
	if a.catalogWindow != nil {
		if a.catalogWindow.IsMinimized() {
			a.catalogWindow.Restore()
		}
		a.catalogWindow.Show()
		return
	}
	a.catalogWindow = mygo.NewWindow(mygo.WindowOptions{
		Title: a.t("library.title"), Width: 920, Height: 600,
		MinWidth: 800, MinHeight: 460, UseContentSize: true,
		TitleBarStyle: mygo.TitleBarHiddenInset, BackgroundColor: windowBackground(mygo.Theme.IsDark()),
		DisableFullScreen: true, StateKey: "library", Hidden: true,
		Content: ui.View(a.catalogView),
	})
	window := a.catalogWindow
	window.SetWindowButtonsTheme(a.preferences.Appearance)
	window.OnReadyToShow(window.Show)
	window.OnFocus(window.Invalidate)
	window.OnClosed(func() {
		a.cancelCatalogRead()
		a.stopCatalogThumbnails()
		a.catalog.list = ui.ListState{}
		a.catalogWindow = nil
	})
	if a.selectedCatalog() != nil {
		a.startCatalogThumbnails()
		a.refreshCatalog()
	} else if len(a.stored.Libraries) > 0 {
		a.selectCatalog(a.stored.Libraries[0].ID)
	}
}

func (a *application) cancelCatalogRead() {
	if a.catalog.cancel != nil {
		a.catalog.cancel()
		a.catalog.cancel = nil
	}
	a.catalog.generation++
	a.catalog.loading = false
}

func (a *application) selectCatalog(id string) {
	a.cancelConnectionEditor()
	i := a.catalogIndex(id)
	if i < 0 {
		return
	}
	a.catalog.selected, a.catalog.name = id, a.stored.Libraries[i].Name
	a.catalog.search = ""
	a.catalog.items, a.catalog.visible = nil, nil
	a.catalog.sourceItems, a.catalog.tree, a.catalog.playable = nil, nil, nil
	a.catalog.total = 0
	a.catalog.list = ui.ListState{}
	a.catalog.directoryPath = []catalogFolder{{id: sourceRoot(a.stored.Libraries[i])}}
	a.startCatalogThumbnails()
	a.refreshCatalog()
}

func (a *application) refreshCatalog() {
	if a.dlnaSharing.server != nil {
		a.dlnaSharing.server.Refresh()
	}
	a.loadCatalog(false)
}

func (a *application) loadCatalog(background bool) {
	a.cancelCatalogRead()
	if !background {
		a.catalog.error = ""
		if a.catalog.thumbnails != nil {
			a.catalog.thumbnails.failed = map[string]bool{}
		}
	}
	saved := a.selectedCatalog()
	if saved == nil {
		return
	}
	// The worker owns its paths even if the collection is edited during IO.
	snapshot := library.CloneSources([]library.Source{*saved})[0]
	a.catalog.loading = true
	a.catalog.nextRefresh = time.Now().Add(10 * time.Second)
	generation := a.catalog.generation
	a.refreshWindows()
	complete := func(updated library.Source, items []library.Item, err error) {
		if generation != a.catalog.generation || a.shutdown {
			return
		}
		a.catalog.cancel, a.catalog.loading = nil, false
		a.catalog.nextRefresh = time.Now().Add(10 * time.Second)
		if err != nil {
			key := "library.readFailed"
			a.catalog.error = a.t(key) + ": " + err.Error()
			for i := range a.catalog.items {
				a.catalog.items[i].Error = err
			}
			for i := range a.catalog.sourceItems {
				a.catalog.sourceItems[i].Error = err
			}
			a.filterCatalog()
		} else {
			if current := a.selectedCatalog(); current != nil && !bytes.Equal(current.Config, updated.Config) {
				current.Config = updated.Config
				defer a.persist()
			}
			a.catalog.error = ""
			previous := a.catalog.items
			if sourceSnapshot(snapshot) {
				previous = a.catalog.sourceItems
			}
			if !sameCatalogItems(previous, items) || sourceSnapshot(snapshot) && a.catalog.sourceItems == nil {
				a.setCatalogItems(snapshot, items)
				a.filterCatalog()
			}
		}
		a.refreshWindows()
	}
	parentID := sourceRoot(snapshot)
	if path := a.catalog.directoryPath; len(path) > 0 {
		parentID = path[len(path)-1].id
	}
	a.catalog.cancel = readCatalog(a.ctx, snapshot, parentID, a.window.Update, complete)
}

func (a *application) addCatalogFolder(path string) {
	path = filepath.Clean(path)
	for _, saved := range a.stored.Libraries {
		if saved.Local().Folder == path {
			a.selectCatalog(saved.ID)
			return
		}
	}
	saved := library.Source{ID: rand.Text(), Name: filepath.Base(path), Plugin: library.LocalID, Config: library.EncodeConfig(library.LocalConfig{Folder: path})}
	a.stored.Libraries = append(a.stored.Libraries, saved)
	a.persist()
	a.selectCatalog(saved.ID)
}

func (a *application) newCatalogCollection(paths []string) {
	name := ""
	for number := 1; ; number++ {
		name = a.t("library.collectionName", number)
		if !slices.ContainsFunc(a.stored.Libraries, func(saved library.Source) bool { return saved.Name == name }) {
			break
		}
	}
	saved := library.Source{ID: rand.Text(), Name: name, Plugin: library.LocalID, Config: library.EncodeConfig(library.LocalConfig{Paths: library.MergePaths(nil, paths)})}
	a.stored.Libraries = append(a.stored.Libraries, saved)
	a.persist()
	a.selectCatalog(saved.ID)
}

func (a *application) savePlaylistCollection() {
	a.openCatalog()
	paths := library.LocalPaths(a.entries)
	if len(paths) == 0 {
		a.catalog.error = a.t("library.noLocalVideos")
		return
	}
	a.newCatalogCollection(paths)
}

func (a *application) renameCatalog() {
	name := strings.TrimSpace(a.catalog.name)
	if saved := a.selectedCatalog(); saved != nil && name != "" {
		saved.Name, a.catalog.name = name, name
		a.persist()
	}
}

func (a *application) removeCatalog() {
	i := a.catalogIndex(a.catalog.selected)
	if i < 0 {
		return
	}
	a.cancelCatalogRead()
	a.stopCatalogThumbnails()
	a.stored.Libraries = slices.Delete(a.stored.Libraries, i, i+1)
	a.catalog.selected, a.catalog.name, a.catalog.error = "", "", ""
	a.catalog.items, a.catalog.visible = nil, nil
	a.catalog.sourceItems, a.catalog.tree, a.catalog.playable = nil, nil, nil
	a.catalog.total = 0
	a.catalog.directoryPath = nil
	a.persist()
	if len(a.stored.Libraries) > 0 {
		a.selectCatalog(a.stored.Libraries[min(i, len(a.stored.Libraries)-1)].ID)
	}
}

func (a *application) appendCatalogPaths(id string, paths []string) {
	i := a.catalogIndex(id)
	if i < 0 || !a.stored.Libraries[i].Collection() {
		return
	}
	config := a.stored.Libraries[i].Local()
	config.Paths = library.MergePaths(config.Paths, paths)
	a.stored.Libraries[i].SetLocal(config)
	a.persist()
	if a.catalog.selected == id {
		a.refreshCatalog()
	}
}

func (a *application) removeCatalogPath(id, path string) {
	i := a.catalogIndex(id)
	if i < 0 || !a.stored.Libraries[i].Collection() {
		return
	}
	saved := &a.stored.Libraries[i]
	config := saved.Local()
	config.Paths = slices.DeleteFunc(config.Paths, func(p string) bool { return p == path })
	config.Folders = slices.DeleteFunc(config.Folders, func(p string) bool { return p == path })
	saved.SetLocal(config)
	a.persist()
	if a.catalog.selected == id {
		a.refreshCatalog()
	}
}

func (a *application) appendCatalogFolders(id string, paths []string) {
	i := a.catalogIndex(id)
	if i < 0 || !a.stored.Libraries[i].Collection() {
		return
	}
	config := a.stored.Libraries[i].Local()
	config.Folders = library.MergeFolders(config.Folders, paths)
	a.stored.Libraries[i].SetLocal(config)
	a.persist()
	if a.catalog.selected == id {
		a.refreshCatalog()
	}
}

func (a *application) chooseCatalogPaths(directory bool, title string, chosen func([]string)) {
	if a.dialogOpen {
		return
	}
	a.dialogOpen = true
	parent, window, ctx := a.catalogWindow, a.window, a.ctx
	dialogTitle := a.t(title)
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: parent, Title: dialogTitle, Directory: directory, Multiple: !directory})
		window.Update(func() {
			a.dialogOpen = false
			if ctx.Err() != nil || a.shutdown {
				return
			}
			if err != nil {
				a.catalog.error = err.Error()
			} else if len(paths) > 0 {
				chosen(paths)
			}
			a.refreshWindows()
		})
	}()
}

func (a *application) relocateCatalogFolder(id string) {
	a.chooseCatalogPaths(true, "library.relocate", func(paths []string) {
		i := a.catalogIndex(id)
		if i < 0 {
			return
		}
		path := filepath.Clean(paths[0])
		for j, saved := range a.stored.Libraries {
			if j != i && saved.Local().Folder == path {
				a.catalog.error = a.t("library.duplicateFolder")
				return
			}
		}
		config := a.stored.Libraries[i].Local()
		config.Folder = path
		a.stored.Libraries[i].SetLocal(config)
		a.persist()
		if a.catalog.selected == id {
			a.selectCatalog(id)
		}
	})
}

func (a *application) relocateCatalogPath(id, oldPath string) {
	i := a.catalogIndex(id)
	if i < 0 || !a.stored.Libraries[i].Collection() {
		return
	}
	directory := slices.Contains(a.stored.Libraries[i].Local().Folders, oldPath)
	a.chooseCatalogPaths(directory, "library.relocate", func(paths []string) {
		i := a.catalogIndex(id)
		if i < 0 || !a.stored.Libraries[i].Collection() {
			return
		}
		if !directory && !library.IsVideo(paths[0]) {
			a.catalog.error = a.t("media.noVideos")
			return
		}
		saved := &a.stored.Libraries[i]
		config := saved.Local()
		members := &config.Paths
		merge := library.MergePaths
		if directory {
			members, merge = &config.Folders, library.MergeFolders
		}
		for j, path := range *members {
			if path == oldPath {
				(*members)[j] = paths[0]
			}
		}
		*members = merge(nil, *members)
		saved.SetLocal(config)
		a.persist()
		if a.catalog.selected == id {
			a.refreshCatalog()
		}
	})
}

func (a *application) playCatalog(path string, appendOnly bool) {
	if saved := a.selectedCatalog(); path == "" && saved != nil && !sourceSnapshot(*saved) {
		a.addRemotePlaylist(*saved, appendOnly)
		a.showWindow()
		return
	}
	if a.catalog.loading && len(a.catalog.items) == 0 {
		return
	}
	entries := a.catalog.playable
	if len(entries) == 0 {
		return
	}
	a.cancelImports()
	a.playlistExpanded = true
	if path == "" {
		path = entries[0].Key()
	}
	a.applyImportedEntries(entries, nil, appendOnly, path)
	a.showWindow()
}
