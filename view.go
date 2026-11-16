package main

import (
	"fmt"
	"math"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/foursmith/player/internal/media"
	"github.com/foursmith/player/internal/projection"
	"github.com/foursmith/player/internal/subtitle"
	"github.com/foursmith/player/internal/video"
)

func (a *application) render(c *ui.Context) {
	a.width, a.height = c.Size()
	if !a.pipActive() {
		a.stageClick(c)
	}
	if !a.openFilesAt.IsZero() {
		if c.Now().Before(a.openFilesAt) {
			c.After(a.openFilesAt.Sub(c.Now()))
		} else {
			a.importPaths(a.pendingPaths, false)
		}
	}
	state := a.player.Snapshot()
	if state.URL == "" && a.surface != nil {
		a.surface.Clear()
	}
	a.usePlayerTheme(c, state.URL != "")
	a.refreshFailedPlayback(state)
	a.updatePlaybackPresentation(state)
	if a.selected >= 0 && state.Playing {
		c.After(max(time.Millisecond, 5*time.Second-time.Since(a.lastSaved)))
	}
	a.updateControlsVisibility(c, state)
	a.playerContent(c).Children(func() {
		if !a.pipActive() {
			a.renderStage(c, false)
		} else {
			if a.pipWindow != nil && a.pipNeedsView(state) {
				a.pipWindow.Invalidate()
			}
			ui.Box(c).Fill().Background(ui.RGB(0, 0, 0)).Center().Children(func() { ui.Text(c, a.t("controls.playingInPip")).TextColor(white(.5)) })
		}
		if state.URL == "" {
			a.home(c)
		}
		if !a.window.IsFullScreen() {
			bar := c.TitleBar()
			ui.Box(c.Key("window-drag")).Absolute().Top(0).Left(0).Right(0).Height(max(bar.Height, 32)).DragWindow()
		}
		if a.controlsVisible {
			a.topControls(c)
			if len(a.playlistModel()) > 0 {
				a.playlist(c, state)
			}
			if state.URL != "" {
				a.controls(c, state)
				a.timelinePreview(c, state)
				a.controlPopovers(c)
			}
		}
		if a.debugOpen && a.mode.IsVR() && state.URL != "" {
			a.debug(c, state)
		}
		if a.notice != "" || state.Error != "" {
			message := a.notice
			if state.Error != "" {
				message = state.Error
			}
			panel(c, 12).Absolute().Attach(ui.AnchorTop, ui.AnchorTop).Top(70).MaxWidth(a.width - 40).Padding(12).Children(func() {
				ui.Row(c).Gap(8).Children(func() {
					ui.Text(c, message).FontSize(11).TextColor(c.Theme().Danger).Wrap().MaxWidth(a.width - 130)
					a.iconButton(c, "x", a.t("settings.close"), 24, false, func() { a.notice = "" })
				})
			})
		}
	})
	a.keyboard(c)
}

const (
	topContentMargin   float32 = 16
	topControlPadding  float32 = 8
	topControlSize     float32 = 32
	stageDragThreshold         = 8
)

func (a *application) playerContent(c *ui.Context) ui.Element {
	content := ui.Box(c.Key("player")).Fill().HandleInput(a.pointerActivity).ObserveInput(func(event ui.InputEvent) {
		switch event.Kind {
		case ui.InputKeyDown:
			a.controlsKeyboard = true
			a.singleClickAt = time.Time{}
			a.showControls(inputTime(event), mouseHideDelay)
			if event.Key == ui.KeyTab && !a.controlsVisible {
				if event.Mods&ui.Shift != 0 {
					a.fullscreenButton.Focus(c)
				} else {
					a.playButton.Focus(c)
				}
			}
		case ui.InputPointerDown:
			a.controlsKeyboard = false
			a.singleClickAt = time.Time{}
			a.showControls(inputTime(event), mouseHideDelay)
		case ui.InputPointerUp:
			a.showControls(inputTime(event), mouseHideDelay)
		}
	})
	// Cover the whole player, including the window-drag region above the
	// stage. Sibling overlays do not inherit the stage's cursor.
	if !a.controlsVisible && c.WindowActive() {
		content.Cursor(ui.CursorNone)
	}
	return content
}

