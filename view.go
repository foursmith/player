package main

import (
	"fmt"
	"math"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/foursmith/player/internal/media"
	"github.com/foursmith/player/internal/projection"
	"github.com/foursmith/player/internal/subtitle"
	"github.com/foursmith/player/internal/video"
)

func (a *application) render(c *ui.Context) {
	transparentTheme(c)
	a.width, a.height = c.Size()
	state := a.player.Snapshot()
	if a.exporting {
		state.Position = a.exportPosition
	}
	if state.Generation != a.lastGeneration {
		a.lastGeneration = state.Generation
		a.tracker.Reset()
		a.endedGeneration = 0
	}
	if state.Ended && !a.exporting && a.endedGeneration != state.Generation {
		a.endedGeneration = state.Generation
		a.next(true)
	}
	if a.loopA != nil && a.loopB != nil && state.Position >= *a.loopB && !a.exporting {
		a.player.Seek(*a.loopA)
	}
	if !a.singleClickAt.IsZero() {
		if !c.Now().Before(a.singleClickAt) {
			a.singleClickAt = time.Time{}
			a.player.Toggle()
			a.activity(mouseHideDelay)
		} else {
			c.After(a.singleClickAt.Sub(c.Now()))
		}
	}
	if a.selected >= 0 && time.Since(a.lastSaved) > 5*time.Second {
		a.persist()
	}
	held := a.controlsHeld
	a.updateTracking(c, state)
	a.controlsHeld = false
	visible := state.URL == "" || state.Loading || a.settingsOpen || a.adjustmentsOpen || a.projectionOpen || held || c.Now().Before(a.controlsUntil)
	a.controlsVisible = visible
	if visible && !a.controlsUntil.IsZero() {
		remaining := a.controlsUntil.Sub(c.Now())
		if remaining > 0 {
			c.After(remaining)
		}
	}
	ui.Box(c.Key("player")).Fill().Children(func() {
		if a.pipWindow == nil {
			a.renderStage(c, false)
		} else {
			ui.Box(c).Fill().Background(ui.RGB(0, 0, 0)).Center().Children(func() { ui.Text(c, a.t("controls.playingInPip")).TextColor(white(.5)) })
		}
		if state.URL == "" {
			a.empty(c)
		}
		if !a.window.IsFullScreen() {
			bar := c.TitleBar()
			ui.Box(c.Key("window-drag")).Absolute().Top(0).Left(0).Right(0).Height(max(bar.Height, 32)).DragWindow()
		}
		if visible {
			a.topControls(c)
			if len(a.entries) > 0 || len(a.folders) > 0 {
				a.playlist(c, state)
			}
			if state.URL != "" {
				a.controls(c, state)
				a.controlPopovers(c)
			}
		}
		if a.debugOpen && state.URL != "" {
			a.debug(c, state)
		}
		if a.notice != "" || state.Error != "" {
			message := a.notice
			if state.Error != "" {
				message = state.Error
			}
			panel(c, 12).Absolute().Attach(ui.AnchorTop, ui.AnchorTop).Top(70).MaxWidth(a.width - 40).Padding(12).Children(func() {
				ui.Row(c).Gap(8).Children(func() {
					ui.Text(c, message).FontSize(11).TextColor(ui.Hex("#fecaca")).Wrap().MaxWidth(a.width - 130)
					a.iconButton(c, "x", a.t("settings.close"), 24, false, func() { a.notice = "" })
				})
			})
		}
		if a.settingsOpen {
			a.settings(c)
		}
	})
	a.keyboard(c)
}

const (
	topContentMargin  float32 = 16
	topControlPadding float32 = 8
	topControlSize    float32 = 32
)

func topContentInset(c *ui.Context) float32 {
	// Reserve the full system caption band, including Windows' wider
	// controls. App controls sit below it and keep the same edge insets.
	return max(topContentMargin, c.TitleBar().Height)
}

