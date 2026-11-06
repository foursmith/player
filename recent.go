package main

import "github.com/foursmith/player/library"

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
	a.refreshMenu()
	a.persist()
}