func (a *application) pointerActivity(event ui.InputEvent) bool {
	if event.Kind != ui.InputPointerMove {
		return false
	}
	a.controlsKeyboard = false
	a.showControls(inputTime(event), mouseHideDelay)
	return true
}

func inputTime(event ui.InputEvent) time.Time {
	if event.Time.IsZero() {
		return time.Now()
	}
	return event.Time
}

func (a *application) updateControlsVisibility(c *ui.Context, state media.State) {
	keep := state.URL == "" || !state.Playing || state.Loading || state.Error != "" || a.pipActive() || a.dialogOpen || a.adjustmentsOpen || a.projectionOpen || a.scrubbing || a.dragging || a.seekUI.chaptersOpen
	a.controlsVisible = keep || c.Now().Before(a.controlsUntil)
	if state.Playing && !state.Loading && (a.controlsVisible || a.debugOpen) {
		interval := 250 * time.Millisecond
		if a.debugOpen {
			interval = 100 * time.Millisecond
		}
		c.After(interval)
	}
	if !keep && c.Now().Before(a.controlsUntil) {
		c.After(a.controlsUntil.Sub(c.Now()))
	}
}

func topContentInset(c *ui.Context) float32 {
	// Reserve the full system caption band, including Windows' wider
	// controls. App controls sit below it and keep the same edge insets.
	return max(topContentMargin, c.TitleBar().Height)
}

func (a *application) topControls(c *ui.Context) {
	inset := topContentMargin + topControlPadding
	row := ui.Themed(c.Key("top-actions"), darkPlayerTheme).Row().Absolute().Top(topContentInset(c) + topControlPadding).Left(inset).Right(inset).Height(topControlSize).Gap(topControlPadding).AlignItems(ui.Center).PassThrough()
	hold(c, a, row)
	row.Children(func() {
		if !a.playlistExpanded && len(a.playlistModel()) > 0 {
			a.iconButton(c, "playlist", a.t("playlist.open"), topControlSize, false, func() { a.playlistExpanded = true })
		}
		ui.Spacer(c).Grow(1)
		if a.selected >= 0 {
			a.iconButton(c, "x", a.t("media.closeVideo"), topControlSize, false, a.closeVideo)
		}
		if a.mode.IsVR() && (a.manuallyPaused || c.Now().Before(a.manualUntil)) {
			a.iconButton(c, "scan-face", a.t("debug.resumeCentering"), topControlSize, false, func() { a.manuallyPaused = false; a.manualUntil = time.Time{}; a.tracker.Reset() })
		}
		onTop := a.window != nil && a.window.IsAlwaysOnTop()
		pinLabel := "controls.pinWindow"
		if onTop {
			pinLabel = "controls.unpinWindow"
		}
		a.iconButton(c, "pin", a.t(pinLabel), topControlSize, onTop, func() {
			a.window.SetAlwaysOnTop(!a.window.IsAlwaysOnTop())
		})
		a.iconButton(c, "settings", a.t("common.settings"), topControlSize, false, a.openSettings)
	})
}

