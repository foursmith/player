package main

import (
	"cmp"
	"net/url"
	"path/filepath"
	"slices"
	"strings"

	"github.com/foursmith/player/internal/library"
)

// Saved playback shares stable media references and an immutable playlist snapshot.
type savedPlayback struct {
	Name     string
	Path     string            `json:",omitempty"`
	DeviceID string            `json:",omitempty"`
	ObjectID string            `json:",omitempty"`
	Playlist *playlistSnapshot `json:",omitempty"`
}

func (item savedPlayback) key() string {
	return (library.Entry{Path: item.Path, DeviceID: item.DeviceID, ObjectID: item.ObjectID}).Key()
}

func (item savedPlayback) normalized() (savedPlayback, bool) {
	if item.Playlist == nil {
		return savedPlayback{}, false
	}
	if item.DeviceID == "" {
		if !filepath.IsAbs(item.Path) {
			return savedPlayback{}, false
		}
		item.Name = filepath.Base(item.Path)
	} else {
		if item.ObjectID == "" {
			return savedPlayback{}, false
		}
		item.Path = ""
		item.Name = strings.TrimSpace(item.Name)
		if item.Name == "" {
			item.Name = item.ObjectID
		}
		source, found := item.Playlist.device(item.DeviceID)
		location, err := url.Parse(source.Location)
		if !found || err != nil || location.Host == "" || location.User != nil || (location.Scheme != "http" && location.Scheme != "https") {
			return savedPlayback{}, false
		}
	}
	key := item.key()
	for _, ref := range item.Playlist.Entries {
		entry := library.Entry{Path: ref.Path, DeviceID: ref.DeviceID, ObjectID: ref.ObjectID}
		if entry.Key() == key && (item.DeviceID == "" || ref.ParentID != "") {
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
	DeviceID string `json:",omitempty"`
	ObjectID string `json:",omitempty"`
	ParentID string `json:",omitempty"`
}

type playlistSourceSnapshot struct {
	Saved   library.Saved
	Folders []playlistFolderSnapshot `json:",omitempty"`
	Tree    bool                     `json:",omitempty"`
}

type playlistFolderSnapshot struct {
	ObjectID string
	Children []playlistFolderChild `json:",omitempty"`
}

type playlistFolderChild struct{ ID, Name string }

func (snapshot *playlistSnapshot) device(id string) (library.Saved, bool) {
	if snapshot != nil {
		for _, source := range snapshot.Sources {
			if source.Saved.DeviceID == id {
				return source.Saved, true
			}
		}
	}
	return library.Saved{}, false
}

func (a *application) snapshotPlaylist() *playlistSnapshot {
	if a.playlistSnapshot != nil {
		return a.playlistSnapshot
	}
	snapshot := &playlistSnapshot{Subtitles: slices.Clone(a.subtitlePaths)}
	for _, entry := range a.entries {
		ref := playlistEntryReference{Name: entry.Name, Path: entry.Path, Group: entry.Group, Root: entry.Root}
		if entry.DeviceID != "" {
			ref = playlistEntryReference{Name: entry.Name, DeviceID: entry.DeviceID, ObjectID: entry.ObjectID, ParentID: entry.ParentID}
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
	sources := map[string]*playlistDLNASource{}
	for id, source := range a.playlistDLNA {
		sources[id] = source
	}
	// Individual catalog rows can also create a playlist without a device root.
	for _, entry := range a.entries {
		if entry.DeviceID == "" || sources[entry.DeviceID] != nil {
			continue
		}
		saved, found := a.playbackDevice(entry.DeviceID)
		if !found {
			continue
		}
		source := &playlistDLNASource{saved: saved, folders: map[string]*playlistDLNAFolder{}, flat: true}
		if a.catalog.selected == saved.ID {
			source.remember(a.catalog.directoryPath)
		}
		sources[entry.DeviceID] = source
	}
	for _, source := range sources {
		saved := source.saved
		saved.Folder, saved.Paths, saved.Folders, saved.ShareDLNA = "", nil, nil, false
		ref := playlistSourceSnapshot{Saved: saved, Tree: !source.flat}
		for _, folder := range source.folders {
			f := playlistFolderSnapshot{ObjectID: folder.objectID}
			for _, item := range folder.items {
				if item.Folder {
					f.Children = append(f.Children, playlistFolderChild{ID: item.ObjectID, Name: item.Name()})
				}
			}
			ref.Folders = append(ref.Folders, f)
		}
		slices.SortFunc(ref.Folders, func(a, b playlistFolderSnapshot) int {
			return cmp.Compare(a.ObjectID, b.ObjectID)
		})
		snapshot.Sources = append(snapshot.Sources, ref)
	}
	slices.SortFunc(snapshot.Sources, func(a, b playlistSourceSnapshot) int {
		return cmp.Compare(a.Saved.DeviceID, b.Saved.DeviceID)
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
	item := savedPlayback{Name: entry.Name, Path: entry.Path, DeviceID: entry.DeviceID, ObjectID: entry.ObjectID, Playlist: a.snapshotPlaylist()}
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

func (a *application) playbackDevice(deviceID string) (library.Saved, bool) {
	if source := a.playlistDLNA[deviceID]; source != nil {
		return source.saved, true
	}
	for _, saved := range a.stored.Libraries {
		if saved.DeviceID == deviceID {
			return saved, true
		}
	}
	if item := a.stored.LastPlayback; item != nil {
		if saved, found := item.Playlist.device(deviceID); found {
			return saved, true
		}
	}
	for _, item := range a.stored.RecentMedia {
		if saved, found := item.Playlist.device(deviceID); found {
			return saved, true
		}
	}
	return library.Saved{}, false
}
