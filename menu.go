package main

import (
	"path/filepath"
	"runtime"

	"github.com/egoist/mygo"
)

func (a *application) refreshMenu() {
	a.menuLocale = a.preferences.Locale
	action := func(key, accelerator string, run func()) *mygo.MenuItem {
		return &mygo.MenuItem{Label: a.t(key), Accelerator: accelerator, Click: func(*mygo.MenuItem, *mygo.Window) {
			a.window.Update(run)
		}}
	}
	settings := action("common.settings", "CmdOrCtrl+,", func() { a.settingsOpen = true })
	recent := []*mygo.MenuItem{}
	for _, path := range a.stored.RecentFiles {
		recent = append(recent, &mygo.MenuItem{Label: filepath.Base(path), Click: func(*mygo.MenuItem, *mygo.Window) {
			a.window.Update(func() { a.importPaths([]string{path}, false) })
		}})
	}
	if len(recent) > 0 {
		recent = append(recent, mygo.Separator(), action("media.clearRecent", "", func() {
			a.stored.RecentFiles = nil
			a.stored.LastFile = ""
			clearSystemRecentFiles()
			a.refreshMenu()
			a.persist()
		}))
	} else {
		recent = append(recent, &mygo.MenuItem{Label: a.t("media.noRecent"), Disabled: true})
	}
	file := []*mygo.MenuItem{
		action("media.openFiles", "CmdOrCtrl+O", func() { a.choose(false, false) }),
		action("media.openFolder", "CmdOrCtrl+Shift+O", func() { a.choose(true, false) }),
		{Label: a.t("media.recentFiles"), Submenu: recent},
		mygo.Separator(),
		action("media.addFiles", "CmdOrCtrl+Shift+A", func() { a.choose(false, true) }),
		action("media.addFolder", "", func() { a.choose(true, true) }),
		action("playlist.scanDlna", "CmdOrCtrl+Shift+D", a.scanDLNA),
		mygo.Separator(),
		action("media.closeVideo", "CmdOrCtrl+Shift+W", a.closeVideo),
		{Role: mygo.RoleClose},
	}
	var items []*mygo.MenuItem
	if runtime.GOOS == "darwin" {
		mygo.App.Dock.SetMenu(mygo.NewMenu([]*mygo.MenuItem{
			action("media.openFiles", "", func() {
				a.showWindow()
				a.choose(false, false)
			}),
			action("media.openFolder", "", func() {
				a.showWindow()
				a.choose(true, false)
			}),
		}))
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
	mygo.App.SetMenu(mygo.NewMenu(items))
}