func (a *application) renderStage(c *ui.Context, pip bool) {
	if pip {
		c.SetTheme(darkPlayerTheme)
		a.stageClick(c)
	}
	width, height := c.Size()
	state := a.player.Snapshot()
	stage := ui.Box(c.Key("stage")).Fill().Focusable().HandleInput(func(event ui.InputEvent) bool { return a.stageInput(event, float64(width/height), pip) })
	if !pip && !a.controlsVisible && c.WindowActive() {
		stage.Cursor(ui.CursorNone)
	}
	index := 0
	if pip {
		index = 1
	}
	presentation := &a.presentation[index]
	text := a.subtitleAt(state.Position)
	presentation.subtitle = text
	services := c.Services()
	stage.Draw(func(p *ui.Painter, r ui.Rect) {
		state := a.player.Snapshot()
		if state.Playing && !state.Loading {
			// Repaint this drawing at the display's pace. Static controls keep
			// their scene, and the video surface only presents changed frames
			// or camera parameters. No view construction or layout is needed.
			p.AnimationFrame()
			a.updateTracking(state, float64(p.Now().Sub(trackingStarted))/float64(time.Millisecond))
			a.loopPlayback(state)
		}
		if a.subtitleAt(state.Position) != text {
			services.Invalidate()
		}
		if state.FrameID == 0 {
			presentation.params = a.params(pip)
			p.Fill(r, ui.RGB(0, 0, 0), 0)
			return
		}
		surface := a.surface
		if pip {
			surface = a.pipSurface
		}
		if surface == nil {
			return
		}
		params, scale := a.params(pip), float64(p.Scale())
		interaction := params != presentation.params || r.W != presentation.width || r.H != presentation.height || state.Generation != presentation.generation
		// Frame() may already have loaded a picture newer than this snapshot.
		if interaction || state.FrameID > presentation.frame || scale != presentation.scale || presentation.err != "" {
			frame := a.player.Frame()
			if frame == nil {
				return
			}
			defer frame.Close()
			if err := surface.Present(frame, params, float64(r.W), float64(r.H), scale); err != nil {
				message := err.Error()
				if presentation.err != message {
					presentation.err = message
					a.notice = message
					services.Invalidate()
					if pip && a.window != nil {
						a.window.Invalidate()
					}
				}
			} else {
				presentation.err = ""
				presentation.frame = frame.ID()
				presentation.scale = scale
			}
		}
		presentation.params = params
		presentation.width = r.W
		presentation.height = r.H
		presentation.generation = state.Generation
		video.Paint(surface, p, r)
	})
	stage.Children(func() {
		if text != "" {
			ui.Box(c.Key("subtitles")).Absolute().Left(width * .06).Right(width * .06).Bottom(height * .14).Center().IgnoreInput(true).Children(func() {
				ui.Text(c, text).FontSize(min(28, max(16, width*.022))).FontWeight(600).TextAlign(ui.Center).TextColor(ui.RGB(255, 255, 255)).Wrap().MaxWidth(min(width*.86, 1152)).Padding(6, 14)
			})
		}
	})
	if pip && state.URL != "" {
		a.playbackKeyboard(c, true)
	}
}

// Keep playback lifecycle work running when the system PiP is the only visible window.
func (a *application) updatePlaybackPresentation(state media.State) {
	if state.PlaybackID == a.playbackID {
		a.resolveVideoMode(state)
	}
	if state.Generation != a.lastGeneration {
		a.lastGeneration = state.Generation
		a.tracker.Reset()
		a.endedGeneration = 0
	}
	if state.Ended && !a.loopPlayback(state) && a.endedGeneration != state.Generation {
		if a.selected >= 0 && a.selected < len(a.entries) {
			a.recordPlaybackHistory(a.entries[a.selected], state)
		}
		a.endedGeneration = state.Generation
		a.next(true)
	}
	if a.selected >= 0 && time.Since(a.lastSaved) > 5*time.Second {
		a.persist()
	}
}

func (a *application) subtitleAt(position float64) string {
	if !a.preferences.SubtitlesEnabled {
		return ""
	}
	return subtitle.Active(a.cues, position)
}

// Playing PiP samples these inputs while painting. A paused PiP needs a view
// update only when its own picture or subtitles change in the main window.
func (a *application) pipNeedsView(state media.State) bool {
	shown := a.presentation[1]
	return !state.Playing && (a.params(true) != shown.params || a.subtitleAt(state.Position) != shown.subtitle)
}

