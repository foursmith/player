package main

import (
	"log"
	"os"
	"runtime"

	"github.com/egoist/mygo"
	"github.com/foursmith/player/internal/library"
)

func normalizeRecentMedia(items []savedPlayback) []savedPlayback {
	var out []savedPlayback
	seen := map[string]bool{}
	for _, item := range items {
		item, valid := item.normalized()
		if !valid {
			continue
		}
		key := item.key()
		if !seen[key] && len(out) < 10 {
			seen[key] = true
			out = append(out, item)
		}
	}
	return out
}

func (a *application) rememberRecentMedia(entry library.Entry) bool {
	if !a.preferences.RecentFilesEnabled {
		return false
	}
	item, valid := a.playbackRecord(entry)
	if !valid {
		return false
	}
	a.stored.RecentMedia = normalizeRecentMedia(append([]savedPlayback{item}, a.stored.RecentMedia...))
	return true
}

func (a *application) clearRecentFiles() {
	a.cancelPlaybackRestore()
	a.stored.RecentMedia = nil
	a.stored.LastPlayback = nil
	if mygo.App.IsReady() {
		clearSystemRecentFiles()
	}
	if a.window != nil {
		a.refreshMenu()
	}
	a.persist()
}

func (a *application) syncRecentFilesVisibility() {
	if a.window != nil {
		a.restoreSystemRecentFiles()
		a.refreshMenu()
	}
}

// Keep AppKit's Dock list in step with the application's recent files.
func (a *application) restoreSystemRecentFiles() {
	if runtime.GOOS != "darwin" {
		return
	}
	clearSystemRecentFiles()
	if !a.preferences.RecentFilesEnabled {
		return
	}
	// AppKit puts each added file first, so replay oldest to newest.
	for i := len(a.stored.RecentMedia) - 1; i >= 0; i-- {
		item := a.stored.RecentMedia[i]
		if item.DeviceID == "" {
			if info, err := os.Stat(item.Path); err == nil && !info.IsDir() {
				addSystemRecentFile(item.Path)
			}
		}
	}
}

func addSystemRecentFile(path string) {
	if runtime.GOOS == "darwin" {
		if err := mygo.App.AddRecentDocument(path); err != nil {
			log.Printf("Add system recent file: %v", err)
		}
	}
}

func clearSystemRecentFiles() {
	if runtime.GOOS == "darwin" {
		if err := mygo.App.ClearRecentDocuments(); err != nil {
			log.Printf("Clear system recent files: %v", err)
		}
	}
}
