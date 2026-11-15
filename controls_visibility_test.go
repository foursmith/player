package main

import (
	"fmt"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/foursmith/player/internal/media"
	"github.com/foursmith/player/internal/subtitle"
	"github.com/foursmith/player/library"
)

func TestPlayerCursorCoversStageAndWindowDragRegion(t *testing.T) {
	a := modeTestApplication(t)
	a.controlsVisible = true
	view := ui.NewTester(func(c *ui.Context) {
		a.playerContent(c).Children(func() {
			a.renderStage(c, false)
			ui.Box(c).Absolute().Top(0).Left(0).Right(0).Height(32).DragWindow()
		})
	}, 640, 480)
	for _, point := range []ui.Point{{X: 320, Y: 240}, {X: 320, Y: 16}} {
		view.Move(point.X, point.Y)
		if view.Cursor() == ui.CursorNone {
			t.Fatal("visible controls must keep the cursor visible")
		}
		a.controlsVisible = false
		view.Frame()
		if view.Cursor() != ui.CursorNone {
			t.Fatalf("stationary cursor stayed visible at %+v", point)
		}
		// Even a one-pixel movement must wake the controls, including over
		// the drag region, which does not send input to the video stage.
		view.Advance(time.Second)
		view.Move(point.X+1, point.Y)
		if want := view.Now().Add(mouseHideDelay); !a.controlsUntil.Equal(want) {
			t.Fatalf("small movement did not reset the idle deadline: got %v, want %v", a.controlsUntil, want)
		}
		a.controlsVisible = true
		view.Frame()
		if view.Cursor() == ui.CursorNone {
			t.Fatal("cursor did not return with controls")
		}
	}
}

func TestSlowStageMovementKeepsExtendingControlsDeadline(t *testing.T) {
	a := modeTestApplication(t)
	a.lastPointer = ui.Point{X: 100, Y: 100}
	at := time.Now()
	for step := 1; step <= 5; step++ {
		at = at.Add(time.Second)
		event := ui.InputEvent{Kind: ui.InputPointerMove, X: 100 + float32(step), Y: 100, Time: at}
		a.stageInput(event, 1.5, false)
		if !a.controlsUntil.Equal(at.Add(mouseHideDelay)) {
			t.Fatalf("one-pixel movement %d failed to extend the deadline", step)
		}
	}
}

func controlsTestView(t *testing.T, a *application, state media.State) *ui.Tester {
	t.Helper()
	view := ui.NewTester(func(c *ui.Context) {
		a.updateControlsVisibility(c, state)
		a.playerContent(c).Children(func() {
			a.renderStage(c, false)
			if a.controlsVisible {
				panel := ui.Box(c.Key("test-controls")).Absolute().Left(10).Bottom(10).Size(200, 50)
				hold(c, a, panel)
				panel.Children(func() {
					ui.Button(c, "Play").Bind(&a.playButton).Size(80, 40)
					ui.Button(c, "Fullscreen").Bind(&a.fullscreenButton).Size(80, 40)
				})
			}
		})
	}, 640, 480)
	a.showControls(view.Now(), mouseHideDelay)
	view.Frame()
	return view
}

func TestControlsIdleTimerAndMouseFocus(t *testing.T) {
	a := modeTestApplication(t)
	view := controlsTestView(t, a, media.State{URL: "video.mp4", Playing: true})
	if err := view.Click("Play"); err != nil {
		t.Fatal(err)
	}
	view.Move(320, 200)
	view.Advance(mouseHideDelay - time.Millisecond)
	if !a.controlsVisible || view.Cursor() == ui.CursorNone {
		t.Fatal("controls and cursor hid before the idle deadline")
	}
	view.Advance(2 * time.Millisecond)
	if a.controlsVisible || view.Cursor() != ui.CursorNone {
		t.Fatal("mouse focus pinned the controls or left the cursor visible")
	}
	view.Move(321, 200)
	if !a.controlsVisible || view.Cursor() == ui.CursorNone {
		t.Fatal("small movement did not reveal controls and cursor together")
	}
}