func (a *application) topControls(c *ui.Context) {
	inset := topContentMargin + topControlPadding
	ui.Row(c.Key("top-actions")).Absolute().Top(topContentInset(c) + topControlPadding).Left(inset).Right(inset).Height(topControlSize).Gap(topControlPadding).AlignItems(ui.Center).PassThrough().Children(func() {
		if !a.playlistExpanded && (len(a.entries) > 0 || len(a.folders) > 0) {
			a.iconButton(c, "playlist", a.t("playlist.open"), topControlSize, false, func() { a.playlistExpanded = true })
		}
		ui.Spacer(c).Grow(1)
		if a.manuallyPaused || c.Now().Before(a.manualUntil) {
			a.iconButton(c, "scan-face", a.t("debug.resumeCentering"), topControlSize, false, func() { a.manuallyPaused = false; a.manualUntil = time.Time{}; a.tracker.Reset() })
		}
		a.iconButton(c, "settings", a.t("common.settings"), topControlSize, false, func() { a.settingsOpen = true })
	})
}

func (a *application) renderStage(c *ui.Context, pip bool) {
	if pip {
		transparentTheme(c)
	}
	width, height := c.Size()
	state := a.player.Snapshot()
	if a.exporting {
		state.Position = a.exportPosition
	}
	stage := ui.Box(c.Key("stage")).Fill().Focusable().HandleInput(func(event ui.InputEvent) bool { return a.stageInput(event, float64(width/height), pip) })
	if !pip && !a.controlsVisible {
		stage.Cursor(ui.CursorNone)
	}
	index := 0
	if pip {
		index = 1
	}
	presentation := &a.presentation[index]
	params := a.params(pip)
	interaction := params != presentation.params || width != presentation.width || height != presentation.height || state.Generation != presentation.generation
	render, next := video.ScheduleFrame(trackingNow(), float64([]int{24, 30, 60}[a.preferences.FrameRateID-1]), presentation.next, interaction)
	if state.Playing && !render {
		c.After(time.Duration(max(0, next-trackingNow()) * float64(time.Millisecond)))
	}
	stage.Draw(func(p *ui.Painter, r ui.Rect) {
		frame := a.player.Frame()
		if a.exportTask != nil {
			if preview := a.exportTask.preview.Frame(); preview != nil {
				frame.Close()
				frame = preview
			}
		}
		if frame == nil {
			return
		}
		defer frame.Close()
		surface := a.surface
		if pip {
			surface = a.pipSurface
		}
		if surface == nil {
			return
		}
		if render {
			window := a.window
			if pip {
				window = a.pipWindow
			}
			scale := video.PixelRatio(a.preferences.QualityID, mygo.Screen.DisplayMatching(window.Bounds()).ScaleFactor)
			if interaction || frame.ID() != presentation.frame || scale != presentation.scale || presentation.err != "" {
				if err := surface.Present(frame, params, float64(r.W), float64(r.H), scale); err != nil {
					message := err.Error()
					if presentation.err != message {
						presentation.err = message
						a.window.Update(func() { a.notice = message })
					}
				} else {
					presentation.err = ""
					presentation.frame = frame.ID()
					presentation.scale = scale
				}
			}
			presentation.next = next
			presentation.params = params
			presentation.width = width
			presentation.height = height
			presentation.generation = state.Generation
		}
		video.Paint(surface, p, r)
	})
	stage.Children(func() {
		if a.preferences.SubtitlesEnabled {
			if text := subtitle.Active(a.cues, state.Position); text != "" {
				ui.Box(c.Key("subtitles")).Absolute().Left(width * .06).Right(width * .06).Bottom(height * .14).Center().IgnoreInput(true).Children(func() {
					ui.Text(c, text).FontSize(min(28, max(16, width*.022))).FontWeight(600).TextAlign(ui.Center).TextColor(ui.RGB(255, 255, 255)).Wrap().MaxWidth(min(width*.86, 1152)).Padding(6, 14)
				})
			}
		}
	})
	if pip && state.Playing {
		c.After(time.Second / 60)
	}
}
func (a *application) stageInput(event ui.InputEvent, aspect float64, pip bool) bool {
	point := ui.Point{X: event.X, Y: event.Y}
	switch event.Kind {
	case ui.InputPointerDown:
		if a.projectionOpen || a.adjustmentsOpen {
			a.projectionOpen = false
			a.adjustmentsOpen = false
			return true
		}
		a.singleClickAt = time.Time{}
		if event.Button == 1 {
			a.player.Toggle()
			a.controlsUntil = time.Time{}
			return true
		}
		if event.Button != 0 {
			return false
		}
		a.dragging = true
		a.pressPointer = point
		a.lastPointer = point
		a.dragDistance = 0
		if event.Clicks > 1 {
			if !pip {
				a.window.ToggleFullScreen()
			}
			a.dragging = false
		}
		return true
	case ui.InputPointerMove:
		dx, dy := float64(point.X-a.lastPointer.X), float64(point.Y-a.lastPointer.Y)
		if a.dragging {
			a.dragDistance = math.Hypot(float64(point.X-a.pressPointer.X), float64(point.Y-a.pressPointer.Y))
			next := a.view
			next.Yaw += dx * .08
			next.Pitch = projection.Clamp(next.Pitch+dy*.08, -85, 85)
			a.setView(next, aspect)
		}
		if math.Hypot(dx, dy) >= 3 {
			a.activity(mouseHideDelay)
		}
		a.lastPointer = point
		return true
	case ui.InputPointerUp:
		if event.Button == 0 && a.dragging {
			a.dragging = false
			if a.dragDistance <= 8 && event.Clicks == 1 {
				a.singleClickAt = event.Time.Add(250 * time.Millisecond)
			}
		}
		return true
	case ui.InputScroll:
		speed := .0016
		if event.Mods&ui.Ctrl != 0 {
			speed = .01
		}
		a.zoom(math.Exp(-float64(event.DY)*speed), aspect)
		a.activity(mouseHideDelay)
		return true
	case ui.InputZoom:
		a.zoom(float64(event.Scale), aspect)
		a.activity(mouseHideDelay)
		return true
	}
	return false
}
func (a *application) setView(next projection.View, aspect float64) {
	if !a.debugOpen {
		next = projection.Constrain(a.mode, a.view, next, aspect)
	}
	if next == a.view {
		return
	}
	a.view = next
	a.manuallyPaused = !a.preferences.ResumeAfterMovement
	a.manualUntil = time.Now().Add(time.Second)
	a.tracker.Reset()
}
func (a *application) zoom(scale, aspect float64) {
	next := a.view
	next.Forward = projection.ZoomForward(next.Forward, scale, a.mode)
	a.setView(next, aspect)
}
func (a *application) keyboard(c *ui.Context) {
	if a.projectionOpen {
		for _, step := range []struct {
			key   ui.Key
			delta int
		}{{ui.KeyUp, -1}, {ui.KeyDown, 1}} {
			if c.Shortcut(0, step.key) {
				a.projectionFocused = (a.projectionFocused + step.delta + 7) % 7
				a.projectionItems[a.projectionFocused].Focus(c)
			}
		}
		if c.Shortcut(0, ui.KeyEnter) {
			a.setMode(projection.Mode(a.projectionFocused))
			a.projectionOpen = false
		}
	}
	if a.settingsOpen || a.adjustmentsOpen || a.projectionOpen {
		if c.Shortcut(0, ui.KeyEscape) {
			a.settingsOpen = false
			a.adjustmentsOpen = false
			a.projectionOpen = false
		}
		return
	}
	state := a.player.Snapshot()
	if state.URL == "" {
		return
	}
	register := func(mods ui.Modifiers, key ui.Key, action func()) {
		if a.timelineHandle.Focused(c) && (key == ui.KeyLeft || key == ui.KeyRight || key == ui.KeyUp || key == ui.KeyDown) {
			return
		}
		if c.Shortcut(mods, key) {
			action()
			a.activity(1500 * time.Millisecond)
		}
	}
	register(0, ui.KeySpace, a.player.Toggle)
	for _, mods := range []ui.Modifiers{0, ui.Shift} {
		amount := 10.0
		if mods == ui.Shift {
			amount = 60
		}
		register(mods, ui.KeyLeft, func() { a.player.Seek(state.Position - amount) })
		register(mods, ui.KeyRight, func() { a.player.Seek(state.Position + amount) })
	}
	register(0, ui.KeyUp, func() { a.setVolume(a.preferences.Volume + .05) })
	register(0, ui.KeyDown, func() { a.setVolume(a.preferences.Volume - .05) })
	for _, mods := range []ui.Modifiers{0, ui.Shift} {
		register(mods, ui.KeyM, a.mute)
		register(mods, ui.KeyF, a.window.ToggleFullScreen)
		register(mods, ui.KeyR, a.resetView)
	}
	for _, key := range []ui.Key{ui.KeyBracketLeft, ui.KeyMinus} {
		register(0, key, func() { a.zoom(1/1.1, float64(a.width/a.height)) })
	}
	for _, key := range []ui.Key{ui.KeyBracketRight, ui.KeyEqual} {
		register(0, key, func() { a.zoom(1.1, float64(a.width/a.height)) })
	}
	register(0, ui.KeyComma, func() { a.preferences.QualityID = max(0, a.preferences.QualityID-1); a.persist() })
	register(0, ui.KeyPeriod, func() { a.preferences.QualityID = min(3, a.preferences.QualityID+1); a.persist() })
	for i, key := range []ui.Key{ui.Key1, ui.Key2, ui.Key3, ui.Key4, ui.Key5, ui.Key6, ui.Key7} {
		register(0, key, func() { a.setMode(projection.Mode(i)) })
	}
}
func (a *application) controls(c *ui.Context, state media.State) {
	pad, inset, buttonSize, playSize, gap := float32(24), float32(16), float32(36), float32(48), float32(12)
	if a.width < 640 {
		pad, inset, buttonSize, playSize, gap = 12, 8, 44, 56, 8
	}
	panel := ui.Column(c.Key("controls")).Bind(&a.controlsPanel).Absolute().Left(pad).Right(pad).Bottom(pad).MaxWidth(1152).Margin(0, ui.Auto).Padding(inset).Gap(12).Transition(ui.ElementTransition{Duration: 300 * time.Millisecond, Enter: &ui.Motion{Y: 16}, Exit: &ui.Motion{Y: 16}})
	hold(a, panel)
	panel.Children(func() {
		ui.Row(c).FillWidth().Gap(gap).AlignItems(ui.Center).Children(func() {
			name, label := "play", "controls.play"
			if state.Playing {
				name, label = "pause", "controls.pause"
			}
			a.iconControl(c, name, a.t(label), iconStyle{playSize, 26, floatingIcon}, false, a.player.Toggle)
			if len(a.entries) > 1 {
				a.iconControl(c, "skip-forward", a.t("controls.nextVideo"), iconStyle{buttonSize, 18, floatingIcon}, false, func() { a.next(false) })
			}
			ui.Spacer(c).Grow(1)
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				if len(a.cues) > 0 {
					a.iconControl(c, "subtitles", a.t("controls.showSubtitles"), iconStyle{buttonSize, 18, floatingIcon}, a.preferences.SubtitlesEnabled, func() { a.preferences.SubtitlesEnabled = !a.preferences.SubtitlesEnabled; a.persist() })
				}
				a.iconControl(c, "projection-"+projection.IDs[a.mode], a.projectionLabel(int(a.mode)), iconStyle{buttonSize, 18, floatingIcon}, false, func() {
					a.projectionOpen = !a.projectionOpen
					a.projectionFocused = int(a.mode)
					a.adjustmentsOpen = false
				}).Bind(&a.projectionButton)
				a.iconControl(c, "sliders", a.t("controls.adjustVolumeSpeed"), iconStyle{buttonSize, 18, floatingIcon}, false, func() { a.adjustmentsOpen = !a.adjustmentsOpen; a.projectionOpen = false }).Bind(&a.adjustmentsButton)
				a.iconControl(c, "picture-in-picture", a.t("controls.enterPip"), iconStyle{buttonSize, 18, floatingIcon}, a.pipWindow != nil, a.togglePip)
				name = "corners-out"
				if a.window.IsFullScreen() {
					name = "corners-in"
				}
				a.iconControl(c, name, a.t("controls.enterFullscreen"), iconStyle{buttonSize, 18, floatingIcon}, a.window.IsFullScreen(), a.window.ToggleFullScreen)
			})
		})
		a.timeline(c, state)
	})
}

