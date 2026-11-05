package main

import (
	"context"
	"log"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/foursmith/player/internal/dlna"
	"github.com/foursmith/player/internal/library"
)

type catalogFolder struct{ id, name string }

func rememberDLNALibraries(saved []library.Saved, devices []dlna.Device) ([]library.Saved, string) {
	out := library.CloneSaved(saved)
	first := ""
	for _, device := range devices {
		found := false
		for i := range out {
			if out[i].DeviceID == device.ID {
				out[i].Location = device.Location
				if first == "" {
					first = out[i].ID
				}
				found = true
				break
			}
		}
		if !found {
			out = append(out, library.Saved{ID: device.ID, Name: device.Name, DeviceID: device.ID, Location: device.Location})
			if first == "" {
				first = device.ID
			}
		}
	}
	return out, first
}

func (a *application) scanDLNA() {
	a.openCatalog()
	if a.scanning {
		return
	}
	a.scanning = true
	a.catalog.error = ""
	a.refreshWindows()
	window, ctx := a.window, a.ctx
	go func() {
		started := time.Now()
		log.Print("[dlna] searching for media servers")
		devices, err := dlna.Discover(ctx)
		log.Printf("[dlna] search finished devices=%d elapsed=%s error=%v", len(devices), time.Since(started).Round(time.Millisecond), err)
		window.Update(func() {
			a.scanning = false
			if ctx.Err() != nil || a.shutdown {
				return
			}
			if a.stored.DLNAUUID != "" {
				self := dlna.DeviceID("uuid:" + a.stored.DLNAUUID)
				devices = slices.DeleteFunc(devices, func(device dlna.Device) bool { return device.ID == self })
			}
			if err != nil {
				a.catalog.error = a.t("dlna.scanFailed") + ": " + err.Error()
			} else if len(devices) == 0 {
				a.catalog.error = a.t("dlna.noDevices")
			} else {
				var first string
				a.stored.Libraries, first = rememberDLNALibraries(a.stored.Libraries, devices)
				a.persist()
				if a.catalogWindow != nil {
					if selected := a.selectedCatalog(); selected == nil || selected.DeviceID == "" {
						a.selectCatalog(first)
					} else {
						a.refreshCatalog()
					}
				}
			}
			a.refreshWindows()
		})
	}()
}

func readDLNACatalog(parent context.Context, saved library.Saved, objectID string, dispatch func(func()), complete func(dlna.Device, []library.SavedItem, error)) context.CancelFunc {
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	go func() {
		device, err := dlna.Connect(ctx, saved.DeviceID, saved.Location)
		var items []library.SavedItem
		if err == nil {
			items, err = library.BrowseDLNA(ctx, device, objectID)
		}
		dispatch(func() {
			defer cancel()
			if parent.Err() == nil && ctx.Err() != context.Canceled {
				complete(device, items, err)
			}
		})
	}()
	return cancel
}

func (a *application) enterCatalogFolder(item library.SavedItem) {
	if !item.Folder || item.Error != nil {
		return
	}
	a.catalog.remotePath = append(a.catalog.remotePath, catalogFolder{item.Entry.ObjectID, item.Name()})
	a.changeCatalogDirectory()
}

func (a *application) catalogParent() {
	if len(a.catalog.remotePath) <= 1 {
		return
	}
	a.catalog.remotePath = a.catalog.remotePath[:len(a.catalog.remotePath)-1]
	a.changeCatalogDirectory()
}

func (a *application) catalogRoot() {
	if len(a.catalog.remotePath) <= 1 {
		return
	}
	a.catalog.remotePath = a.catalog.remotePath[:1]
	a.changeCatalogDirectory()
}

func (a *application) changeCatalogDirectory() {
	a.catalog.items, a.catalog.visible, a.catalog.search = nil, nil, ""
	a.catalog.list = ui.ListState{}
	a.startCatalogThumbnails()
	a.refreshCatalog()
}

func (a *application) catalogDirectoryName() string {
	var names []string
	if saved := a.selectedCatalog(); saved != nil {
		names = append(names, saved.Name)
	}
	for _, folder := range a.catalog.remotePath[min(1, len(a.catalog.remotePath)):] {
		names = append(names, folder.name)
	}
	return strings.Join(names, " / ")
}
