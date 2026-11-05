package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/foursmith/player/internal/library"
)

func (a *application) catalogView(c *ui.Context) {
	transparentTheme(c)
	if a.player != nil && a.player.Snapshot().Playing {
		c.After(time.Second)
	}
	if a.catalogWindow != nil && a.selectedCatalog() != nil && a.preferences.LibraryAutoRefresh {
		if !a.catalog.loading && !a.dialogOpen && !c.Now().Before(a.catalog.nextRefresh) {
			a.loadCatalog(true)
		}
		c.After(max(time.Second, a.catalog.nextRefresh.Sub(c.Now())))
	}
	windowSurface(c.Key("library")).Children(func() {
		a.catalogSidebar(c)
		ui.Box(c).Width(1).FillHeight().Background(white(.055))
		ui.Column(c.Key("library-detail")).Grow(1).MinWidth(0).FillHeight().Children(func() {
			ui.Row(c.Key("library-titlebar")).FillWidth().Height(max(48, c.TitleBar().Height)).Padding(0, max(16, c.TitleBar().Right+8), 0, 20).Gap(12).DragWindow().Children(func() {
				ui.Text(c, a.t("library.title")).FontSize(12).FontWeight(500).TextColor(white(.48)).Grow(1).MinWidth(0).SingleLine()
				if a.selectedCatalog() != nil {
					a.catalogSearch(c)
					quietIcon(c, "rotate-ccw", a.t("library.refresh")).Disabled(a.catalog.loading).OnClick(a.refreshCatalog)
				}
			})
			if saved := a.selectedCatalog(); saved != nil {
				a.catalogDetail(c, *saved)
			} else {
				ui.Column(c).FillWidth().Grow(1).Center().Padding(24).Gap(12).Children(func() {
					glass(c, 20).Size(64, 64).Center().Children(func() {
						ui.Icon(c, icons["library"]).Size(28, 28).TextColor(accent.Alpha(.7))
					})
					ui.Text(c, a.t("library.empty")).FontSize(18).FontWeight(600).TextColor(white(.9))
					ui.Text(c, a.t("library.emptyDescription")).FontSize(11).TextColor(white(.45)).MaxWidth(320).AlignSelf(ui.Center).Wrap()
				})
			}
			if a.catalog.error != "" {
				ui.Box(c.Key("library-error")).FillWidth().Padding(0, 16, 6).Children(func() {
					ui.Row(c).FillWidth().Padding(8, 10).Gap(8).Radius(10).Background(ui.Hex("#efad82").Alpha(.07)).Children(func() {
						ui.Icon(c, icons["info"]).Size(13, 13).TextColor(ui.Hex("#efad82")).Shrink(0)
						ui.Text(c, a.catalog.error).FontSize(10).TextColor(ui.Hex("#efad82")).Wrap().Grow(1).MinWidth(0)
					})
				})
			}
		})
	})
	if c.Shortcut(0, ui.KeyEscape) && a.catalogWindow != nil {
		a.catalogWindow.Close()
	}
}

func (a *application) catalogSearch(c *ui.Context) {
	shell := ui.Row(c.Key("library-search-shell")).Width(228).Height(28).Padding(0, 10).Gap(7).Radius(14).Background(white(.04))
	if shell.FocusWithin() {
		shell.Background(white(.07)).Border(1, accent.Alpha(.3))
	}
	shell.Children(func() {
		ui.Icon(c, icons["search"]).Size(12, 12).TextColor(white(.4)).Shrink(0)
		ui.TextInput(c.Key("library-search"), &a.catalog.search).Placeholder(a.t("library.search")).Label(a.t("library.search")).Height(28).Padding(0).FontSize(11).Background(ui.Transparent).Border(0, ui.Transparent).FocusRing(false).PlaceholderColor(white(.35)).Grow(1).MinWidth(0).OnChange(a.filterCatalog)
	})
}