func (a *application) controlPopovers(c *ui.Context) {
	if a.adjustmentsOpen {
		a.adjustments(c)
	}
	if a.projectionOpen {
		anchor := a.projectionButton.Bounds(c)
		left := max(float32(12), min(a.width-220, anchor.X+anchor.W/2-104))
		panel := panel(c, 16).Absolute().Left(left).Bottom(a.height - anchor.Y + 10).Width(208).Height(288).Padding(6).Gap(4).Role(ui.RoleMenu).Label(a.t("projection.title"))
		hold(a, panel)
		panel.Children(func() {
			for i := range projection.Labels {
				label := a.projectionLabel(i)
				selected := a.mode == projection.Mode(i)
				color := white(.68)
				if selected {
					color = white(1)
				}
				button := ui.Button(c, "").Bind(&a.projectionItems[i]).FillWidth().Height(36).FontSize(12).Padding(0, 10).Background(ui.Transparent).Border(0, ui.Transparent).Radius(12).Label(label)
				button.Children(func() {
					ui.Row(c).Fill().Gap(12).AlignItems(ui.Center).Children(func() {
						ui.Icon(c, icons["projection-"+projection.IDs[i]]).Size(18, 18).TextColor(color)
						ui.Text(c, label).FontSize(12).FontWeight(500).TextColor(color).Grow(1).MinWidth(0).Wrap()
						if selected {
							ui.Icon(c, icons["check"]).Size(16, 16).TextColor(white(.72))
						}
					})
				})
				if selected {
					button.Background(white(.12))
					button.AutoFocus()
				}
				if button.Clicked() {
					a.setMode(projection.Mode(i))
					a.projectionOpen = false
					a.persist()
				}
			}
		})
	}
}
func (a *application) timeline(c *ui.Context, state media.State) {
	if state.Metadata.Duration <= 0 {
		return
	}
	ui.Column(c.Key("timeline")).Gap(8).Children(func() {
		track := ui.Box(c).Bind(&a.timelineHandle).Height(20).FillWidth().Focusable().Role(ui.RoleSlider).Range(0, state.Metadata.Duration, state.Position).Label(a.t("timeline.playbackPosition"))
		track.HandleInput(func(e ui.InputEvent) bool {
			bounds := a.timelineHandle.Bounds(c)
			at := projection.Clamp(float64(e.X/max(1, bounds.W)), 0, 1) * state.Metadata.Duration
			switch e.Kind {
			case ui.InputPointerDown:
				if e.Button == 0 {
					a.scrubbing = true
					a.scrubTime = at
					a.activity(mouseHideDelay)
					return true
				}
			case ui.InputPointerMove:
				if a.scrubbing {
					a.scrubTime = at
					a.player.Seek(at)
					return true
				}
			case ui.InputKeyDown:
				if e.Key == ui.KeyLeft {
					a.player.Seek(state.Position - 0.1)
					return true
				}
				if e.Key == ui.KeyRight {
					a.player.Seek(state.Position + 0.1)
					return true
				}
			case ui.InputPointerUp:
				if a.scrubbing {
					a.scrubbing = false
					a.player.Seek(at)
					a.activity(mouseHideDelay)
					return true
				}
			}
			return false
		})
		if a.scrubbing || track.Hovered() {
			a.controlsHeld = true
		}
		track.Draw(func(p *ui.Painter, r ui.Rect) {
			y := r.Y + r.H/2
			position := state.Position
			if a.scrubbing {
				position = a.scrubTime
			}
			paintGlassTrack(p, r, float32(position/state.Metadata.Duration))
			for _, marker := range []struct {
				value *float64
				color ui.Color
			}{{a.loopA, ui.Hex("#fcd34d")}, {a.loopB, ui.Hex("#bae6fd")}} {
				if marker.value != nil {
					x := r.X + r.W*float32(*marker.value/state.Metadata.Duration)
					p.Fill(ui.Rect{X: x - 1, Y: y - 7, W: 2, H: 14}, marker.color, 1)
				}
			}
		})
		ui.Row(c).FillWidth().Gap(6).Children(func() {
			smallText(c, formatTime(state.Position), .48)
			for i, label := range []string{"A", "B"} {
				text := label
				if i == 0 && a.loopA != nil {
					text += " " + formatTime(*a.loopA)
				}
				if i == 1 && a.loopB != nil {
					text += " " + formatTime(*a.loopB)
				}
				button := ui.Button(c, text).FontSize(9).Height(20).Padding(0, 6).Radius(6).Border(0, ui.Transparent).Background(white(.06))
				if i == 0 && a.loopA != nil {
					button.Background(ui.Hex("#fcd34d").Alpha(.14)).TextColor(ui.Hex("#fde68a"))
				}
				if i == 1 && a.loopB != nil {
					button.Background(ui.Hex("#7dd3fc").Alpha(.14)).TextColor(ui.Hex("#bae6fd"))
				}
				if i == 1 && a.loopA == nil {
					button.Disabled(true)
				}
				if button.Clicked() {
					point := state.Position
					if i == 0 {
						a.loopA = &point
						a.loopB = nil
					} else if a.loopA != nil && point > *a.loopA {
						a.loopB = &point
					}
				}
				if i == 0 {
					if a.loopA != nil {
						a.iconButton(c, "x", a.t("timeline.clearLoop"), 20, false, func() { a.loopA = nil; a.loopB = nil; a.exportMessage = "" })
					} else {
						ui.Box(c).Width(8).Height(1).Background(white(.15))
					}
				}
			}
			if a.loopB != nil {
				for _, format := range []string{"webm", "mp4", "motion-photo"} {
					key := format
					if key == "motion-photo" {
						key = "motionPhoto"
					}
					label := a.t("export.formats." + key)
					if a.exporting && a.exportFormat == format {
						label = fmt.Sprintf("%d%%", a.exportProgress)
					}
					button := ui.Button(c, label).Description(a.exportMessage).FontSize(9).Height(20).Padding(0, 6).Radius(6).Background(white(.08)).Border(0, ui.Transparent).Disabled(a.exporting || *a.loopB-*a.loopA > 60)
					if button.Clicked() {
						a.exportLoop(format)
					}
				}
			}
			ui.Spacer(c).Grow(1)
			smallText(c, formatTime(state.Metadata.Duration), .6)
		})
	})
}
func (a *application) adjustments(c *ui.Context) {
	anchor, controls := a.adjustmentsButton.Bounds(c), a.controlsPanel.Bounds(c)
	panel := panel(c, 16).Absolute().Right(a.width - controls.X - controls.W).Bottom(a.height - anchor.Y + 10).Width(min(296, a.width-24)).Padding(8).Gap(2)
	hold(a, panel)
	panel.Children(func() {
		for i, entry := range []struct {
			label         string
			value, lo, hi float64
		}{{"controls.volume", a.preferences.Volume, 0, 1}, {"controls.speed", a.preferences.PlaybackRate, .5, 2}} {
			ui.Row(c.Key(i)).FillWidth().Gap(8).Padding(8, 10).Children(func() {
				smallText(c, a.t(entry.label), .62).Width(52)
				value := entry.value
				slider := ui.SliderBase(c, &value, entry.lo, entry.hi).Step(map[bool]float64{true: .05, false: .01}[i == 1]).Grow(1).Height(22).Label(a.t(entry.label))
				slider.Draw(func(p *ui.Painter, r ui.Rect) { paintGlassTrack(p, r, float32((value-entry.lo)/(entry.hi-entry.lo))) })
				if slider.Changed() {
					if i == 0 {
						a.setVolume(value)
					} else {
						a.setRate(math.Round(value*20) / 20)
					}
				}
				label := fmt.Sprintf("%d%%", int(value*100))
				if i == 1 {
					label = fmt.Sprintf("%.2g×", value)
				}
				smallText(c, label, .46).Width(34).TextAlign(ui.End)
				icon := "volume-2"
				if i == 1 {
					icon = "rotate-ccw"
				} else if value == 0 {
					icon = "volume-x"
				} else if value < .5 {
					icon = "volume-1"
				}
				a.iconControl(c, icon, a.t(entry.label), iconStyle{24, 14, bareIcon}, false, func() {
					if i == 0 {
						a.mute()
					} else {
						a.setRate(1)
					}
				})
			})
		}
	})
}
func (a *application) playlist(c *ui.Context, state media.State) {
	if !a.playlistExpanded {
		return
	}
	top := topContentInset(c)
	panel := panel(c.Key("playlist"), 20).Absolute().Top(top).Left(topContentMargin).Width(min(288, a.width-2*topContentMargin)).MaxHeight(max(120, a.height-top-200))
	hold(a, panel)
	panel.Children(func() {
		ui.Row(c).FillWidth().Height(topControlSize + 2*topControlPadding).Padding(topControlPadding).Gap(topControlPadding).Children(func() {
			a.iconButton(c, "playlist", a.t("playlist.close"), topControlSize, true, func() { a.playlistExpanded = false })
			ui.Text(c, a.t("playlist.title")).FontSize(14).FontWeight(600).Grow(1)
			name := "play-once"
			if a.preferences.RepeatMode == "file" {
				name = "repeat-once"
			} else if a.preferences.RepeatMode == "folder" {
				name = "folder-repeat"
			}
			a.iconButton(c, name, a.preferences.RepeatMode, 32, false, func() {
				switch a.preferences.RepeatMode {
				case "off":
					a.preferences.RepeatMode = "folder"
				case "folder":
					a.preferences.RepeatMode = "file"
				default:
					a.preferences.RepeatMode = "off"
				}
				a.persist()
			})
			if a.serverStatus == "connected" {
				a.iconButton(c, "dlna-scan", a.t("playlist.scanDlna"), 32, a.scanning, func() {
					if !a.scanning {
						a.scanning = true
						a.loadServerLibrary(true)
					}
				})
			}
			a.iconControl(c, "trash", a.t("playlist.clear"), iconStyle{32, 14, floatingIcon}, false, a.clearLocalPlaylist)
		})
		rows := a.visiblePlaylist()
		a.playlistList.Key = func(i int) any { return rows[i].node.ID }
		a.playlistList.Label = func(i int) string { return rows[i].node.Name }
		ui.List(c.Key("playlist-rows"), &a.playlistList, len(rows), func(i int) {
			a.playlistRow(c, rows[i])
		}).FillWidth().Height(min(float32(len(rows))*36, max(40, a.height-top-306))).Padding(0, 8)
		ui.Box(c).Padding(8).Children(func() { a.pickers(c, min(272, a.width-40)) })
	})
}

type presentationState struct {
	params        video.Parameters
	width, height float32
	generation    uint64
	frame         uint64
	scale         float64
	next          float64
	err           string
}

func (a *application) resetView() {
	a.view = projection.DefaultView()
	a.manuallyPaused = !a.preferences.ResumeAfterMovement
	a.manualUntil = time.Now().Add(time.Second)
	a.tracker.Reset()
}
func (a *application) setMode(mode projection.Mode) {
	a.mode = mode
	a.view = projection.DefaultView()
	a.tracker.Reset()
	a.manuallyPaused = false
	a.manualUntil = time.Time{}
	a.persist()
}
