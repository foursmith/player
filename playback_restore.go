package main

import (
	"context"
	"errors"
	"os"
	"slices"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/foursmith/player/internal/providers"
	"github.com/foursmith/player/library"
)

func (a *application) openSavedPlayback(item savedPlayback) {
	a.cancelImports()
	generation := a.importGeneration
	locations := map[string]library.Source{}
	if item.Playlist != nil {
		for _, source := range item.Playlist.Sources {
			if current, found := a.playbackSource(source.Saved.ID); found {
				locations[source.Saved.ID] = current
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
			if item.SourceID != "" {
				a.notice = a.t("library.readFailed") + ": " + err.Error()
			}
		} else {
			for sourceID, source := range restored.sources {
				for i := range a.stored.Libraries {
					if a.stored.Libraries[i].ID == sourceID {
						a.stored.Libraries[i].Config = source.saved.Config
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
	sources   map[string]*playlistRemoteSource
	expanded  map[string]bool
	roots     []library.Node
	subtitles []string
	selected  int
}

func readSavedPlayback(parent context.Context, item savedPlayback, locations map[string]library.Source, dispatch func(func()), complete func(restoredPlaylist, error)) context.CancelFunc {
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

func resolveSavedPlayback(ctx context.Context, item savedPlayback, locations map[string]library.Source) (restoredPlaylist, error) {
	out := restoredPlaylist{sources: map[string]*playlistRemoteSource{}, expanded: map[string]bool{}, selected: -1}
	snapshot := item.Playlist
	if snapshot == nil {
		return out, os.ErrNotExist
	}
	for _, ref := range snapshot.Sources {
		saved := ref.Saved
		if current, ok := locations[saved.ID]; ok && current.Plugin == saved.Plugin {
			saved = library.CloneSources([]library.Source{current})[0]
		}
		source := &playlistRemoteSource{saved: saved, folders: map[string]*playlistRemoteFolder{}, flat: !ref.Tree}
		for _, folder := range ref.Folders {
			f := source.folder(folder.ItemID)
			for _, child := range folder.Children {
				f.items = append(f.items, library.Item{Entry: library.Entry{Name: child.Name, SourceID: saved.ID, ItemID: child.ID}, Folder: true})
			}
		}
		out.sources[saved.ID] = source
	}
	resolver := playbackResolver{ctx: ctx, sources: out.sources, sessions: map[string]playbackConnection{}, folderErrors: map[string]error{}}
	defer resolver.close()
	resolved := make([]library.Entry, len(snapshot.Entries))
	selected := -1
	key := item.key()
	for i, ref := range snapshot.Entries {
		if (library.Entry{Path: ref.Path, SourceID: ref.SourceID, ItemID: ref.ItemID}).Key() == key {
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
	session library.Session
	err     error
}

type playbackResolver struct {
	ctx          context.Context
	sources      map[string]*playlistRemoteSource
	sessions     map[string]playbackConnection
	folderErrors map[string]error
}

func (r *playbackResolver) entry(ref playlistEntryReference) (library.Entry, error) {
	if ref.SourceID == "" {
		entry, err := providers.ResolveLocal(r.ctx, ref.Path)
		entry.Group, entry.Root = ref.Group, ref.Root
		return entry, err
	}

	source := r.sources[ref.SourceID]
	if source == nil {
		return library.Entry{}, os.ErrNotExist
	}
	connection, connected := r.sessions[ref.SourceID]
	if !connected {
		connection.session, connection.err = libraryPlugins.Open(r.ctx, source.saved)
		r.sessions[ref.SourceID] = connection
		if connection.err == nil {
			source.saved = library.CloneSources([]library.Source{connection.session.Source()})[0]
		}
	}
	if connection.err != nil {
		return library.Entry{}, connection.err
	}
	playback, err := library.ResolvePlayback(r.ctx, connection.session, library.Reference{SourceID: ref.SourceID, ItemID: ref.ItemID, ParentID: ref.ParentID})
	if err != nil {
		return library.Entry{}, err
	}
	entry := library.Entry{Name: ref.Name, SourceID: ref.SourceID, ItemID: ref.ItemID, ParentID: ref.ParentID}
	folder := source.folder(ref.ParentID)
	if !folder.loaded {
		key := playlistRemoteFolderID(ref.SourceID, ref.ParentID)
		if r.folderErrors[key] == nil {
			items, err := library.ReadDirectory(r.ctx, connection.session, ref.ParentID)
			if err != nil {
				r.folderErrors[key] = err
			} else {
				source.update(folder, items)
			}
		}
	}
	for _, item := range folder.items {
		if !item.Folder && item.ItemID == ref.ItemID {
			entry = item.Entry
			break
		}
	}
	entry.Path = playback.URL
	entry.Subtitle = playback.Subtitle.URL
	entry.Playback = &playback
	return entry, nil
}

func (a *application) installPlaylist(restored restoredPlaylist) {
	a.clearPlaylistRemote()
	a.entries, a.subtitlePaths = restored.entries, restored.subtitles
	a.playlistRemote, a.expandedFolders = restored.sources, restored.expanded
	a.playlistTree = restored.roots
	a.playlistList = ui.ListState{}
	a.playlistExpanded = true
	a.invalidatePlaylist()
}

func (r *playbackResolver) close() {
	for _, connection := range r.sessions {
		if connection.session != nil {
			_ = connection.session.Close()
		}
	}
}