func (a *application) catalogSidebar(c *ui.Context) {
	windowSidebar(c.Key("library-sidebar")).Children(func() {
		ui.Box(c).FillWidth().Height(max(48, c.TitleBar().Height)).DragWindow()
		ui.Scroll(c.Key("library-sources")).FillWidth().Grow(1).MinHeight(0).TrackScroll(&a.catalog.sidebar).Children(func() {
			ui.Column(c).FillWidth().Padding(2, 10, 12).Gap(3).Children(func() {
				for kind, title := range []string{"library.folders", "library.dlna", "library.collections"} {
					count := 0
					for _, saved := range a.stored.Libraries {
						if catalogSourceKind(saved) == kind {
							count++
						}
					}
					ui.Row(c.Key(title)).FillWidth().Height(30).Margin(8, 0, 0).Padding(0, 8).Gap(6).Children(func() {
						ui.Text(c, a.t(title)).FontSize(10).FontWeight(500).TextColor(white(.4)).Grow(1).MinWidth(0).SingleLine()
						ui.Text(c, fmt.Sprint(count)).FontSize(9).TextColor(white(.23))
						switch kind {
						case 0:
							quietIcon(c, "plus", a.t("library.addFolder")).Size(22, 22).Disabled(a.dialogOpen).OnClick(func() {
								a.chooseCatalogPaths(true, "library.addFolder", func(paths []string) { a.addCatalogFolder(paths[0]) })
							})
						case 1:
							label := a.t("playlist.scanDlna")
							if a.scanning {
								label = a.t("playlist.scanningDlna")
							}
							quietIcon(c, "source-dlna", label).Size(22, 22).Disabled(a.scanning).OnClick(a.scanDLNA)
						case 2:
							quietIcon(c, "plus", a.t("library.newCollection")).Size(22, 22).OnClick(func() { a.newCatalogCollection(nil) })
						}
					})
					for _, saved := range a.stored.Libraries {
						if catalogSourceKind(saved) != kind {
							continue
						}
						a.catalogSource(c, saved)
					}
					if count == 0 {
						ui.Text(c, a.t("library.noSources")).FontSize(10).TextColor(white(.22)).Padding(4, 10, 8)
					}
				}
			})
		})
		ui.Box(c).FillWidth().Padding(0, 14).Children(func() { ui.Box(c).FillWidth().Height(1).Background(white(.05)) })
		ui.Box(c).FillWidth().Padding(10).Children(func() {
			glassAction(c, "playlist", a.t("library.savePlaylistShort")).FillWidth().Label(a.t("library.savePlaylist")).Disabled(len(library.LocalPaths(a.entries)) == 0).OnClick(a.savePlaylistCollection)
		})
	})
}

func catalogSourceKind(saved library.Saved) int {
	if saved.Folder != "" {
		return 0
	}
	if saved.DeviceID != "" {
		return 1
	}
	return 2
}

func catalogSourceIcon(saved library.Saved) string {
	return [...]string{"folder", "source-dlna", "library"}[catalogSourceKind(saved)]
}

func (a *application) catalogSource(c *ui.Context, saved library.Saved) {
	button := navigationStyle(ui.Button(c.Key(saved.ID), ""), a.catalog.selected == saved.ID).Label(saved.Name)
	button.Children(func() {
		ui.Row(c).Fill().Gap(9).Children(func() {
			ui.Icon(c, icons[catalogSourceIcon(saved)]).Size(14, 14).Shrink(0)
			ui.Text(c, saved.Name).FontSize(11).FontWeight(500).SingleLine().Grow(1).MinWidth(0)
			if saved.ShareDLNA && saved.DeviceID == "" {
				ui.Icon(c, icons["source-dlna"]).Size(12, 12).TextColor(accent.Alpha(.7)).Shrink(0)
			}
		})
	}).OnClick(func() { a.selectCatalog(saved.ID) })
}

