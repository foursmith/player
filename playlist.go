package main

import (
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/foursmith/player/internal/library"
)

type playlistRow struct {
	node  library.Node
	depth int
}

func (a *application) invalidatePlaylist() {
	a.playlistTreeValid = false
	a.playlistRowsValid = false
}

func (a *application) playlistModel() []library.Node {
	if !a.playlistTreeValid {
		a.playlistTree = library.Tree(a.entries, a.folders)
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
				if node.Folder != nil && a.expandedFolders[node.ID] {
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
		selected := node.Folder == nil && node.Index == a.selected
		row := ui.Button(c, "").FillWidth().Height(36).Padding(0).Background(ui.Transparent).Border(0, ui.Transparent).Radius(10).Label(node.Name)
		if selected {
			row.Background(white(.08))
		}
		row.Children(func() {
			ui.Row(c).Fill().Padding(0, 8+float32(depth)*14).Gap(8).Children(func() {
				name := "file-video"
				color := white(.66)
				if node.Folder != nil {
					name = "folder"
					if a.expandedFolders[node.ID] {
						name = "folder-open"
					}
					if depth == 0 && node.Folder.DeviceID != "" {
						name = "source-dlna"
					}
				}
				if selected {
					color = accent
				}
				ui.Icon(c, icons[name]).Size(14, 14).TextColor(color)
				ui.Text(c, node.Name).FontSize(11).TextColor(color).SingleLine().Grow(1).MinWidth(0)
				if node.Folder != nil {
					ui.Icon(c, icons["caret-down"]).Size(10, 10).TextColor(white(.4))
				} else if a.entries[node.Index].Subtitle != "" {
					ui.Icon(c, icons["subtitles"]).Size(12, 12).TextColor(white(.4))
				}
			})
		})
		row.OnClick(func() {
			if node.Folder == nil {
				a.openEntry(node.Index)
			} else {
				a.expandedFolders[node.ID] = !a.expandedFolders[node.ID]
				a.playlistRowsValid = false
				if a.expandedFolders[node.ID] && node.Folder.DeviceID != "" {
					a.loadDLNAFolder(*node.Folder)
				}
			}
		})
	})
}
func (a *application) cancelImports() {
	a.importGeneration++
	a.pendingPaths = nil
	a.openFilesAt = time.Time{}
}

func (a *application) clearLocalPlaylist() {
	a.cancelImports()
	selectedDLNA := a.selected >= 0 && a.selected < len(a.entries) && a.entries[a.selected].DeviceID != ""
	if !selectedDLNA {
		a.resetPlayback()
	}
	entries := dlnaEntries(a.entries)
	selected := -1
	for i, e := range entries {
		if selectedDLNA && e.Path == a.entries[a.selected].Path {
			selected = i
		}
	}
	a.entries = entries
	a.subtitlePaths = nil
	a.invalidatePlaylist()
	a.selected = selected
}

func dlnaEntries(entries []library.Entry) []library.Entry {
	var out []library.Entry
	for _, entry := range entries {
		if entry.DeviceID != "" {
			out = append(out, entry)
		}
	}
	return out
}

func mergeEntries(current, incoming []library.Entry) []library.Entry {
	seen := make(map[string]bool, len(current)+len(incoming))
	out := make([]library.Entry, 0, len(current)+len(incoming))
	for _, entries := range [][]library.Entry{current, incoming} {
		for _, entry := range entries {
			if !seen[entry.Path] {
				seen[entry.Path] = true
				out = append(out, entry)
			}
		}
	}
	return out
}

func importedPlaylist(current, incoming []library.Entry, selected int, appendOnly bool) ([]library.Entry, int, bool) {
	if !appendOnly {
		current = dlnaEntries(current)
		selected = -1
	}
	queue := mergeEntries(current, incoming)
	if selected >= 0 && selected < len(current) {
		return queue, selected, false
	}
	first := library.FirstVideo(library.Tree(incoming, nil))
	if first >= 0 {
		for i, entry := range queue {
			if entry.Path == incoming[first].Path {
				return queue, i, true
			}
		}
	}
	return queue, -1, false
}

func (a *application) closeVideo() {
	a.cancelImports()
	a.resetPlayback()
}

func (a *application) resetPlayback() {
	a.persist()
	a.cancelExport()
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
	a.stored.LastFile = ""
	a.persist()
}
