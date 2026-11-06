package main

import (
	"cmp"
	"path/filepath"
	"slices"
	"strings"

	"github.com/foursmith/player/library"
)

// Saved playback shares stable media references and an immutable playlist snapshot.
type savedPlayback struct {
	Name     string
	Path     string            `json:",omitempty"`
	SourceID string            `json:",omitempty"`
	ItemID   string            `json:",omitempty"`
	Playlist *playlistSnapshot `json:",omitempty"`
}

func (item savedPlayback) key() string {
	return (library.Entry{Path: item.Path, SourceID: item.SourceID, ItemID: item.ItemID}).Key()
}

func (item savedPlayback) normalized() (savedPlayback, bool) {
	if item.Playlist == nil {
		return savedPlayback{}, false
	}
	if item.SourceID == "" {
		if !filepath.IsAbs(item.Path) {
			return savedPlayback{}, false
		}
		item.Name = filepath.Base(item.Path)
	} else {
		if item.ItemID == "" {
			return savedPlayback{}, false
		}
		item.Path = ""
		item.Name = strings.TrimSpace(item.Name)
		if item.Name == "" {
			item.Name = item.ItemID
		}
		source, found := item.Playlist.source(item.SourceID)
		if !found || library.ValidateSource(source) != nil {
			return savedPlayback{}, false
		}
	}
	key := item.key()
	for _, ref := range item.Playlist.Entries {
		entry := library.Entry{Path: ref.Path, SourceID: ref.SourceID, ItemID: ref.ItemID}
		if entry.Key() == key {
			return item, true
		}
	}
	return savedPlayback{}, false
}

// Snapshots are immutable and can be shared by recent items and the disk writer.
// Only durable references are saved; remote URLs are resolved when reopening.
type playlistSnapshot struct {
	Entries   []playlistEntryReference
	Sources   []playlistSourceSnapshot `json:",omitempty"`
	Roots     []string                 `json:",omitempty"`
	Expanded  []string                 `json:",omitempty"`
	Subtitles []string                 `json:",omitempty"`
}

type playlistEntryReference struct {
	Name     string
	Path     string `json:",omitempty"`
	Group    string `json:",omitempty"`
	Root     string `json:",omitempty"`
	SourceID string `json:",omitempty"`
	ItemID   string `json:",omitempty"`
	ParentID string `json:",omitempty"`
}

type playlistSourceSnapshot struct {
	Saved   library.Source
	Folders []playlistFolderSnapshot `json:",omitempty"`
	Tree    bool                     `json:",omitempty"`
}

type playlistFolderSnapshot struct {
	ItemID   string
	Children []playlistFolderChild `json:",omitempty"`
}

type playlistFolderChild struct{ ID, Name string }

func (snapshot *playlistSnapshot) source(id string) (library.Source, bool) {
	if snapshot != nil {
		for _, source := range snapshot.Sources {
			if source.Saved.ID == id {
				return source.Saved, true
			}
		}
	}
	return library.Source{}, false
}

func (a *application) snapshotPlaylist() *playlistSnapshot {
	if a.playlistSnapshot != nil {
		return a.playlistSnapshot
	}
	snapshot := &playlistSnapshot{Subtitles: slices.Clone(a.subtitlePaths)}
	for _, entry := range a.entries {
		ref := playlistEntryReference{Name: entry.Name, Path: entry.Path, Group: entry.Group, Root: entry.Root}
		if entry.SourceID != "" {
			ref = playlistEntryReference{Name: entry.Name, SourceID: entry.SourceID, ItemID: entry.ItemID, ParentID: entry.ParentID}
		}
		snapshot.Entries = append(snapshot.Entries, ref)
	}
	for _, node := range a.playlistModel() {
		snapshot.Roots = append(snapshot.Roots, node.ID)
	}
	for id, expanded := range a.expandedFolders {
		if expanded {
			snapshot.Expanded = append(snapshot.Expanded, id)
		}
	}
	slices.Sort(snapshot.Expanded)
	sources := map[string]*playlistRemoteSource{}
	for id, source := range a.playlistRemote {
		sources[id] = source
	}
	// Individual catalog rows can also create a playlist without a device root.
	for _, entry := range a.entries {
		if entry.SourceID == "" || sources[entry.SourceID] != nil {
			continue
		}
		saved, found := a.playbackSource(entry.SourceID)
		if !found {
			continue
		}
		source := &playlistRemoteSource{saved: saved, folders: map[string]*playlistRemoteFolder{}, flat: true}
		if a.catalog.selected == saved.ID {
			source.remember(a.catalog.directoryPath)
		}
		sources[entry.SourceID] = source
	}
	for _, source := range sources {
		saved := source.saved
		if i := a.catalogIndex(saved.ID); i >= 0 {
			saved = a.stored.Libraries[i]
		}
		saved = library.CloneSources([]library.Source{saved})[0]
		saved.ShareDLNA = false
		ref := playlistSourceSnapshot{Saved: saved, Tree: !source.flat}
		for _, folder := range source.folders {
			f := playlistFolderSnapshot{ItemID: folder.itemID}
			for _, item := range folder.items {
				if item.Folder {
					f.Children = append(f.Children, playlistFolderChild{ID: item.ItemID, Name: item.Name()})
				}
			}
			ref.Folders = append(ref.Folders, f)
		}
		slices.SortFunc(ref.Folders, func(a, b playlistFolderSnapshot) int {
			return cmp.Compare(a.ItemID, b.ItemID)
		})
		snapshot.Sources = append(snapshot.Sources, ref)
	}
	slices.SortFunc(snapshot.Sources, func(a, b playlistSourceSnapshot) int {
		return cmp.Compare(a.Saved.ID, b.Saved.ID)
	})
	a.playlistSnapshot = snapshot
	return snapshot
}

func (a *application) updatePlaybackPlaylists() {
	if a.selected < 0 || a.selected >= len(a.entries) {
		return
	}
	key := a.entries[a.selected].Key()
	if previous := a.stored.LastPlayback; previous != nil && previous.key() == key {
		item := *previous
		item.Playlist = a.snapshotPlaylist()
		a.stored.LastPlayback = &item
	}
	if !a.preferences.RecentFilesEnabled {
		return
	}
	for i, item := range a.stored.RecentMedia {
		if item.key() == key {
			a.stored.RecentMedia[i].Playlist = a.snapshotPlaylist()
			return
		}
	}
}

func (a *application) playbackRecord(entry library.Entry) (savedPlayback, bool) {
	item := savedPlayback{Name: entry.Name, Path: entry.Path, SourceID: entry.SourceID, ItemID: entry.ItemID, Playlist: a.snapshotPlaylist()}
	return item.normalized()
}

func (a *application) rememberStartupPlayback(entry library.Entry) {
	var next *savedPlayback
	if a.preferences.RecordPlaybackHistory && a.preferences.AutoResume {
		if item, valid := a.playbackRecord(entry); valid {
			next = &item
		}
	}
	a.stored.LastPlayback = next
}

func (a *application) playbackSource(sourceID string) (library.Source, bool) {
	for _, saved := range a.stored.Libraries {
		if saved.ID == sourceID {
			return saved, true
		}
	}
	if source := a.playlistRemote[sourceID]; source != nil {
		return source.saved, true
	}
	if item := a.stored.LastPlayback; item != nil {
		if saved, found := item.Playlist.source(sourceID); found {
			return saved, true
		}
	}
	for _, item := range a.stored.RecentMedia {
		if saved, found := item.Playlist.source(sourceID); found {
			return saved, true
		}
	}
	return library.Source{}, false
}