func (a *application) catalogDetail(c *ui.Context, saved library.Saved) {
	available, total, folders := 0, 0, 0
	for _, item := range a.catalog.items {
		if item.Folder {
			folders++
		} else {
			total++
		}
	}
	for _, index := range a.catalog.visible {
		item := a.catalog.items[index]
		if item.Error == nil && !item.Folder {
			available++
		}
	}
	ui.Column(c.Key(saved.ID)).FillWidth().Grow(1).MinHeight(0).Children(func() {
		ui.Row(c).FillWidth().Height(52).Padding(0, 20).Gap(8).Children(func() {
			glass(c, 10).Size(32, 32).Center().Children(func() {
				ui.Icon(c, icons[catalogSourceIcon(saved)]).Size(17, 17).TextColor(accent.Alpha(.8))
			})
			name := ui.TextInput(c.Key("library-name"), &a.catalog.name).Label(a.t("library.name")).Height(30).Padding(2, 4).FontSize(18).FontWeight(600).Background(ui.Transparent).Border(0, ui.Transparent).FocusRing(false).Radius(6).Grow(1).MinWidth(0).OnSubmit(a.renameCatalog)
			if name.Focused() || name.Hovered() {
				name.Background(white(.035))
			}
			trimmed := strings.TrimSpace(a.catalog.name)
			quietIcon(c, "check", a.t("library.rename")).Size(24, 24).Disabled(trimmed == "" || trimmed == saved.Name).OnClick(a.renameCatalog)
			quietIcon(c, "trash", a.t("library.remove")).Size(24, 24).OnClick(a.removeCatalog)
			a.catalogSharingButton(c, saved)
			glassAction(c, "play", a.t("library.playAll")).Background(accent.Alpha(.12)).TextColor(accent).Disabled(available == 0).OnClick(func() { a.playCatalog("", false) })
			quietIcon(c, "playlist-add", a.t("library.append")).Disabled(available == 0).OnClick(func() { a.playCatalog("", true) })
		})
		a.catalogLocation(c, saved)
		a.catalogToolbar(c, available, total, folders)
		ui.Box(c).FillWidth().Padding(0, 20, 4).Children(func() { ui.Box(c).FillWidth().Height(1).Background(white(.05)) })
		if len(a.catalog.visible) == 0 {
			ui.Column(c).FillWidth().Grow(1).Center().Gap(10).Children(func() {
				message := "library.noItems"
				icon := "file-video"
				if a.catalog.loading {
					message = "library.loading"
				} else if a.catalog.search != "" || a.preferences.LibraryFilter != catalogAll {
					message, icon = "library.noResults", "search"
				}
				ui.Icon(c, icons[icon]).Size(26, 26).TextColor(white(.2))
				ui.Text(c, a.t(message)).FontSize(12).TextColor(white(.42))
			})
		} else {
			rows, items := a.catalog.visible, a.catalog.items
			a.catalog.list.Key = func(i int) any { return items[rows[i]].Key() }
			a.catalog.list.Label = func(i int) string { return items[rows[i]].Name() }
			ui.List(c.Key("library-videos"), &a.catalog.list, len(rows), func(i int) {
				a.catalogItem(c, saved, items[rows[i]])
			}).FillWidth().Grow(1).MinHeight(0).Padding(0, 12, 4)
		}
		a.catalogSharingStatus(c, saved)
	})
}

func (a *application) catalogLocation(c *ui.Context, saved library.Saved) {
	ui.Row(c).FillWidth().Height(28).Padding(0, 20).Gap(6).Children(func() {
		if saved.DeviceID != "" {
			quietIcon(c, "caret-down", a.t("library.parent")).Rotate(90).Size(24, 24).Disabled(len(a.catalog.remotePath) <= 1).OnClick(a.catalogParent)
			ui.Text(c, a.catalogDirectoryName()).FontSize(10).TextColor(white(.4)).SingleLine().Grow(1).MinWidth(0)
			quietIcon(c, "folder", a.t("library.root")).Size(24, 24).Disabled(len(a.catalog.remotePath) <= 1).OnClick(a.catalogRoot)
		} else if saved.Folder != "" {
			ui.Icon(c, icons["folder-open"]).Size(12, 12).TextColor(white(.3)).Shrink(0)
			ui.Text(c, saved.Folder).FontSize(10).TextColor(white(.4)).SingleLine().Grow(1).MinWidth(0)
			quietIcon(c, "folder-open", a.t("library.relocate")).Size(24, 24).Disabled(a.dialogOpen).OnClick(func() { a.relocateCatalogFolder(saved.ID) })
		} else {
			glassAction(c, "plus", a.t("media.addFiles")).Height(24).Disabled(a.dialogOpen).OnClick(func() {
				a.chooseCatalogPaths(false, "media.addFiles", func(paths []string) { a.appendCatalogPaths(saved.ID, paths) })
			})
			glassAction(c, "playlist-add", a.t("library.addPlaylist")).Height(24).Disabled(len(library.LocalPaths(a.entries)) == 0).OnClick(func() {
				a.appendCatalogPaths(saved.ID, library.LocalPaths(a.entries))
			})
		}
	})
}

