package main

import (
	"time"

	"github.com/egoist/mygo"
)

func (a *application) showWindow() {
	if a.window.IsMinimized() {
		a.window.Restore()
	}
	a.window.Show()
}

func (a *application) closePlayerWindow(event *mygo.CloseEvent) {
	// Explicit app quit closes the window after OnBeforeQuit has cleaned up.
	if a.shutdown {
		return
	}
	event.PreventDefault()
	a.cancelImports()
	a.singleClickAt = time.Time{}
	a.scrubbing, a.dragging = false, false
	if !a.pipActive() {
		a.player.Pause()
	}
	a.persist()
	a.window.Hide()
	a.quitIfAllWindowsClosed()
}

func (a *application) quitIfAllWindowsClosed() {
	if a.shutdown || !a.preferences.QuitOnWindowClose || a.catalogWindow != nil || a.settingsWindow != nil || a.pipActive() {
		return
	}
	if a.window != nil && !a.window.IsVisible() {
		mygo.App.Quit()
	}
}
