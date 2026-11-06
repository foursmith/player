package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/foursmith/player/internal/dlna"
	"github.com/foursmith/player/internal/library"
)

func (a *application) openSavedPlayback(item savedPlayback) {
	a.cancelImports()
	generation := a.importGeneration
	locations := map[string]string{}
	if item.Playlist != nil {
		for _, source := range item.Playlist.Sources {
			if current, found := a.playbackDevice(source.Saved.DeviceID); found {
				locations[source.Saved.DeviceID] = current.Location
			}
		}
	}
	a.playbackRestoreCancel = readSavedPlayback(a.ctx, item, locations, a.window.Update, func(restored restoredPlaylist, err error) {
		if a.shutdown || generation != a.importGeneration {
			return
		}
		a.playbackRestoreCancel = nil
		if errors.Is(err, os.ErrNotExist) {
			a.notice = a.t("media.playbackUnavailable")
		} else if err != nil {
			a.notice = err.Error()
			if item.DeviceID != "" {
				a.notice = a.t("library.dlnaUnavailable") + ": " + err.Error()
			}
		} else {
			for deviceID, source := range restored.sources {
				for i := range a.stored.Libraries {
					if a.stored.Libraries[i].DeviceID == deviceID {
						a.stored.Libraries[i].Location = source.saved.Location
					}
				}
			}
			a.resetPlayback()
			a.installPlaylist(restored)
			a.playEntry(restored.selected)
		}
		a.refreshWindows()
	})
}

func (a *application) cancelPlaybackRestore() {
	if a.playbackRestoreCancel != nil {
		a.playbackRestoreCancel()
		a.playbackRestoreCancel = nil
	}
}

type restoredPlaylist struct {
	entries   []library.Entry
	sources   map[string]*playlistDLNASource
	expanded  map[string]bool
	roots     []library.Node
	subtitles []string
	selected  int
}

func readSavedPlayback(parent context.Context, item savedPlayback, locations map[string]string, dispatch func(func()), complete func(restoredPlaylist, error)) context.CancelFunc {
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	go func() {
		restored, err := resolveSavedPlayback(ctx, item, locations)
		dispatch(func() {
			defer cancel()
			if parent.Err() == nil && ctx.Err() != context.Canceled {
				complete(restored, err)
			}
		})
	}()
	return cancel
}

func resolveSavedPlayback(ctx context.Context, item savedPlayback, locations map[string]string) (restoredPlaylist, error) {
	out := restoredPlaylist{sources: map[string]*playlistDLNASource{}, expanded: map[string]bool{}, selected: -1}
	snapshot := item.Playlist
	if snapshot == nil {
		return out, os.ErrNotExist
	}
	for _, ref := range snapshot.Sources {
		saved := ref.Saved
		if location := locations[saved.DeviceID]; location != "" {
			saved.Location = location
		}
		source := &playlistDLNASource{saved: saved, folders: map[string]*playlistDLNAFolder{}, flat: !ref.Tree}
		for _, folder := range ref.Folders {
			f := source.folder(folder.ObjectID)
			for _, child := range folder.Children {
				f.items = append(f.items, library.SavedItem{Entry: library.Entry{Name: child.Name, DeviceID: saved.DeviceID, ObjectID: child.ID}, Folder: true})
			}
		}
		out.sources[saved.DeviceID] = source
	}
	resolver := playbackResolver{ctx: ctx, sources: out.sources, devices: map[string]playbackConnection{}, folderErrors: map[string]error{}}
	resolved := make([]library.Entry, len(snapshot.Entries))
	selected := -1
	key := item.key()
	for i, ref := range snapshot.Entries {
		if (library.Entry{Path: ref.Path, DeviceID: ref.DeviceID, ObjectID: ref.ObjectID}).Key() == key {
			selected = i
			break
		}
	}
	if selected < 0 {
		return out, os.ErrNotExist
	}
	// Resolve the requested video first. Missing unrelated files do not prevent
	// returning to a list; an unavailable selection leaves current playback intact.
	order := []int{selected}
	for i := range snapshot.Entries {
		if i != selected {
			order = append(order, i)
		}
	}
	for _, i := range order {
		if err := ctx.Err(); err != nil {
			if err == context.Canceled || resolved[selected].Path == "" {
				return out, err
			}
			break
		}
		entry, err := resolver.entry(snapshot.Entries[i])
		if err != nil && i == selected {
			return out, err
		}
		resolved[i] = entry
	}
	for i, entry := range resolved {
		if entry.Path != "" {
			if i == selected {
				out.selected = len(out.entries)
			}
			out.entries = append(out.entries, entry)
		}
	}
	for _, id := range snapshot.Expanded {
		out.expanded[id] = true
	}
	for _, id := range snapshot.Roots {
		out.roots = append(out.roots, library.Node{ID: id})
	}
	out.subtitles = slices.Clone(snapshot.Subtitles)
	return out, nil
}

type playbackConnection struct {
	device dlna.Device
	err    error
}

type playbackResolver struct {
	ctx          context.Context
	sources      map[string]*playlistDLNASource
	devices      map[string]playbackConnection
	folderErrors map[string]error
}

func (r *playbackResolver) entry(ref playlistEntryReference) (library.Entry, error) {
	if ref.DeviceID == "" {
		if !filepath.IsAbs(ref.Path) {
			return library.Entry{}, os.ErrNotExist
		}
		items, err := library.ReadSaved(r.ctx, library.Saved{Paths: []string{ref.Path}})
		if err != nil {
			return library.Entry{}, err
		}
		if items[0].Error != nil {
			return library.Entry{}, items[0].Error
		}
		entry := items[0].Entry
		entry.Group, entry.Root = ref.Group, ref.Root
		return entry, nil
	}
	source := r.sources[ref.DeviceID]
	if source == nil || ref.ParentID == "" {
		return library.Entry{}, os.ErrNotExist
	}
	connection, connected := r.devices[ref.DeviceID]
	if !connected {
		connection.device, connection.err = dlna.Connect(r.ctx, ref.DeviceID, source.saved.Location)
		r.devices[ref.DeviceID] = connection
		if connection.err == nil {
			source.saved.Location = connection.device.Location
		}
	}
	if connection.err != nil {
		return library.Entry{}, connection.err
	}
	folder := source.folder(ref.ParentID)
	if !folder.loaded {
		key := playlistDLNAFolderID(ref.DeviceID, ref.ParentID)
		if err := r.folderErrors[key]; err != nil {
			return library.Entry{}, err
		}
		items, err := library.BrowseDLNA(r.ctx, connection.device, ref.ParentID)
		if err != nil {
			r.folderErrors[key] = err
			return library.Entry{}, err
		}
		source.update(folder, items)
	}
	for _, item := range folder.items {
		if !item.Folder && item.Error == nil && item.ObjectID == ref.ObjectID {
			return item.Entry, nil
		}
	}
	return library.Entry{}, os.ErrNotExist
}

func (a *application) installPlaylist(restored restoredPlaylist) {
	a.clearPlaylistDLNA()
	a.entries, a.subtitlePaths = restored.entries, restored.subtitles
	a.playlistDLNA, a.expandedFolders = restored.sources, restored.expanded
	a.playlistTree = restored.roots
	a.playlistList = ui.ListState{}
	a.playlistExpanded = true
	a.invalidatePlaylist()
}