func (a *application) catalogToolbar(c *ui.Context, available, total, folders int) {
	ui.Row(c).FillWidth().Height(42).Padding(0, 20).Gap(6).Children(func() {
		status := a.t("library.itemCount", available, total)
		if folders > 0 {
			status += " · " + a.t("library.folderCount", folders)
		}
		if a.catalog.loading && len(a.catalog.items) == 0 {
			status = a.t("library.loading")
		}
		ui.Text(c, status).FontSize(10).TextColor(white(.35)).Grow(1).MinWidth(0).SingleLine()
		glassButton(c.Key("library-filter"), a.t(catalogFilterKeys[a.preferences.LibraryFilter])+" ▾").Label(a.t("library.filter")).Menu(func(m *ui.Menu) {
			for i, key := range catalogFilterKeys {
				disabled := !a.preferences.RecordPlaybackHistory && i >= catalogUnwatched && i <= catalogWatched
				if m.Item(a.t(key)).Disabled(disabled).Radio(i == a.preferences.LibraryFilter).Chosen() {
					a.preferences.LibraryFilter = i
					a.persist()
				}
			}
		})
		glassButton(c.Key("library-sort"), a.t(catalogSortKeys[a.preferences.LibrarySort])+" ▾").Label(a.t("library.sort")).Menu(func(m *ui.Menu) {
			for i, key := range catalogSortKeys {
				if m.Item(a.t(key)).Radio(i == a.preferences.LibrarySort).Chosen() {
					a.preferences.LibrarySort = i
					a.persist()
				}
			}
		})
		auto := quietIcon(c.Key("library-auto-refresh"), "clock", a.t("library.autoRefresh")).Role(ui.RoleSwitch).Checked(a.preferences.LibraryAutoRefresh)
		if a.preferences.LibraryAutoRefresh {
			auto.Background(accent.Alpha(.08)).TextColor(accent.Alpha(.8))
		}
		auto.OnClick(func() {
			a.preferences.LibraryAutoRefresh = !a.preferences.LibraryAutoRefresh
			a.catalog.nextRefresh = c.Now().Add(10 * time.Second)
			a.persist()
		})
	})
}

