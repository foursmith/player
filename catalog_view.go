package main

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/foursmith/player/library"
)

func (a *application) catalogView(c *ui.Context) {
	a.useTheme(c)
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
		ui.Box(c).Width(1).FillHeight().Background(ink(c, .055))
		ui.Column(c.Key("library-detail")).Grow(1).MinWidth(0).FillHeight().Children(func() {
			ui.Row(c.Key("library-titlebar")).FillWidth().Height(max(48, c.TitleBar().Height)).Padding(0, max(16, c.TitleBar().Right+8), 0, 20).Gap(12).DragWindow().Children(func() {
				ui.Text(c, a.t("library.title")).FontSize(12).FontWeight(500).TextColor(textColor(c, .48)).Grow(1).MinWidth(0).SingleLine()
				if a.connectionEditor == nil && a.selectedCatalog() != nil {
					a.catalogSearch(c)
					quietIcon(c, "rotate-ccw", a.t("library.refresh")).Disabled(a.catalog.loading).OnClick(a.refreshCatalog)
				}
			})
			if a.connectionEditor != nil {
				a.connectionView(c)
			} else if saved := a.selectedCatalog(); saved != nil {
				a.catalogDetail(c, *saved)
			} else {
				ui.Column(c).FillWidth().Grow(1).Center().Padding(24).Gap(12).Children(func() {
					glass(c, 20).Size(64, 64).Center().Children(func() {
						ui.Icon(c, icons["library"]).Size(28, 28).TextColor(c.Theme().Accent.Alpha(.7))
					})
					ui.Text(c, a.t("library.empty")).FontSize(18).FontWeight(600).TextColor(textColor(c, .9))
					ui.Text(c, a.t("library.emptyDescription")).FontSize(11).TextColor(textColor(c, .45)).MaxWidth(320).AlignSelf(ui.Center).Wrap()
				})
			}
			if a.catalog.error != "" {
				ui.Box(c.Key("library-error")).FillWidth().Padding(0, 16, 6).Children(func() {
					ui.Row(c).FillWidth().Padding(8, 10).Gap(8).Radius(10).Background(c.Theme().Warning.Alpha(.07)).Children(func() {
						ui.Icon(c, icons["info"]).Size(13, 13).TextColor(c.Theme().Warning).Shrink(0)
						ui.Text(c, a.catalog.error).FontSize(10).TextColor(c.Theme().Warning).Wrap().Grow(1).MinWidth(0)
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
	shell := ui.Row(c.Key("library-search-shell")).Width(228).Height(28).Padding(0, 10).Gap(7).Radius(14).Background(ink(c, .04))
	if shell.FocusWithin() {
		shell.Background(ink(c, .07)).Border(1, c.Theme().Accent.Alpha(.3))
	}
	shell.Children(func() {
		ui.Icon(c, icons["search"]).Size(12, 12).TextColor(textColor(c, .4)).Shrink(0)
		ui.TextInput(c.Key("library-search"), &a.catalog.search).Placeholder(a.t("library.search")).Label(a.t("library.search")).Height(28).Padding(0).FontSize(11).Background(ui.Transparent).Border(0, ui.Transparent).FocusRing(false).PlaceholderColor(textColor(c, .35)).Grow(1).MinWidth(0).OnChange(a.filterCatalog)
	})
}

func (a *application) catalogSidebar(c *ui.Context) {
	windowSidebar(c.Key("library-sidebar")).Children(func() {
		ui.Box(c).FillWidth().Height(max(48, c.TitleBar().Height)).DragWindow()
		ui.Scroll(c.Key("library-sources")).FillWidth().Grow(1).MinHeight(0).TrackScroll(&a.catalog.sidebar).Children(func() {
			ui.Column(c).FillWidth().Padding(2, 10, 12).Gap(3).Children(func() {
				for kind, title := range []string{"library.folders", "library.remote", "library.collections"} {
					count := 0
					for _, saved := range a.stored.Libraries {
						if catalogSourceKind(saved) == kind {
							count++
						}
					}
					ui.Row(c.Key(title)).FillWidth().Height(30).Margin(8, 0, 0).Padding(0, 8).Gap(6).Children(func() {
						ui.Text(c, a.t(title)).FontSize(10).FontWeight(500).TextColor(textColor(c, .4)).Grow(1).MinWidth(0).SingleLine()
						ui.Text(c, fmt.Sprint(count)).FontSize(9).TextColor(textColor(c, .23))
						switch kind {
						case 0:
							quietIcon(c, "plus", a.t("library.addFolder")).Size(22, 22).Disabled(a.dialogOpen).OnClick(func() {
								a.chooseCatalogPaths(true, "library.addFolder", func(paths []string) { a.addCatalogFolder(paths[0]) })
							})
						case 1:
							quietIcon(c, "plus", a.t("plugins.addConnection")).Size(22, 22).Menu(a.connectionMenu)
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
						ui.Text(c, a.t("library.noSources")).FontSize(10).TextColor(textColor(c, .22)).Padding(4, 10, 8)
					}
				}
			})
		})
		ui.Box(c).FillWidth().Padding(0, 14).Children(func() { ui.Box(c).FillWidth().Height(1).Background(ink(c, .05)) })
		ui.Box(c).FillWidth().Padding(10).Children(func() {
			glassAction(c, "playlist", a.t("library.savePlaylistShort")).FillWidth().Label(a.t("library.savePlaylist")).Disabled(len(library.LocalPaths(a.entries)) == 0).OnClick(a.savePlaylistCollection)
		})
	})
}

func catalogSourceKind(saved library.Source) int {
	if saved.LocalFolder() != "" {
		return 0
	}
	if !saved.IsLocal() {
		return 1
	}
	return 2
}

func catalogSourceIcon(saved library.Source) string {
	if !saved.IsLocal() {
		icon := libraryPlugins.Manifest(saved).Icon
		if _, ok := icons[icon]; ok {
			return icon
		}
		return "library"
	}
	return [...]string{"folder", "library", "library"}[catalogSourceKind(saved)]
}

func (a *application) catalogSource(c *ui.Context, saved library.Source) {
	button := navigationStyle(c, ui.Button(c.Key(saved.ID), ""), a.catalog.selected == saved.ID).Label(saved.Name)
	button.Children(func() {
		ui.Row(c).Fill().Gap(9).Children(func() {
			ui.Icon(c, icons[catalogSourceIcon(saved)]).Size(14, 14).Shrink(0)
			ui.Text(c, saved.Name).FontSize(11).FontWeight(500).SingleLine().Grow(1).MinWidth(0)
			if saved.ShareDLNA && saved.IsLocal() {
				ui.Icon(c, icons["source-dlna"]).Size(12, 12).TextColor(c.Theme().Accent.Alpha(.7)).Shrink(0)
			}
		})
	}).OnClick(func() { a.selectCatalog(saved.ID) })
}

func (a *application) catalogDetail(c *ui.Context, saved library.Source) {
	config := saved.Local()
	available, total, folders := len(a.catalog.playable), a.catalog.total, 0
	canPlay := available > 0 || !sourceSnapshot(saved)
	for _, item := range a.catalog.items {
		if item.Folder {
			folders++
		}
	}
	ui.Column(c.Key(saved.ID)).FillWidth().Grow(1).MinHeight(0).Children(func() {
		ui.Row(c).FillWidth().Height(52).Padding(0, 20).Gap(8).Children(func() {
			glass(c, 10).Size(32, 32).Center().Children(func() {
				ui.Icon(c, icons[catalogSourceIcon(saved)]).Size(17, 17).TextColor(c.Theme().Accent.Alpha(.8))
			})
			name := ui.TextInput(c.Key("library-name"), &a.catalog.name).Label(a.t("library.name")).Height(30).Padding(2, 4).FontSize(18).FontWeight(600).Background(ui.Transparent).Border(0, ui.Transparent).FocusRing(false).Radius(6).Grow(1).MinWidth(0).OnSubmit(a.renameCatalog)
			if name.Focused() || name.Hovered() {
				name.Background(ink(c, .035))
			}
			trimmed := strings.TrimSpace(a.catalog.name)
			quietIcon(c, "check", a.t("library.rename")).Size(24, 24).Disabled(trimmed == "" || trimmed == saved.Name).OnClick(a.renameCatalog)
			quietIcon(c, "trash", a.t("library.remove")).Size(24, 24).OnClick(a.removeCatalog)
			if p, err := libraryPlugins.Plugin(saved.Plugin); err == nil {
				if _, ok := p.(library.Configurable); ok {
					quietIcon(c, "settings", a.t("plugins.connection")).Size(24, 24).OnClick(func() { a.editConnection(saved) })
				}
			}
			a.catalogSharingButton(c, saved)
			glassAction(c, "play", a.t("library.playAll")).Background(c.Theme().Accent.Alpha(.12)).TextColor(c.Theme().Accent).Disabled(!canPlay).OnClick(func() { a.playCatalog("", false) })
			quietIcon(c, "playlist-add", a.t("library.append")).Disabled(!canPlay).OnClick(func() { a.playCatalog("", true) })
		})
		a.catalogLocation(c, saved, config)
		a.catalogToolbar(c, available, total, folders)
		ui.Box(c).FillWidth().Padding(0, 20, 4).Children(func() { ui.Box(c).FillWidth().Height(1).Background(ink(c, .05)) })
		if len(a.catalog.visible) == 0 {
			ui.Column(c).FillWidth().Grow(1).Center().Gap(10).Children(func() {
				message := "library.noItems"
				icon := "file-video"
				if a.catalog.loading {
					message = "library.loading"
				} else if a.catalog.search != "" || a.preferences.LibraryFilter != catalogAll {
					message, icon = "library.noResults", "search"
				}
				ui.Icon(c, icons[icon]).Size(26, 26).TextColor(textColor(c, .2))
				ui.Text(c, a.t(message)).FontSize(12).TextColor(textColor(c, .42))
			})
		} else {
			rows, items := a.catalog.visible, a.catalog.items
			a.catalog.list.Key = func(i int) any { return items[rows[i]].Key() }
			a.catalog.list.Label = func(i int) string { return items[rows[i]].Name() }
			ui.List(c.Key("library-videos"), &a.catalog.list, len(rows), func(i int) {
				a.catalogItem(c, saved, config, items[rows[i]])
			}).FillWidth().Grow(1).MinHeight(0).Padding(0, 12, 4)
		}
		a.catalogSharingStatus(c, saved)
	})
}

func (a *application) catalogLocation(c *ui.Context, saved library.Source, config library.LocalConfig) {
	ui.Row(c).FillWidth().Height(28).Padding(0, 20).Gap(6).Children(func() {
		quietIcon(c, "caret-down", a.t("library.parent")).Rotate(90).Size(24, 24).Disabled(len(a.catalog.directoryPath) <= 1).OnClick(a.catalogParent)
		location := a.catalogDirectoryName()
		if config.Folder != "" {
			location = config.Folder
			if path := a.catalog.directoryPath; len(path) > 1 {
				location = path[len(path)-1].id
			}
		}
		ui.Text(c, location).FontSize(10).TextColor(textColor(c, .4)).SingleLine().Grow(1).MinWidth(0)
		quietIcon(c, "folder", a.t("library.root")).Size(24, 24).Disabled(len(a.catalog.directoryPath) <= 1).OnClick(a.catalogRoot)
		if config.Folder != "" {
			quietIcon(c, "folder-open", a.t("library.relocate")).Size(24, 24).Disabled(a.dialogOpen).OnClick(func() { a.relocateCatalogFolder(saved.ID) })
		}
	})
	if saved.IsLocal() && config.Folder == "" {
		ui.Row(c).FillWidth().Height(28).Padding(0, 20).Gap(6).Children(func() {
			glassAction(c, "plus", a.t("media.addFiles")).Height(24).Disabled(a.dialogOpen).OnClick(func() {
				a.chooseCatalogPaths(false, "media.addFiles", func(paths []string) { a.appendCatalogPaths(saved.ID, paths) })
			})
			glassAction(c, "folder", a.t("media.addFolder")).Height(24).Disabled(a.dialogOpen).OnClick(func() {
				a.chooseCatalogPaths(true, "media.addFolder", func(paths []string) { a.appendCatalogFolders(saved.ID, paths) })
			})
			glassAction(c, "playlist-add", a.t("library.addPlaylist")).Height(24).Disabled(len(library.LocalPaths(a.entries)) == 0).OnClick(func() {
				a.appendCatalogPaths(saved.ID, library.LocalPaths(a.entries))
			})
		})
	}
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
		ui.Text(c, status).FontSize(10).TextColor(textColor(c, .35)).Grow(1).MinWidth(0).SingleLine()
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
			auto.Background(c.Theme().Accent.Alpha(.08)).TextColor(c.Theme().Accent.Alpha(.8))
		}
		auto.OnClick(func() {
			a.preferences.LibraryAutoRefresh = !a.preferences.LibraryAutoRefresh
			a.catalog.nextRefresh = c.Now().Add(10 * time.Second)
			a.persist()
		})
	})
}

func (a *application) catalogItem(c *ui.Context, saved library.Source, config library.LocalConfig, item library.Item) {
	if item.Folder {
		a.catalogFolderItem(c, saved, config, item)
		return
	}
	history := a.catalogHistory(item)
	timing := a.catalogPlaybackTime(item)
	row := ui.Row(c.Key(item.Key())).FillWidth().Height(76).Padding(3, 0).Gap(4)
	background := ink(c, 0)
	if row.Hovered() || row.FocusWithin() {
		background = ink(c, .035)
	}
	if a.player != nil && a.catalogMatchesPlayback(item.Entry, a.player.Snapshot()) {
		background = c.Theme().Accent.Alpha(.07)
	}
	background = row.AnimateColor("background", background, pickerTransition)
	row.Draw(func(p *ui.Painter, r ui.Rect) { p.Fill(r, background, 12) }).Children(func() {
		name := item.Name()
		button := ui.Button(c, "").Grow(1).MinWidth(0).FillHeight().Padding(7, 8).Background(ui.Transparent).Border(0, ui.Transparent).Radius(10).Label(a.t("library.playItem", name)).Disabled(item.Error != nil)
		button.Children(func() {
			ui.Row(c).Fill().Gap(12).Children(func() {
				ui.Box(c).Size(88, 50).Radius(8).Clip().Background(ink(c, .04)).Children(func() {
					if preview := a.catalogThumbnail(item); preview != nil {
						ui.Image(c, preview).Fill().Fit(ui.Cover).Radius(8)
					} else {
						ui.Icon(c, icons["file-video"]).Size(20, 20).TextColor(textColor(c, .25)).Attach(ui.AnchorCenter, ui.AnchorCenter)
					}
					if button.Hovered() || button.FocusVisible() {
						ui.Box(c).Absolute().Left(0).Top(0).Right(0).Bottom(0).Background(ui.RGBA(0, 0, 0, .25)).Center().Children(func() {
							ui.Icon(c, icons["play"]).Size(18, 18).TextColor(white(.95))
						})
					}
					if timing.duration > 0 && timing.position > 0 {
						ui.Box(c).Absolute().Left(0).Bottom(0).Height(2).Width(88 * float32(timing.position/timing.duration)).Background(c.Theme().Accent)
					}
				})
				ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
					ui.Text(c, name).FontSize(12).FontWeight(500).TextColor(textColor(c, .88)).SingleLine().FillWidth().MinWidth(0)
					ui.Text(c, a.catalogTimeLabel(timing)).Font("monospace").FontSize(10).TextColor(textColor(c, .56)).SingleLine().FillWidth().MinWidth(0)
					detail := filepath.Dir(item.Path)
					if config.Folder != "" {
						if relative, err := filepath.Rel(config.Folder, detail); err == nil {
							detail = relative
							if detail == "." {
								detail = saved.Name
							}
						}
					} else if !saved.IsLocal() {
						detail = a.catalogDirectoryName()
					}
					if item.Error != nil {
						detail = a.t("library.unavailable") + " · " + detail
					}
					ui.Text(c, detail).FontSize(9).TextColor(textColor(c, .3)).SingleLine().FillWidth().MinWidth(0)
				})
			})
		}).OnClick(func() { a.playCatalog(item.Key(), false) }).ContextMenu(func(m *ui.Menu) { a.catalogWatchMenu(m, item) })
		if item.Error == nil && a.preferences.RecordPlaybackHistory {
			status := history.watchStatus()
			color := ink(c, .4)
			if status == catalogWatching {
				color = c.Theme().Accent.Alpha(.75)
			}
			badge := ui.Button(c, "").Height(24).Padding(0, 7).Radius(12).Border(0, ui.Transparent).Background(ui.Transparent).FocusRing(false).Label(a.t("library.watchStatus", name))
			controlSurface(c, badge, false).TextColor(color).Children(func() {
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
		if item.Error != nil && (saved.IsLocal() && config.Folder == "") && slices.Contains(config.Paths, item.Path) {
			quietIcon(c, "folder-open", a.t("library.relocateItem", name)).Disabled(a.dialogOpen).OnClick(func() { a.relocateCatalogPath(saved.ID, item.Path) })
		}
		if (saved.IsLocal() && config.Folder == "") && slices.Contains(config.Paths, item.Path) {
			quietIcon(c, "x", a.t("library.removeItem", name)).OnClick(func() { a.removeCatalogPath(saved.ID, item.Path) })
		}
	})
}

func (a *application) catalogFolderItem(c *ui.Context, saved library.Source, config library.LocalConfig, item library.Item) {
	member := (saved.IsLocal() && config.Folder == "") && slices.Contains(config.Folders, item.Path)
	ui.Row(c.Key(item.Key())).FillWidth().Height(50).Gap(4).Children(func() {
		button := ui.Button(c, "").Grow(1).MinWidth(0).Height(50).Padding(5, 8).Background(ui.Transparent).Border(0, ui.Transparent).Radius(10).Label(a.t("library.openFolder", item.Name())).Disabled(item.Error != nil)
		if button.Hovered() {
			button.Background(ink(c, .04))
		}
		button.Children(func() {
			ui.Row(c).Fill().Gap(12).Children(func() {
				glass(c, 8).Size(36, 32).Center().Children(func() { ui.Icon(c, icons["folder"]).Size(17, 17).TextColor(c.Theme().Accent.Alpha(.65)) })
				ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
					ui.Text(c, item.Name()).FontSize(12).TextColor(textColor(c, .8)).SingleLine().FillWidth().MinWidth(0)
					if member {
						detail := item.Path
						if item.Error != nil {
							detail = a.t("library.unavailable") + " · " + detail
						}
						ui.Text(c, detail).FontSize(9).TextColor(textColor(c, .3)).SingleLine().FillWidth().MinWidth(0)
					}
				})
				ui.Icon(c, icons["caret-down"]).Size(10, 10).Rotate(-90).TextColor(textColor(c, .3))
			})
		}).OnClick(func() { a.enterCatalogFolder(item) })
		if member && item.Error != nil {
			quietIcon(c, "folder-open", a.t("library.relocateItem", item.Name())).Disabled(a.dialogOpen).OnClick(func() { a.relocateCatalogPath(saved.ID, item.Path) })
		}
		if member {
			quietIcon(c, "x", a.t("library.removeItem", item.Name())).OnClick(func() { a.removeCatalogPath(saved.ID, item.Path) })
		}
	})
}

func (a *application) catalogWatchMenu(m *ui.Menu, item library.Item) {
	disabled := !a.preferences.RecordPlaybackHistory || item.Error != nil || item.Folder
	for _, option := range []struct{ title, value string }{
		{"library.markWatched", "watched"}, {"library.markUnwatched", "unwatched"}, {"library.watchAutomatic", ""},
	} {
		if m.Item(a.t(option.title)).Disabled(disabled).Checked(a.catalogHistory(item).WatchOverride == option.value).Chosen() {
			a.setCatalogWatch(item, option.value)
		}
	}
}
