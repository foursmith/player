package main

import (
	"github.com/egoist/mygo/ui"
	"github.com/foursmith/vr/app/internal/library"
)

func (a *application) playlistNodes(c *ui.Context, nodes []library.Node, depth int) {
	for _, node := range nodes {
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
						if depth == 0 && node.Folder.Kind != "browser" {
							name = "source-" + node.Folder.Kind
						}
					}
					if selected {
						color = accent
					}
					ui.Icon(c, icons[name]).Size(14, 14).TextColor(color)
					ui.Text(c, node.Name).FontSize(11).TextColor(color).Ellipsis("…").Grow(1)
					if node.Folder != nil {
						ui.Icon(c, icons["caret-down"]).Size(10, 10).TextColor(white(.4))
					} else if a.entries[node.Index].Subtitle != "" {
						ui.Icon(c, icons["subtitles"]).Size(12, 12).TextColor(white(.4))
					}
				})
			})
			if row.Clicked() {
				if node.Folder == nil {
					a.openEntry(node.Index)
				} else {
					a.expandedFolders[node.ID] = !a.expandedFolders[node.ID]
					if a.expandedFolders[node.ID] && node.Folder.SourceID != "" {
						a.loadRemoteFolder(*node.Folder, nil)
					}
				}
			}
			if node.Folder != nil && a.expandedFolders[node.ID] {
				a.playlistNodes(c, node.Children, depth+1)
			}
		})
	}
}
func (a *application) clearLocalPlaylist() {
	a.persist()
	selectedRemote := a.selected >= 0 && a.entries[a.selected].SourceID != ""
	var entries []library.Entry
	selected := -1
	for i, e := range a.entries {
		if e.SourceID != "" {
			if i == a.selected {
				selected = len(entries)
			}
			entries = append(entries, e)
		}
	}
	a.entries = entries
	a.selected = selected
	if !selectedRemote {
		a.player.Reset()
		a.cancelSubtitle()
		a.cues = nil
		a.loopA = nil
		a.loopB = nil
		a.tracker.Reset()
	}
}