func (a *application) catalogItem(c *ui.Context, saved library.Saved, item library.SavedItem) {
	if item.Folder {
		a.catalogFolderItem(c, item)
		return
	}
	history := a.catalogHistory(item)
	timing := a.catalogPlaybackTime(item)
	row := ui.Row(c.Key(item.Key())).FillWidth().Height(76).Padding(3, 0).Gap(4)
	background := white(0)
	if row.Hovered() || row.FocusWithin() {
		background = white(.035)
	}
	if a.player != nil && a.catalogMatchesPlayback(item.Entry, a.player.Snapshot()) {
		background = accent.Alpha(.07)
	}
	background = row.AnimateColor("background", background, pickerTransition)
	row.Draw(func(p *ui.Painter, r ui.Rect) { p.Fill(r, background, 12) }).Children(func() {
		name := item.Name()
		button := ui.Button(c, "").Grow(1).MinWidth(0).FillHeight().Padding(7, 8).Background(ui.Transparent).Border(0, ui.Transparent).Radius(10).Label(a.t("library.playItem", name)).Disabled(item.Error != nil)
		button.Children(func() {
			ui.Row(c).Fill().Gap(12).Children(func() {
				ui.Box(c).Size(88, 50).Radius(8).Clip().Background(white(.04)).Children(func() {
					if preview := a.catalogThumbnail(item); preview != nil {
						ui.Image(c, preview).Fill().Fit(ui.Cover).Radius(8)
					} else {
						ui.Icon(c, icons["file-video"]).Size(20, 20).TextColor(white(.25)).Attach(ui.AnchorCenter, ui.AnchorCenter)
					}
					if button.Hovered() || button.FocusVisible() {
						ui.Box(c).Absolute().Left(0).Top(0).Right(0).Bottom(0).Background(ui.RGBA(0, 0, 0, .25)).Center().Children(func() {
							ui.Icon(c, icons["play"]).Size(18, 18).TextColor(white(.95))
						})
					}
					if timing.duration > 0 && timing.position > 0 {
						ui.Box(c).Absolute().Left(0).Bottom(0).Height(2).Width(88 * float32(timing.position/timing.duration)).Background(accent)
					}
				})
				ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
					ui.Text(c, name).FontSize(12).FontWeight(500).TextColor(white(.88)).SingleLine().FillWidth().MinWidth(0)
					ui.Text(c, a.catalogTimeLabel(timing)).Font("monospace").FontSize(10).TextColor(white(.56)).SingleLine().FillWidth().MinWidth(0)
					detail := filepath.Dir(item.Path)
					if saved.Folder != "" {
						if relative, err := filepath.Rel(saved.Folder, detail); err == nil {
							detail = relative
							if detail == "." {
								detail = saved.Name
							}
						}
					} else if saved.DeviceID != "" {
						detail = a.catalogDirectoryName()
					}
					if item.Error != nil {
						detail = a.t("library.unavailable") + " · " + detail
					}
					ui.Text(c, detail).FontSize(9).TextColor(white(.3)).SingleLine().FillWidth().MinWidth(0)
				})
			})
		}).OnClick(func() { a.playCatalog(item.Path, false) }).ContextMenu(func(m *ui.Menu) { a.catalogWatchMenu(m, item) })
		if item.Error == nil && a.preferences.RecordPlaybackHistory {
			status := history.watchStatus()
			color := white(.4)
			if status == catalogWatching {
				color = accent.Alpha(.75)
			}
			badge := ui.Button(c, "").Height(24).Padding(0, 7).Radius(12).Border(0, ui.Transparent).Background(ui.Transparent).FocusRing(false).Label(a.t("library.watchStatus", name))
			controlSurface(badge, false).TextColor(color).Children(func() {
				ui.Row(c).Gap(5).Children(func() {
					if status == catalogWatched {
						ui.Icon(c, icons["check"]).Size(10, 10)
					} else {
						ui.Box(c).Size(3, 3).Radius(1.5).Background(color)
					}
					ui.Text(c, a.t(catalogFilterKeys[status])).FontSize(10).SingleLine()
				})
			}).Menu(func(m *ui.Menu) { a.catalogWatchMenu(m, item) })
		}
		if item.Error != nil && saved.Collection() {
			quietIcon(c, "folder-open", a.t("library.relocateItem", name)).Disabled(a.dialogOpen).OnClick(func() { a.relocateCatalogPath(saved.ID, item.Path) })
		}
		if saved.Collection() {
			quietIcon(c, "x", a.t("library.removeItem", name)).OnClick(func() { a.removeCatalogPath(saved.ID, item.Path) })
		}
	})
}

func (a *application) catalogFolderItem(c *ui.Context, item library.SavedItem) {
	button := ui.Button(c.Key(item.Key()), "").FillWidth().Height(50).Padding(5, 8).Background(ui.Transparent).Border(0, ui.Transparent).Radius(10).Label(a.t("library.openFolder", item.Name())).Disabled(item.Error != nil)
	if button.Hovered() {
		button.Background(white(.04))
	}
	button.Children(func() {
		ui.Row(c).Fill().Gap(12).Children(func() {
			glass(c, 8).Size(36, 32).Center().Children(func() { ui.Icon(c, icons["folder"]).Size(17, 17).TextColor(accent.Alpha(.65)) })
			ui.Text(c, item.Name()).FontSize(12).TextColor(white(.8)).SingleLine().Grow(1).MinWidth(0)
			ui.Icon(c, icons["caret-down"]).Size(10, 10).Rotate(-90).TextColor(white(.3))
		})
	}).OnClick(func() { a.enterCatalogFolder(item) })
}

func (a *application) catalogWatchMenu(m *ui.Menu, item library.SavedItem) {
	disabled := !a.preferences.RecordPlaybackHistory || item.Error != nil || item.Folder
	for _, option := range []struct{ title, value string }{
		{"library.markWatched", "watched"}, {"library.markUnwatched", "unwatched"}, {"library.watchAutomatic", ""},
	} {
		if m.Item(a.t(option.title)).Disabled(disabled).Checked(a.catalogHistory(item).WatchOverride == option.value).Chosen() {
			a.setCatalogWatch(item, option.value)
		}
	}
}
