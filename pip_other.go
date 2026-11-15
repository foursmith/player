//go:build !darwin

package main

import (
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/foursmith/player/internal/projection"
	"github.com/foursmith/player/internal/video"
)

func (a *application) openPip() {
	a.presentation[1] = presentationState{}
	width, height := 360, 640
	if a.mode == projection.Flat {
		metadata := a.player.Snapshot().Metadata
		width, height = 480, 270
		if metadata.Width > 0 && metadata.Height > 0 {
			if metadata.Width >= metadata.Height {
				height = max(180, int(480*float64(metadata.Height)/float64(metadata.Width)))
			} else {
				width, height = max(240, int(480*float64(metadata.Width)/float64(metadata.Height))), 480
			}
		}
	}
	a.pipWindow = mygo.NewWindow(mygo.WindowOptions{Title: appInfo.Name, Width: width, Height: height, MinWidth: 240, MinHeight: 180, AlwaysOnTop: true, BackgroundColor: "#000000", Transparent: video.TransparentWindow, Content: ui.View(func(c *ui.Context) { a.renderStage(c, true) })})
	a.pipWindow.SetWindowButtonsTheme(mygo.ThemeDark)
	a.pipWindowRef.Store(a.pipWindow)
	a.pipWindow.OnBlur(a.cancelStageInteraction)
	a.refreshVideoOnReveal(a.pipWindow, 1)
	var err error
	a.pipSurface, err = video.New(a.pipWindow.NativeHandle())
	if err != nil {
		a.notice = err.Error()
	}
	a.pipWindow.OnClosed(func() {
		a.cancelStageInteraction()
		a.pipWindowRef.Store(nil)
		if a.pipSurface != nil {
			a.pipSurface.Close()
			a.pipSurface = nil
		}
		a.pipWindow = nil
		a.window.Invalidate()
	})
}
