package main

import (
	"testing"

	"github.com/foursmith/player/internal/media"
	"github.com/foursmith/player/internal/projection"
	"github.com/foursmith/player/internal/subtitle"
	"github.com/foursmith/player/internal/video"
)

type testSystemPip struct {
	params         video.Parameters
	caption        string
	playing        bool
	closed         bool
	frames, closes int
}

func (p *testSystemPip) Size() (float64, float64) { return 360, 640 }
func (p *testSystemPip) Closed() bool             { return p.closed }
func (p *testSystemPip) Close()                   { p.closes++ }
func (p *testSystemPip) Present(_ *media.Frame, params video.Parameters, caption string, playing bool) error {
	p.params, p.caption, p.playing = params, caption, playing
	p.frames++
	return nil
}

func TestSystemPipRendersWithoutMainWindow(t *testing.T) {
	player := media.NewPlayer(nil)
	defer player.Close()
	player.Open(media.Source{URL: playbackRenderFixture(t)})
	waitRenderState(t, player, func(s media.State) bool { return s.FrameID != 0 })
	player.Pause()
	waitRenderState(t, player, func(s media.State) bool { return !s.Playing })
	surface := &testSystemPip{}
	p := &systemPipPresentation{surface: surface, done: make(chan struct{})}
	a := &application{player: player, systemPip: p, selected: -1, preferences: defaultPreferences(), mode: projection.SBS180, view: projection.DefaultView()}
	a.preferences.SplitCount = 3
	a.cues = []subtitle.Cue{{Start: 0, End: 10, Text: "PiP subtitle"}}
	a.presentSystemPip(p)
	if surface.frames != 1 || surface.params.SplitCount != 1 || surface.caption != "PiP subtitle" || surface.playing {
		t.Fatalf("paused native presentation: %+v", surface)
	}
	a.view.Yaw = 25
	a.preferences.SubtitlesEnabled = false
	a.presentSystemPip(p)
	if surface.params.View.Yaw != 25 || surface.caption != "" {
		t.Fatalf("paused camera/subtitle update: %+v", surface)
	}
	a.closePip()
	if surface.closes != 1 || p.restore {
		t.Fatal("explicit close did not dismiss native PiP")
	}
	surface.closed, a.shutdown = true, true
	a.presentSystemPip(p)
	if a.pipActive() {
		t.Fatal("native presentation remained active after close")
	}
	select {
	case <-p.done:
	default:
		t.Fatal("render ticker was not stopped")
	}
}
