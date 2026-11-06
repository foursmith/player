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
	if a.preferences.QuitOnWindowClose {
		mygo.App.Quit()
		return
	}
	a.cancelImports()
	a.singleClickAt = time.Time{}
	a.scrubbing, a.dragging = false, false
	a.player.Pause()
	a.persist()
	if a.pipWindow != nil {
		a.pipWindow.Close()
	}
	if a.settingsWindow != nil {
		a.settingsWindow.Close()
	}
	a.window.Hide()
}
