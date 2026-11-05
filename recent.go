package main

import (
	"log"
	"os"
	"runtime"

	"github.com/egoist/mygo"
	"github.com/foursmith/player/internal/library"
)

func (a *application) rememberRecentFile(entry library.Entry) bool {
	if entry.DeviceID != "" || !a.preferences.RecentFilesEnabled {
		return false
	}
	a.stored.RecentFiles = rememberFile(a.stored.RecentFiles, entry.Path)
	return true
}

func (a *application) clearRecentFiles() {
	a.stored.RecentFiles = nil
	a.stored.LastFile = ""
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
	for i := len(a.stored.RecentFiles) - 1; i >= 0; i-- {
		path := a.stored.RecentFiles[i]
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			addSystemRecentFile(path)
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
