package main

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/foursmith/player/internal/media"
	"github.com/foursmith/player/internal/projection"
)

func TestStageDragAndCancellationNeverBecomeClicks(t *testing.T) {
	for _, test := range []struct {
		name     string
		moves    []ui.Point
		up       ui.Point
		canceled bool
	}{
		{name: "canceled", up: ui.Point{X: 100, Y: 100}, canceled: true},
		{name: "returns to start", moves: []ui.Point{{X: 140, Y: 100}, {X: 100, Y: 100}}, up: ui.Point{X: 100, Y: 100}},
		{name: "release beyond threshold", up: ui.Point{X: 140, Y: 100}},
	} {
		t.Run(test.name, func(t *testing.T) {
			a := modeTestApplication(t)
			a.width, a.height = 640, 480
			at := time.Now()
			a.stageInput(ui.InputEvent{Kind: ui.InputPointerDown, X: 100, Y: 100, Button: 0, Clicks: 1, Time: at}, 1.5, false)
			for _, p := range test.moves {
				a.stageInput(ui.InputEvent{Kind: ui.InputPointerMove, X: p.X, Y: p.Y, Time: at}, 1.5, false)
			}
			a.stageInput(ui.InputEvent{Kind: ui.InputPointerUp, X: test.up.X, Y: test.up.Y, Button: 0, Clicks: 1, Canceled: test.canceled, Time: at}, 1.5, false)
			if a.dragging || !a.singleClickAt.IsZero() {
				t.Fatal("drag or canceled press became a playback click")
			}
		})
	}
}

func TestStageClickJitterDoesNotPanVideo(t *testing.T) {
	a := modeTestApplication(t)
	a.mode = projection.SBS180
	a.view = projection.DefaultView()
	before := a.view
	at := time.Now()
	a.stageInput(ui.InputEvent{Kind: ui.InputPointerDown, X: 100, Y: 100, Button: 0, Clicks: 1, Time: at}, 1.5, false)
	a.stageInput(ui.InputEvent{Kind: ui.InputPointerMove, X: 102, Y: 101, Time: at}, 1.5, false)
	a.stageInput(ui.InputEvent{Kind: ui.InputPointerUp, X: 102, Y: 101, Button: 0, Clicks: 1, Time: at}, 1.5, false)
	if a.view != before || a.singleClickAt.IsZero() {
		t.Fatal("click jitter moved the video or lost the click")
	}
}

func TestClosingPopoverConsumesClickAndRenewsControls(t *testing.T) {
	a := modeTestApplication(t)
	a.projectionOpen, a.adjustmentsOpen, a.seekUI.chaptersOpen = true, true, true
	at := time.Now()
	a.stageInput(ui.InputEvent{Kind: ui.InputPointerDown, Button: 0, Clicks: 1, Time: at}, 1.5, false)
	a.stageInput(ui.InputEvent{Kind: ui.InputPointerUp, Button: 0, Clicks: 1, Time: at}, 1.5, false)
	if a.projectionOpen || a.adjustmentsOpen || a.seekUI.chaptersOpen || a.dragging || !a.singleClickAt.IsZero() {
		t.Fatal("dismissal left a popover open or queued a playback click")
	}
	if !a.controlsUntil.Equal(at.Add(mouseHideDelay)) {
		t.Fatal("dismissing a popover did not grant a new idle interval")
	}
}

func TestPipClickRunsWithoutMainWindowRendering(t *testing.T) {
	file := playbackRenderFixture(t)
	for _, double := range []bool{false, true} {
		name := "single"
		if double {
			name = "double"
		}
		t.Run(name, func(t *testing.T) {
			a := modeTestApplication(t)
			a.player.Open(media.Source{URL: file})
			waitRenderState(t, a.player, func(s media.State) bool { return s.Playing && !s.Loading && s.FrameID != 0 })
			view := ui.NewTester(func(c *ui.Context) { a.renderStage(c, true) }, 640, 480)
			view.ClickAt(320, 240)
			if double {
				view.Advance(80 * time.Millisecond)
				view.ClickAt(320, 240)
			}
			view.Advance(300 * time.Millisecond)
			if double {
				if !a.player.Snapshot().Playing || !a.singleClickAt.IsZero() {
					t.Fatal("double click paused PiP playback or left a pending click")
				}
			} else {
				waitRenderState(t, a.player, func(s media.State) bool { return !s.Playing })
				view.Advance(time.Second)
				if a.player.Snapshot().Playing || !a.singleClickAt.IsZero() {
					t.Fatal("single PiP click toggled playback more than once")
				}
			}
		})
	}
}

func TestLosingFocusCancelsPendingStageInteraction(t *testing.T) {
	a := modeTestApplication(t)
	a.singleClickAt = time.Now().Add(time.Second)
	a.dragging, a.scrubbing, a.seekUI.fine = true, true, true
	a.cancelStageInteraction()
	if !a.singleClickAt.IsZero() || a.dragging || a.scrubbing || a.seekUI.fine {
		t.Fatal("background window retained a pending playback interaction")
	}
}

func TestPipKeyboardRespectsBindingsWithoutFullscreeningMainWindow(t *testing.T) {
	a := modeTestApplication(t)
	a.preferences.Shortcuts = map[string]keyChord{"playPause": {Key: ui.KeyP}}
	a.player.Open(media.Source{URL: playbackRenderFixture(t)})
	waitRenderState(t, a.player, func(s media.State) bool { return s.Playing && !s.Loading && s.FrameID != 0 })
	view := ui.NewTester(func(c *ui.Context) { a.renderStage(c, true) }, 640, 480)
	view.Key(0, ui.KeyTab)
	view.Key(0, ui.KeySpace)
	if !a.player.Snapshot().Playing {
		t.Fatal("PiP ignored a remapped play/pause binding")
	}
	view.Key(0, ui.KeyP)
	waitRenderState(t, a.player, func(s media.State) bool { return !s.Playing })
	// No native main window exists in this fixture. A fullscreen action
	// leaking from PiP would try to use it.
	view.Key(0, ui.KeyF)
}
