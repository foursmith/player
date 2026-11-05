package main

import (
	"encoding/base64"
	"fmt"

	"github.com/foursmith/player/internal/dlna"
	"github.com/foursmith/player/internal/library"
)

func (a *application) scanDLNA() {
	if a.scanning {
		return
	}
	a.scanning = true
	window, ctx := a.window, a.ctx
	go func() {
		devices, err := dlna.Discover(ctx)
		window.Update(func() {
			if ctx.Err() != nil {
				return
			}
			a.scanning = false
			if err != nil {
				a.notice = a.t("dlna.scanFailed") + ": " + err.Error()
				return
			}
			for _, device := range devices {
				if _, exists := a.dlnaDevices[device.ID]; !exists {
					a.folders = append(a.folders, library.Folder{ID: device.ID, Name: device.Name, DeviceID: device.ID, ObjectID: "0"})
				}
				// Keep loaded object IDs and resource URLs when rediscovering a device.
				a.dlnaDevices[device.ID] = device
			}
			a.playlistExpanded = true
			a.invalidatePlaylist()
			if len(devices) == 0 {
				a.notice = a.t("dlna.noDevices")
			}
		})
	}()
}

func (a *application) loadDLNAFolder(folder library.Folder) {
	if folder.Loaded || a.loadingFolders[folder.ID] {
		return
	}
	device, exists := a.dlnaDevices[folder.DeviceID]
	if !exists {
		return
	}
	a.loadingFolders[folder.ID] = true
	window, ctx := a.window, a.ctx
	go func() {
		items, err := device.Browse(ctx, folder.ObjectID)
		window.Update(func() {
			if ctx.Err() != nil {
				return
			}
			delete(a.loadingFolders, folder.ID)
			if err != nil {
				a.notice = fmt.Sprintf("%s: %v", folder.Name, err)
				return
			}
			for i := range a.folders {
				if a.folders[i].ID == folder.ID {
					a.folders[i].Loaded = true
				}
			}
			var entries []library.Entry
			var subtitles []dlna.Item
			for _, item := range items {
				id := folder.DeviceID + ":" + base64.RawURLEncoding.EncodeToString([]byte(item.ID))
				if item.Folder {
					a.folders = append(a.folders, library.Folder{ID: id, Parent: folder.ID, Name: item.Name, DeviceID: folder.DeviceID, ObjectID: item.ID})
				} else if item.SubtitleFile {
					subtitles = append(subtitles, item)
				} else {
					entries = append(entries, library.Entry{Name: item.Name, Path: item.URL, Group: folder.ID, DeviceID: folder.DeviceID, Subtitle: item.Subtitle})
				}
			}
			var videos, names []string
			var unmatched []int
			for i, entry := range entries {
				if entry.Subtitle == "" {
					videos = append(videos, entry.Name)
					unmatched = append(unmatched, i)
				}
			}
			for _, item := range subtitles {
				names = append(names, item.Name)
			}
			for vi, si := range library.MatchSubtitles(videos, names) {
				entries[unmatched[vi]].Subtitle = subtitles[si].URL
			}
			a.entries = append(a.entries, entries...)
			a.invalidatePlaylist()
		})
	}()
}