func TestHoveredControlsStayUntilPointerLeaves(t *testing.T) {
	a := modeTestApplication(t)
	view := controlsTestView(t, a, media.State{URL: "video.mp4", Playing: true})
	r, ok := view.Find("Play")
	if !ok {
		t.Fatal("missing playback control")
	}
	view.Move(r.X+r.W/2, r.Y+r.H/2)
	view.Advance(3 * mouseHideDelay)
	if !a.controlsVisible {
		t.Fatal("hovered controls disappeared")
	}
	view.Move(320, 200)
	view.Advance(mouseHideDelay + time.Millisecond)
	if a.controlsVisible {
		t.Fatal("controls did not hide after leaving them")
	}
}

func TestTabRevealsControlsAndKeepsKeyboardFocusVisible(t *testing.T) {
	for _, mods := range []ui.Modifiers{0, ui.Shift} {
		t.Run(keyChord{Key: ui.KeyTab, Mods: mods}.String(), func(t *testing.T) {
			a := modeTestApplication(t)
			view := controlsTestView(t, a, media.State{URL: "video.mp4", Playing: true})
			view.Key(0, ui.KeyTab) // Focus the video stage first.
			view.Move(320, 200)
			view.Advance(mouseHideDelay + time.Millisecond)
			if a.controlsVisible {
				t.Fatal("controls did not become idle")
			}
			view.Key(mods, ui.KeyTab)
			want := "Play"
			if mods != 0 {
				want = "Fullscreen"
			}
			if !a.controlsVisible || !view.Focused(want) {
				t.Fatalf("Tab did not reveal and focus %s", want)
			}
			view.Advance(3 * mouseHideDelay)
			if !a.controlsVisible || !view.Focused(want) {
				t.Fatal("controls disappeared during keyboard navigation")
			}
			view.Move(321, 200)
			view.Advance(mouseHideDelay + time.Millisecond)
			if a.controlsVisible {
				t.Fatal("keyboard focus kept controls pinned after returning to the mouse")
			}
		})
	}
}

func TestControlsRemainAvailableOutsideNormalPlayback(t *testing.T) {
	for _, test := range []struct {
		name  string
		state media.State
		setup func(*application)
	}{
		{name: "home"},
		{name: "paused", state: media.State{URL: "video.mp4"}},
		{name: "ended", state: media.State{URL: "video.mp4", Ended: true}},
		{name: "loading", state: media.State{URL: "video.mp4", Playing: true, Loading: true}},
		{name: "error", state: media.State{URL: "video.mp4", Playing: true, Error: "unavailable"}},
		{name: "dialog", setup: func(a *application) { a.dialogOpen = true }},
		{name: "projection", setup: func(a *application) { a.projectionOpen = true }},
		{name: "adjustments", setup: func(a *application) { a.adjustmentsOpen = true }},
		{name: "chapters", setup: func(a *application) { a.seekUI.chaptersOpen = true }},
		{name: "seek", setup: func(a *application) { a.scrubbing = true }},
		{name: "drag", setup: func(a *application) { a.dragging = true }},
		{name: "pip", setup: func(a *application) { a.pipWindow = &mygo.Window{} }},
		{name: "system-pip", setup: func(a *application) { a.systemPip = &systemPipPresentation{} }},
	} {
		t.Run(test.name, func(t *testing.T) {
			a := modeTestApplication(t)
			if test.setup != nil {
				test.setup(a)
				test.state = media.State{URL: "video.mp4", Playing: true}
			}
			view := controlsTestView(t, a, test.state)
			view.Move(320, 200)
			view.Advance(3 * mouseHideDelay)
			if !a.controlsVisible || view.Cursor() == ui.CursorNone {
				t.Fatal("controls or cursor disappeared while needed")
			}
		})
	}
}

func TestInactivePlayerNeverHidesCursor(t *testing.T) {
	a := modeTestApplication(t)
	view := controlsTestView(t, a, media.State{URL: "video.mp4", Playing: true})
	view.Move(320, 200)
	view.Advance(mouseHideDelay + time.Millisecond)
	view.SetWindowActive(false)
	if view.Cursor() == ui.CursorNone {
		t.Fatal("inactive player hid the cursor")
	}
}