// Loops follow the video clock even while controls are hidden. Keep an end-of-
// file B marker ahead of automatic playlist advance, and coalesce redraws while
// the worker is still processing this generation's seek.
func (a *application) loopPlayback(state media.State) bool {
	if state.Loading || !state.Playing && !state.Ended || a.loopA == nil || a.loopB == nil || state.Position < *a.loopB {
		return false
	}
	if a.loopSeekGeneration != state.Generation {
		a.loopSeekGeneration = state.Generation
		a.player.Seek(*a.loopA)
	}
	return true
}

func (a *application) stageClick(c *ui.Context) {
	if a.singleClickAt.IsZero() {
		return
	}
	if c.Now().Before(a.singleClickAt) {
		c.After(a.singleClickAt.Sub(c.Now()))
		return
	}
	a.singleClickAt = time.Time{}
	a.player.Toggle()
	a.showControls(c.Now(), mouseHideDelay)
}

func (a *application) cancelStageInteraction() {
	a.singleClickAt = time.Time{}
	a.dragging = false
	a.scrubbing, a.seekUI.fine = false, false
}

func (a *application) stageInput(event ui.InputEvent, aspect float64, pip bool) bool {
	point := ui.Point{X: event.X, Y: event.Y}
	switch event.Kind {
	case ui.InputPointerDown:
		a.controlsKeyboard = false
		a.singleClickAt = time.Time{}
		a.showControls(inputTime(event), mouseHideDelay)
		if !pip && (a.seekUI.chaptersOpen || a.projectionOpen || a.adjustmentsOpen) {
			a.seekUI.chaptersOpen, a.projectionOpen, a.adjustmentsOpen = false, false, false
			return true
		}
		if event.Button == 1 {
			a.player.Toggle()
			return true
		}
		if event.Button != 0 {
			return false
		}
		a.dragging = true
		a.pressPointer = point
		a.lastPointer = point
		a.dragDistance = 0
		if event.Clicks == 2 {
			if !pip {
				a.window.ToggleFullScreen()
			}
			a.dragging = false
		}
		return true
	case ui.InputPointerMove:
		dx, dy := float64(point.X-a.lastPointer.X), float64(point.Y-a.lastPointer.Y)
		if a.dragging {
			distance := math.Hypot(float64(point.X-a.pressPointer.X), float64(point.Y-a.pressPointer.Y))
			if a.dragDistance <= stageDragThreshold {
				dx, dy = float64(point.X-a.pressPointer.X), float64(point.Y-a.pressPointer.Y)
			}
			a.dragDistance = max(a.dragDistance, distance)
		}
		if a.dragging && a.dragDistance > stageDragThreshold && a.mode == projection.Flat {
			width, height := float64(a.width), float64(a.height)
			if pip {
				bounds := a.pipWindow.ContentBounds()
				width, height = float64(bounds.Width), float64(bounds.Height)
			}
			a.flatView = a.flatView.Pan(dx/max(1, width), dy/max(1, height), aspect, a.sourceAspect())
		} else if a.dragging && a.dragDistance > stageDragThreshold {
			next := a.view
			next.Yaw += dx * .08 * a.preferences.VRDragSensitivity
			next.Pitch = projection.Clamp(next.Pitch+dy*.08*a.preferences.VRDragSensitivity, -85, 85)
			a.setView(next, aspect)
		}
		a.pointerActivity(event)
		a.lastPointer = point
		return true
	case ui.InputPointerUp:
		if event.Button == 0 && a.dragging {
			a.dragging = false
			distance := math.Hypot(float64(point.X-a.pressPointer.X), float64(point.Y-a.pressPointer.Y))
			if !event.Canceled && max(a.dragDistance, distance) <= stageDragThreshold && event.Clicks == 1 {
				a.singleClickAt = inputTime(event).Add(250 * time.Millisecond)
			}
		}
		a.showControls(inputTime(event), mouseHideDelay)
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
	a.manualUntil = time.Now().Add(a.preferences.centeringDelay())
	a.tracker.Reset()
}
func (a *application) zoom(scale, aspect float64) {
	if a.mode == projection.Flat {
		a.flatView = a.flatView.Scale(scale, aspect, a.sourceAspect())
		return
	}
	next := a.view
	next.Forward = projection.ZoomForward(next.Forward, scale)
	a.setView(next, aspect)
}
func (a *application) keyboard(c *ui.Context) {
	if a.scrubbing && c.Shortcut(0, ui.KeyEscape) {
		a.scrubbing = false
		a.seekUI.fine = false
		a.showControls(c.Now(), mouseHideDelay)
		return
	}
	if a.seekUI.chaptersOpen && c.Shortcut(0, ui.KeyEscape) {
		a.seekUI.chaptersOpen = false
		a.showControls(c.Now(), mouseHideDelay)
		a.timelineHandle.Focus(c)
		return
	}
	if a.projectionOpen {
		for _, step := range []struct {
			key   ui.Key
			delta int
		}{{ui.KeyUp, -1}, {ui.KeyDown, 1}} {
			if c.Shortcut(0, step.key) {
				a.projectionFocused = (a.projectionFocused + step.delta + len(a.projectionItems)) % len(a.projectionItems)
				a.projectionItems[a.projectionFocused].Focus(c)
			}
		}
		if c.Shortcut(0, ui.KeyEnter) {
			if a.projectionFocused == len(projection.IDs) {
				a.redetectMode()
			} else {
				a.setMode(projection.Mode(a.projectionFocused))
			}
			a.projectionOpen = false
			a.projectionButton.Focus(c)
		}
	}
	if a.adjustmentsOpen || a.projectionOpen {
		if c.Shortcut(0, ui.KeyEscape) {
			if a.projectionOpen {
				a.projectionButton.Focus(c)
			} else {
				a.adjustmentsButton.Focus(c)
			}
			a.adjustmentsOpen = false
			a.projectionOpen = false
			a.showControls(c.Now(), mouseHideDelay)
		}
		return
	}
	if c.Shortcut(0, ui.KeyEscape) && a.window.IsFullScreen() {
		a.window.SetFullScreen(false)
	}
	state := a.player.Snapshot()
	if state.URL == "" {
		return
	}
	a.playbackKeyboard(c, false)
}

func (a *application) playbackKeyboard(c *ui.Context, pip bool) {
	for _, def := range playbackShortcuts {
		if pip && def.id == "fullscreen" {
			continue
		}
		for _, chord := range a.preferences.shortcutKeys(def) {
			// While dragging, plain arrows adjust the preview one frame at a time.
			if a.scrubbing && a.timelineHandle.Focused(c) && chord.Mods == 0 && (chord.Key == ui.KeyLeft || chord.Key == ui.KeyRight) {
				continue
			}
			if c.Shortcut(chord.Mods, chord.Key) {
				a.singleClickAt = time.Time{}
				def.run(a)
				a.showControls(c.Now(), 1500*time.Millisecond)
				return
			}
		}
	}
}
func (a *application) controls(c *ui.Context, state media.State) {
	fullscreen := a.window != nil && a.window.IsFullScreen()
	pad, inset, buttonSize, playSize, gap := float32(24), float32(16), float32(36), float32(48), float32(12)
	if a.width < 640 {
		pad, inset, buttonSize, playSize, gap = 12, 8, 44, 56, 8
	}
	panel := ui.Themed(c.Key("controls"), darkPlayerTheme).Bind(&a.controlsPanel).Absolute().Left(pad).Right(pad).Bottom(pad).MaxWidth(1152).Margin(0, ui.Auto).Padding(inset).Gap(12).Transition(ui.ElementTransition{Duration: 300 * time.Millisecond, Enter: &ui.Motion{Y: 16}, Exit: &ui.Motion{Y: 16}})
	hold(c, a, panel)
	panel.Children(func() {
		if !a.scrubbing || !a.seekUI.fine {
			ui.Row(c).FillWidth().Gap(gap).AlignItems(ui.Center).Wrap().Children(func() {
				name, label := "play", "controls.play"
				if state.Playing {
					name, label = "pause", "controls.pause"
				}
				a.iconControl(c, name, a.t(label), iconStyle{playSize, 26, floatingIcon}, false, a.player.Toggle).Bind(&a.playButton)
				if len(a.entries) > 1 {
					a.iconControl(c, "skip-forward", a.t("controls.nextVideo"), iconStyle{buttonSize, 18, floatingIcon}, false, func() { a.next(false) })
				}
				ui.Spacer(c).Grow(1)
				actions := ui.Row(c).Gap(8).AlignItems(ui.Center)
				if a.width < 480 {
					actions.FillWidth().Justify(ui.End)
				}
				actions.Children(func() {
					if len(a.cues) > 0 {
						label := "controls.showSubtitles"
						if a.preferences.SubtitlesEnabled {
							label = "controls.hideSubtitles"
						}
						a.iconControl(c, "subtitles", a.t(label), iconStyle{buttonSize, 18, floatingIcon}, a.preferences.SubtitlesEnabled, func() { a.preferences.SubtitlesEnabled = !a.preferences.SubtitlesEnabled; a.persist() })
					}
					a.iconControl(c, "projection-"+projection.IDs[a.mode], a.projectionLabel(int(a.mode)), iconStyle{buttonSize, 18, floatingIcon}, false, func() {
						a.projectionOpen = !a.projectionOpen
						a.projectionFocused = int(a.mode)
						a.adjustmentsOpen = false
						a.seekUI.chaptersOpen = false
					}).Bind(&a.projectionButton)
					a.iconControl(c, "sliders", a.t("controls.adjustVolumeSpeed"), iconStyle{buttonSize, 18, floatingIcon}, false, func() {
						a.adjustmentsOpen = !a.adjustmentsOpen
						a.projectionOpen, a.seekUI.chaptersOpen = false, false
					}).Bind(&a.adjustmentsButton)
					pipLabel := "controls.enterPip"
					if a.pipActive() {
						pipLabel = "controls.exitPip"
					}
					a.iconControl(c, "picture-in-picture", a.t(pipLabel), iconStyle{buttonSize, 18, floatingIcon}, a.pipActive(), a.togglePip)
					name = "corners-out"
					fullscreenLabel := "controls.enterFullscreen"
					if fullscreen {
						name = "corners-in"
						fullscreenLabel = "controls.exitFullscreen"
					}
					a.iconControl(c, name, a.t(fullscreenLabel), iconStyle{buttonSize, 18, floatingIcon}, fullscreen, a.window.ToggleFullScreen).Bind(&a.fullscreenButton)
				})
			})
		}
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
		height := min(float32(380), max(100, anchor.Y-22))
		panel := panel(c, 16).Absolute().Left(left).Bottom(a.height - anchor.Y + 10).Width(208).Height(height).Padding(6).Role(ui.RoleMenu).Label(a.t("projection.title"))
		hold(c, a, panel)
		panel.Children(func() {
			ui.Scroll(c).Fill().Gap(4).Children(func() {
				for i := range projection.Labels {
					if i == 0 || projection.Mode(i) == projection.Flat {
						key := "projection.vr"
						if projection.Mode(i) == projection.Flat {
							key = "projection.flat"
						}
						smallText(c, a.t(key), .42).FontSize(10).Height(20).Padding(2, 10)
					}
					label := a.projectionLabel(i)
					selected := a.mode == projection.Mode(i)
					color := ink(c, .68)
					if selected {
						color = ink(c, 1)
					}
					button := ui.Button(c, "").Bind(&a.projectionItems[i]).FillWidth().Height(36).FontSize(12).Padding(0, 10).Background(ui.Transparent).Border(0, ui.Transparent).Radius(12).Label(label)
					button.Children(func() {
						ui.Row(c).Fill().Gap(12).AlignItems(ui.Center).Children(func() {
							ui.Icon(c, icons["projection-"+projection.IDs[i]]).Size(18, 18).TextColor(color)
							ui.Text(c, label).FontSize(12).FontWeight(500).TextColor(color).Grow(1).MinWidth(0).Wrap()
							if selected {
								ui.Icon(c, icons["check"]).Size(16, 16).TextColor(textColor(c, .72))
							}
						})
					})
					if selected {
						button.Background(ink(c, .12))
						button.AutoFocus()
					}
					if button.Clicked() {
						a.setMode(projection.Mode(i))
						a.projectionOpen = false
						a.projectionButton.Focus(c)
					}
				}
				ui.Divider(c).Background(ink(c, .07))
				label := a.t("projection.redetect")
				button := ui.Button(c, "").Bind(&a.projectionItems[len(projection.IDs)]).FillWidth().Height(36).Padding(0, 10).Background(ui.Transparent).Border(0, ui.Transparent).Radius(12).Label(label)
				button.Children(func() {
					ui.Row(c).Fill().Gap(12).AlignItems(ui.Center).Children(func() {
						ui.Icon(c, icons["rotate-ccw"]).Size(18, 18).TextColor(textColor(c, .68))
						ui.Text(c, label).FontSize(12).FontWeight(500).TextColor(textColor(c, .68))
					})
				})
				if button.Clicked() {
					a.redetectMode()
					a.projectionOpen = false
					a.projectionButton.Focus(c)
				}
			})
		})
	}
}
func (a *application) adjustments(c *ui.Context) {
	anchor, controls := a.adjustmentsButton.Bounds(c), a.controlsPanel.Bounds(c)
	panel := panel(c, 16).Absolute().Right(a.width - controls.X - controls.W).Bottom(a.height - anchor.Y + 10).Width(min(296, a.width-24)).Padding(8).Gap(2)
	hold(c, a, panel)
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
	hold(c, a, panel)
	panel.Children(func() {
		ui.Row(c).FillWidth().Height(topControlSize + 2*topControlPadding).Padding(topControlPadding).Gap(topControlPadding).Children(func() {
			a.iconButton(c, "playlist", a.t("playlist.close"), topControlSize, true, func() { a.playlistExpanded = false })
			ui.Text(c, a.t("playlist.title")).FontSize(14).FontWeight(600).Grow(1).MinWidth(0).SingleLine()
			a.iconButton(c, "library", a.t("library.title"), 32, false, a.openCatalog)
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
			a.iconControl(c, "trash", a.t("playlist.clear"), iconStyle{32, 14, floatingIcon}, false, a.clearPlaylist)
		})
		rows := a.visiblePlaylist()
		a.playlistList.Key = func(i int) any { return rows[i].node.ID }
		a.playlistList.Label = func(i int) string { return rows[i].node.Name }
		ui.List(c.Key("playlist-rows"), &a.playlistList, len(rows), func(i int) {
			a.playlistRow(c, rows[i])
		}).FillWidth().Height(min(float32(len(rows))*36, max(40, a.height-top-306))).Padding(0, 8)
		ui.Box(c).Padding(8).Children(func() { a.pickers(c, min(272, a.width-40), true) })
	})
}

type presentationState struct {
	params        video.Parameters
	width, height float32
	generation    uint64
	frame         uint64
	scale         float64
	err           string
	subtitle      string
}

func (a *application) resetView() {
	if a.mode == projection.Flat {
		a.flatView = video.FlatView{}
		return
	}
	a.view = projection.DefaultView()
	a.manuallyPaused = !a.preferences.ResumeAfterMovement
	a.manualUntil = time.Now().Add(a.preferences.centeringDelay())
	a.tracker.Reset()
}
func (a *application) setMode(mode projection.Mode) {
	a.cancelModeDetection()
	a.detectMode = false
	a.modeManual = true
	a.applyMode(mode)
}

func (a *application) applyMode(mode projection.Mode) {
	a.mode = mode
	a.view = projection.DefaultView()
	a.flatView = video.FlatView{}
	a.tracker.Reset()
	a.manuallyPaused = false
	a.manualUntil = time.Time{}
	a.persist()
}

func (a *application) sourceAspect() float64 {
	metadata := a.player.Snapshot().Metadata
	return float64(max(1, metadata.Width)) / float64(max(1, metadata.Height))
}
