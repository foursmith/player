package main

import (
	"runtime"

	"github.com/egoist/mygo"
)

func (a *application) refreshMenu() {
	if a.window == nil {
		return
	}
	a.menuLocale = a.preferences.Locale
	menu, dock := a.newMenus()
	if runtime.GOOS == "darwin" {
		mygo.App.Dock.SetMenu(dock)
	}
	mygo.App.SetMenu(menu)
}

func (a *application) newMenus() (*mygo.Menu, *mygo.Menu) {
	action := func(key, accelerator string, run func()) *mygo.MenuItem {
		return &mygo.MenuItem{Label: a.t(key), Accelerator: accelerator, Click: func(*mygo.MenuItem, *mygo.Window) {
			a.window.Update(run)
		}}
	}
	settings := action("common.settings", "CmdOrCtrl+,", a.openSettings)
	file := []*mygo.MenuItem{
		action("media.showPlayer", "", a.showWindow),
		mygo.Separator(),
		action("media.openFiles", "CmdOrCtrl+O", func() { a.choose(false, false) }),
		action("media.openFolder", "CmdOrCtrl+Shift+O", func() { a.choose(true, false) }),
	}
	if a.preferences.RecentFilesEnabled {
		file = append(file, &mygo.MenuItem{Label: a.t("media.recentFiles"), Submenu: a.recentMenuItems()})
	}
	file = append(file,
		mygo.Separator(),
		action("library.title", "", a.openCatalog),
		action("library.savePlaylist", "", a.savePlaylistCollection),
		mygo.Separator(),
		action("media.addFiles", "CmdOrCtrl+Shift+A", func() { a.choose(false, true) }),
		action("media.addFolder", "", func() { a.choose(true, true) }),
		action("playlist.scanDlna", "CmdOrCtrl+Shift+D", a.scanDLNA),
		mygo.Separator(),
		action("media.closeVideo", "CmdOrCtrl+Shift+W", a.closeVideo),
		&mygo.MenuItem{Role: mygo.RoleClose},
	)
	var items []*mygo.MenuItem
	var dockMenu *mygo.Menu
	if runtime.GOOS == "darwin" {
		dock := a.recentMenuItems()
		if len(dock) > 0 {
			dock = append(dock, mygo.Separator())
		}
		dock = append(dock,
			action("media.showPlayer", "", a.showWindow),
			mygo.Separator(),
			action("media.openFiles", "", func() { a.choose(false, false) }),
			action("media.openFolder", "", func() { a.choose(true, false) }),
		)
		dockMenu = mygo.NewMenu(dock)
		items = append(items, &mygo.MenuItem{Role: mygo.RoleAppMenu, Submenu: []*mygo.MenuItem{
			{Role: mygo.RoleAbout}, mygo.Separator(), settings, mygo.Separator(),
			{Role: mygo.RoleServices}, mygo.Separator(), {Role: mygo.RoleHide}, {Role: mygo.RoleHideOthers}, {Role: mygo.RoleUnhide}, mygo.Separator(), {Role: mygo.RoleQuit},
		}})
	} else {
		file = append(file, mygo.Separator(), settings, &mygo.MenuItem{Role: mygo.RoleQuit})
	}
	items = append(items,
		&mygo.MenuItem{Label: a.t("menu.file"), Submenu: file},
		&mygo.MenuItem{Role: mygo.RoleEditMenu},
		&mygo.MenuItem{Label: a.t("settings.playback"), Submenu: []*mygo.MenuItem{
			action("shortcuts.playPause", "", func() {
				if a.selected >= 0 {
					a.player.Toggle()
				}
			}),
			action("controls.nextVideo", "CmdOrCtrl+Right", func() { a.next(false) }),
			action("playlist.title", "CmdOrCtrl+L", func() { a.playlistExpanded = !a.playlistExpanded }),
			mygo.Separator(),
			{Role: mygo.RoleToggleFullScreen},
		}},
		&mygo.MenuItem{Role: mygo.RoleWindowMenu},
	)
	menu := mygo.NewMenu(items)
	if a.shortcutRecording != "" {
		var disable func([]*mygo.MenuItem)
		disable = func(items []*mygo.MenuItem) {
			for _, item := range items {
				item.SetEnabled(false)
				disable(item.Submenu)
			}
		}
		disable(menu.Items())
	}
	return menu, dockMenu
}

// Build separate native items from the same records for each menu. A MyGo
// menu item has one parent, so the Dock and menu bar cannot share instances.
func (a *application) recentMenuItems() []*mygo.MenuItem {
	if !a.preferences.RecentFilesEnabled {
		return nil
	}
	if len(a.stored.RecentMedia) == 0 {
		return []*mygo.MenuItem{{Label: a.t("media.noRecent"), Disabled: true}}
	}
	var items []*mygo.MenuItem
	for _, item := range a.stored.RecentMedia {
		items = append(items, &mygo.MenuItem{Label: item.Name, Click: func(*mygo.MenuItem, *mygo.Window) {
			a.window.Update(func() {
				a.showWindow()
				a.openSavedPlayback(item)
			})
		}})
	}
	return append(items, mygo.Separator(), &mygo.MenuItem{Label: a.t("media.clearRecent"), Click: func(*mygo.MenuItem, *mygo.Window) {
		a.window.Update(a.clearRecentFiles)
	}})
}
