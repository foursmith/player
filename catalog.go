package main

import (
	"context"
	"crypto/rand"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/foursmith/player/internal/dlna"
	"github.com/foursmith/player/internal/library"
)

type catalogState struct {
	selected, name, search, error string
	items                         []library.SavedItem
	visible                       []int
	list                          ui.ListState
	sidebar                       ui.ScrollState
	loading                       bool
	generation                    uint64
	cancel                        context.CancelFunc
	nextRefresh                   time.Time
	thumbnails                    *catalogThumbnails
	remotePath                    []catalogFolder
}

func (a *application) catalogIndex(id string) int {
	return slices.IndexFunc(a.stored.Libraries, func(saved library.Saved) bool { return saved.ID == id })
}

func (a *application) selectedCatalog() *library.Saved {
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
		a.catalogWindow.Focus()
		return
	}
	a.catalogWindow = mygo.NewWindow(mygo.WindowOptions{
		Title: a.t("library.title"), Width: 920, Height: 600,
		MinWidth: 800, MinHeight: 460, UseContentSize: true,
		TitleBarStyle: mygo.TitleBarHiddenInset, BackgroundColor: windowBackground,
		DisableFullScreen: true, Parent: a.window, StateKey: "library", Hidden: true,
		Content: ui.View(a.catalogView),
	})
	window := a.catalogWindow
	window.SetWindowButtonsTheme(mygo.ThemeDark)
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
	i := a.catalogIndex(id)
	if i < 0 {
		return
	}
	a.catalog.selected, a.catalog.name = id, a.stored.Libraries[i].Name
	a.catalog.search = ""
	a.catalog.items, a.catalog.visible = nil, nil
	a.catalog.list = ui.ListState{}
	a.catalog.remotePath = nil
	if a.stored.Libraries[i].DeviceID != "" {
		a.catalog.remotePath = []catalogFolder{{id: "0", name: a.stored.Libraries[i].Name}}
	}
	a.startCatalogThumbnails()
	a.refreshCatalog()
}

func (a *application) refreshCatalog() {
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
	snapshot := *saved
	snapshot.Paths = slices.Clone(saved.Paths)
	a.catalog.loading = true
	a.catalog.nextRefresh = time.Now().Add(10 * time.Second)
	generation := a.catalog.generation
	a.refreshWindows()
	complete := func(items []library.SavedItem, err error) {
		if generation != a.catalog.generation || a.shutdown {
			return
		}
		a.catalog.cancel, a.catalog.loading = nil, false
		a.catalog.nextRefresh = time.Now().Add(10 * time.Second)
		if err != nil {
			key := "library.readFailed"
			if snapshot.DeviceID != "" {
				key = "library.dlnaUnavailable"
			}
			a.catalog.error = a.t(key) + ": " + err.Error()
			for i := range a.catalog.items {
				a.catalog.items[i].Error = err
			}
			a.filterCatalog()
		} else {
			a.catalog.error = ""
			if !sameCatalogItems(a.catalog.items, items) {
				a.catalog.items = items
				a.filterCatalog()
			}
		}
		a.refreshWindows()
	}
	if snapshot.DeviceID == "" {
		a.catalog.cancel = readCatalog(a.ctx, snapshot, a.window.Update, complete)
		return
	}
	objectID := "0"
	if path := a.catalog.remotePath; len(path) > 0 {
		objectID = path[len(path)-1].id
	}
	a.catalog.cancel = readDLNACatalog(a.ctx, snapshot, objectID, a.window.Update, func(device dlna.Device, items []library.SavedItem, err error) {
		if generation != a.catalog.generation || a.shutdown {
			return
		}
		changed := false
		if current := a.selectedCatalog(); err == nil && current != nil && current.Location != device.Location {
			current.Location, changed = device.Location, true
		}
		complete(items, err)
		if changed {
			a.persist()
		}
	})
}

// dispatch queues a UI update; the context must stay alive until that update
// runs, while a later selection can still cancel a result waiting in the queue.
func readCatalog(parent context.Context, saved library.Saved, dispatch func(func()), complete func([]library.SavedItem, error)) context.CancelFunc {
	ctx, cancel := context.WithCancel(parent)
	go func() {
		items, err := library.ReadSaved(ctx, saved)
		dispatch(func() {
			defer cancel()
			if ctx.Err() == nil {
				complete(items, err)
			}
		})
	}()
	return cancel
}

func (a *application) addCatalogFolder(path string) {
	path = filepath.Clean(path)
	for _, saved := range a.stored.Libraries {
		if saved.Folder == path {
			a.selectCatalog(saved.ID)
			return
		}
	}
	saved := library.Saved{ID: rand.Text(), Name: filepath.Base(path), Folder: path}
	a.stored.Libraries = append(a.stored.Libraries, saved)
	a.persist()
	a.selectCatalog(saved.ID)
}

func (a *application) newCatalogCollection(paths []string) {
	name := ""
	for number := 1; ; number++ {
		name = a.t("library.collectionName", number)
		if !slices.ContainsFunc(a.stored.Libraries, func(saved library.Saved) bool { return saved.Name == name }) {
			break
		}
	}
	saved := library.Saved{ID: rand.Text(), Name: name, Paths: library.MergePaths(nil, paths)}
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
	a.stored.Libraries[i].Paths = library.MergePaths(a.stored.Libraries[i].Paths, paths)
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
	saved.Paths = slices.DeleteFunc(saved.Paths, func(p string) bool { return p == path })
	a.persist()
	a.refreshCatalog()
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
			if j != i && saved.Folder == path {
				a.catalog.error = a.t("library.duplicateFolder")
				return
			}
		}
		a.stored.Libraries[i].Folder = path
		a.persist()
		if a.catalog.selected == id {
			a.refreshCatalog()
		}
	})
}

func (a *application) relocateCatalogPath(id, oldPath string) {
	a.chooseCatalogPaths(false, "library.relocate", func(paths []string) {
		i := a.catalogIndex(id)
		if i < 0 || !library.IsVideo(paths[0]) {
			a.catalog.error = a.t("media.noVideos")
			return
		}
		saved := &a.stored.Libraries[i]
		for j, path := range saved.Paths {
			if path == oldPath {
				saved.Paths[j] = paths[0]
			}
		}
		saved.Paths = library.MergePaths(nil, saved.Paths)
		a.persist()
		if a.catalog.selected == id {
			a.refreshCatalog()
		}
	})
}

func (a *application) playCatalog(path string, appendOnly bool) {
	if a.catalog.loading && len(a.catalog.items) == 0 {
		return
	}
	var entries []library.Entry
	for _, index := range a.catalog.visible {
		item := a.catalog.items[index]
		if item.Error == nil && !item.Folder {
			entries = append(entries, item.Entry)
		}
	}
	if len(entries) == 0 {
		return
	}
	a.cancelImports()
	a.playlistExpanded = true
	if path == "" {
		path = entries[0].Path
	}
	a.applyImportedEntries(entries, nil, appendOnly, path)
	a.showWindow()
}
