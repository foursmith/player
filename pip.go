package main

import (
	"sync/atomic"

	"github.com/foursmith/player/internal/video"
)

type systemPipPresentation struct {
	surface video.PictureInPicture
	done    chan struct{}
	restore bool
	queued  atomic.Bool
}

func (a *application) pipActive() bool { return a.pipWindow != nil || a.systemPip != nil }

func (a *application) closePip() {
	if a.systemPip != nil {
		a.systemPip.restore = false
		a.systemPip.surface.Close()
	}
	if a.pipWindow != nil {
		a.pipWindow.Close()
	}
}

func (a *application) presentSystemPip(p *systemPipPresentation) {
	if p.surface.Closed() {
		close(p.done)
		p.surface.Close()
		a.systemPip = nil
		a.presentation[0] = presentationState{}
		if !a.shutdown {
			if p.restore {
				a.showWindow()
			}
			a.window.Invalidate()
			a.quitIfAllWindowsClosed()
		}
		return
	}
	if a.shutdown {
		return
	}
	state := a.player.Snapshot()
	a.updatePlaybackPresentation(state)
	if state.URL == "" {
		p.surface.Close()
		return
	}
	if state.Playing && !state.Loading {
		a.updateTracking(state, trackingNow())
		a.loopPlayback(state)
	}
	frame := a.player.Frame()
	if frame == nil {
		return
	}
	defer frame.Close()
	if err := p.surface.Present(frame, a.params(true), a.subtitleAt(state.Position), state.Playing); err != nil {
		a.notice = err.Error()
		a.window.Invalidate()
		p.surface.Close()
	}
}
