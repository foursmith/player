package main

import (
	"time"

	"github.com/egoist/mygo"
	"github.com/foursmith/player/internal/projection"
	"github.com/foursmith/player/internal/video"
)

func (a *application) openPip() {
	state := a.player.Snapshot()
	if state.FrameID == 0 {
		return
	}
	width, height := float64(360), float64(640)
	if a.mode == projection.Flat {
		width, height = 480, 270
		if state.Metadata.Width > 0 && state.Metadata.Height > 0 {
			height = width * float64(state.Metadata.Height) / float64(state.Metadata.Width)
		}
	}
	p := &systemPipPresentation{done: make(chan struct{}), restore: true}
	surface, err := video.NewPictureInPicture(a.window.NativeHandle(), width, height, video.PictureInPictureActions{
		Play:  a.player.Play,
		Pause: a.player.Pause,
	})
	if err != nil {
		a.notice = err.Error()
		return
	}
	p.surface = surface
	a.systemPip = p
	a.presentSystemPip(p)
	// The system window remains live even when the player window is hidden or
	// minimized. Coalesce updates so a busy UI thread never accumulates frames.
	go func() {
		ticker := time.NewTicker(time.Second / 60)
		defer ticker.Stop()
		for {
			select {
			case <-p.done:
				return
			case <-ticker.C:
				if p.queued.CompareAndSwap(false, true) {
					mygo.RunOnMain(func() {
						defer p.queued.Store(false)
						if a.systemPip == p {
							a.presentSystemPip(p)
						}
					})
				}
			}
		}
	}()
}