func TestPlaybackControlsFitMinimumWindowAcrossLocales(t *testing.T) {
	if err := loadAssets(); err != nil {
		t.Fatal(err)
	}
	for _, locale := range []string{"en", "zh-CN", "ja"} {
		for _, width := range []int{360, 420, 480, 640, 1200} {
			t.Run(fmt.Sprintf("%s-%d", locale, width), func(t *testing.T) {
				a := modeTestApplication(t)
				a.preferences.Locale = locale
				a.entries = append(a.entries, library.Entry{Name: "next.mp4"})
				a.cues = []subtitle.Cue{{Start: 0, End: 10, Text: "subtitle"}}
				state := media.State{URL: "video.mp4", Playing: true, Metadata: media.Metadata{Duration: 180}}
				view := ui.NewTester(func(c *ui.Context) {
					a.width, a.height = c.Size()
					ui.Box(c).Fill().Background(ui.RGB(0, 0, 0)).Children(func() { a.controls(c, state) })
				}, width, 320)
				view.Advance(300 * time.Millisecond)
				writeUISnapshot(t, fmt.Sprintf("playback-%s-%d.png", locale, width), view)
				for _, label := range []string{"controls.pause", "controls.nextVideo", "controls.hideSubtitles", "controls.adjustVolumeSpeed", "controls.enterPip", "controls.enterFullscreen", "timeline.playbackPosition"} {
					r, ok := view.Find(a.t(label))
					if !ok || r.X < 0 || r.Y < 0 || r.X+r.W > float32(width) || r.Y+r.H > 320 {
						t.Fatalf("%s outside %dx320 window: %+v", label, width, r)
					}
				}
			})
		}
	}
}

func TestTopActionsHoldControlsWithoutPinningEmptySpace(t *testing.T) {
	if err := loadAssets(); err != nil {
		t.Fatal(err)
	}
	a := modeTestApplication(t)
	view := ui.NewTester(func(c *ui.Context) {
		a.width, a.height = c.Size()
		a.updateControlsVisibility(c, media.State{URL: "video.mp4", Playing: true})
		a.playerContent(c).Children(func() {
			a.renderStage(c, false)
			if a.controlsVisible {
				a.topControls(c)
			}
		})
	}, 640, 480)
	a.showControls(view.Now(), mouseHideDelay)
	view.Frame()
	r, ok := view.Find(a.t("common.settings"))
	if !ok {
		t.Fatal("missing settings action")
	}
	view.Move(r.X+r.W/2, r.Y+r.H/2)
	view.Advance(3 * mouseHideDelay)
	if !a.controlsVisible {
		t.Fatal("top actions disappeared under the pointer")
	}
	view.Move(320, r.Y+r.H/2)
	view.Advance(mouseHideDelay + time.Millisecond)
	if a.controlsVisible {
		t.Fatal("empty top-action space kept controls visible")
	}
}

func TestPlaybackPopoversAreMutuallyExclusive(t *testing.T) {
	if err := loadAssets(); err != nil {
		t.Fatal(err)
	}
	a := modeTestApplication(t)
	state := media.State{URL: "video.mp4", Metadata: media.Metadata{Duration: 180, Chapters: []media.Chapter{{Start: 0, Title: "Opening"}}}}
	view := ui.NewTester(func(c *ui.Context) {
		a.width, a.height = c.Size()
		ui.Box(c).Fill().Children(func() { a.controls(c, state) })
	}, 640, 480)
	for _, label := range []string{a.t("timeline.chapters"), a.t("controls.adjustVolumeSpeed"), a.projectionLabel(int(a.mode)), a.t("timeline.chapters")} {
		if err := view.Click(label); err != nil {
			t.Fatal(err)
		}
		open := 0
		for _, flag := range []bool{a.seekUI.chaptersOpen, a.adjustmentsOpen, a.projectionOpen} {
			if flag {
				open++
			}
		}
		if open != 1 {
			t.Fatalf("%s left %d popovers open", label, open)
		}
	}
}
