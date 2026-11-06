package main

import (
	"cmp"
	"slices"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/foursmith/player/internal/library"
)

type playlistRow struct {
	node  library.Node
	depth int
}

func (a *application) invalidatePlaylist() {
	a.playlistSnapshot = nil
	a.playlistTreeValid = false
	a.playlistRowsValid = false
}

func (a *application) playlistModel() []library.Node {
	if !a.playlistTreeValid {
		// Preserve root insertion order while keeping natural order within folders.
		positions := make(map[string]int, len(a.playlistTree))
		for i, node := range a.playlistTree {
			positions[node.ID] = i
		}
		nodes := a.buildPlaylistTree()
		for _, node := range nodes {
			if _, exists := positions[node.ID]; !exists {
				positions[node.ID] = len(positions)
			}
		}
		slices.SortStableFunc(nodes, func(left, right library.Node) int {
			return cmp.Compare(positions[left.ID], positions[right.ID])
		})
		a.playlistTree = nodes
		a.playlistTreeValid = true
	}
	return a.playlistTree
}

func (a *application) visiblePlaylist() []playlistRow {
	if !a.playlistRowsValid {
		// Clear old references when a large library shrinks.
		clear(a.playlistRows)
		a.playlistRows = a.playlistRows[:0]
		var appendRows func([]library.Node, int)
		appendRows = func(nodes []library.Node, depth int) {
			for _, node := range nodes {
				a.playlistRows = append(a.playlistRows, playlistRow{node, depth})
				if node.Folder && a.expandedFolders[node.ID] {
					appendRows(node.Children, depth+1)
				}
			}
		}
		appendRows(a.playlistModel(), 0)
		a.playlistRowsValid = true
	}
	return a.playlistRows
}

func (a *application) playlistRow(c *ui.Context, item playlistRow) {
	node, depth := item.node, item.depth
	ui.Box(c.Key(node.ID)).FillWidth().Children(func() {
		selected := !node.Folder && node.Index == a.selected
		row := ui.Button(c, "").FillWidth().Height(36).Padding(0).Background(ui.Transparent).Border(0, ui.Transparent).Radius(10).Label(node.Name)
		if selected {
			row.Background(ink(c, .08))
		}
		row.Children(func() {
			ui.Row(c).Fill().Padding(0, 8+float32(depth)*14).Gap(8).Children(func() {
				name := "file-video"
				color := ink(c, .66)
				if node.Folder {
					name = "folder"
					if a.expandedFolders[node.ID] {
						name = "folder-open"
					}
				}
				if selected {
					color = c.Theme().Accent
				}
				ui.Icon(c, icons[name]).Size(14, 14).TextColor(color)
				ui.Text(c, node.Name).FontSize(11).TextColor(color).SingleLine().Grow(1).MinWidth(0)
				if _, folder := a.playlistDLNAFolder(node.ID); folder != nil && folder.cancel != nil {
					ui.Text(c, a.t("library.loading")).FontSize(10).TextColor(textColor(c, .4))
				}
				if node.Folder {
					ui.Icon(c, icons["caret-down"]).Size(10, 10).TextColor(textColor(c, .4))
				} else if a.entries[node.Index].Subtitle != "" {
					ui.Icon(c, icons["subtitles"]).Size(12, 12).TextColor(textColor(c, .4))
				}
			})
		})
		row.OnClick(func() {
			if !node.Folder {
				a.openEntry(node.Index)
			} else {
				a.togglePlaylistFolder(node.ID)
			}
		})
	})
}
func (a *application) cancelImports() {
	a.cancelPlaybackRestore()
	a.importGeneration++
	a.pendingPaths = nil
	a.openFilesAt = time.Time{}
}

func (a *application) clearPlaylist() {
	a.cancelImports()
	a.resetPlayback()
	a.clearPlaylistDLNA()
	a.entries = nil
	a.playlistTree = nil
	a.subtitlePaths = nil
	a.invalidatePlaylist()
	a.playlistList = ui.ListState{}
}

func mergeEntries(current, incoming []library.Entry) []library.Entry {
	seen := make(map[string]bool, len(current)+len(incoming))
	out := make([]library.Entry, 0, len(current)+len(incoming))
	for _, entries := range [][]library.Entry{current, incoming} {
		for _, entry := range entries {
			key := entry.Key()
			if !seen[key] {
				seen[key] = true
				out = append(out, entry)
			}
		}
	}
	return out
}

func importedPlaylist(current, incoming []library.Entry, selected int, appendOnly bool) ([]library.Entry, int, bool) {
	if !appendOnly {
		current = nil
		selected = -1
	}
	queue := mergeEntries(current, incoming)
	if selected >= 0 && selected < len(current) {
		return queue, selected, false
	}
	first := library.FirstVideo(library.Tree(incoming))
	if first >= 0 {
		for i, entry := range queue {
			if entry.Path == incoming[first].Path {
				return queue, i, true
			}
		}
	}
	return queue, -1, false
}

func (a *application) applyImportedEntries(entries []library.Entry, subtitles []string, appendOnly bool, startPath string) {
	queue, selected, play := importedPlaylist(a.entries, entries, a.selected, appendOnly)
	if !appendOnly {
		a.resetPlayback()
		a.clearPlaylistDLNA()
		a.playlistTree = nil
		for i, entry := range queue {
			if startPath != "" && entry.Path == startPath {
				selected, play = i, true
				break
			}
		}
		a.subtitlePaths = nil
		a.expandedFolders = map[string]bool{}
	}
	a.subtitlePaths = append(a.subtitlePaths, subtitles...)
	a.entries = queue
	a.invalidatePlaylist()
	var expand func([]library.Node)
	expand = func(nodes []library.Node) {
		for _, node := range nodes {
			if node.Folder {
				if _, folder := a.playlistDLNAFolder(node.ID); folder != nil && !folder.loaded {
					continue
				}
				a.expandedFolders[node.ID] = true
				expand(node.Children)
			}
		}
	}
	expand(a.playlistModel())
	if play {
		a.playEntry(selected)
	}
}

func (a *application) closeVideo() {
	a.cancelImports()
	a.resetPlayback()
}

func (a *application) resetPlayback() {
	a.persist()
	a.cancelModeDetection()
	a.stopSeekPreviews()
	a.seekUI = seekInteraction{}
	a.detectMode = false
	a.cancelSubtitle()
	a.player.Reset()
	a.selected = -1
	a.cues = nil
	a.loopA, a.loopB = nil, nil
	a.singleClickAt = time.Time{}
	a.scrubbing, a.dragging = false, false
	a.adjustmentsOpen, a.projectionOpen = false, false
	a.notice = ""
	a.tracker.Reset()
	if a.pipWindow != nil {
		a.pipWindow.Close()
	}
	a.window.SetTitle(appInfo.Name)
	a.stored.LastPlayback = nil
	a.persist()
}
