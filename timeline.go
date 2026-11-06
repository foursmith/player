package main

import (
	"fmt"
	"math"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/foursmith/player/internal/media"
)

type seekInteraction struct {
	hovered, fine, chaptersOpen bool
	hoverX, lastX, downY        float32
	hoverTime, fineTime         float64
	chapterList                 ui.ListState
}

func (a *application) timeline(c *ui.Context, state media.State) {
	if state.Metadata.Duration <= 0 {
		return
	}
	fine := a.scrubbing && a.seekUI.fine
	gap, trackHeight := float32(8), float32(24)
	if fine {
		gap, trackHeight = 0, 12
	}
	ui.Column(c.Key("timeline")).Gap(gap).Children(func() {
		position := state.Position
		if a.scrubbing {
			position = a.scrubTime
		}
		track := ui.Box(c).Bind(&a.timelineHandle).Height(trackHeight).FillWidth().Focusable().Cursor(ui.CursorPointer).Role(ui.RoleSlider).Range(0, state.Metadata.Duration, position).Label(a.t("timeline.playbackPosition"))
		track.HandleInput(func(e ui.InputEvent) bool { return a.timelineInput(e, a.timelineHandle.Bounds(c).W, state) })
		a.seekUI.hovered = track.Hovered()
		if x, _, over := track.PointerPosition(); over && !a.scrubbing {
			a.seekUI.hoverX = x
			a.seekUI.hoverTime = timelineTime(x, a.timelineHandle.Bounds(c).W, state.Metadata.Duration)
		}
		if a.scrubbing || track.Hovered() {
			a.controlsHeld = true
		}
		track.Draw(func(p *ui.Painter, r ui.Rect) {
			y := r.Y + r.H/2
			current := a.player.Snapshot()
			position := current.Position
			if a.scrubbing {
				position = a.scrubTime
			} else if current.Playing && !current.Loading {
				p.AnimationFrame()
			}
			hover := a.seekUI.hoverTime
			if a.scrubbing {
				hover = a.scrubTime
			}
			paintTimelineTrack(p, r, position, state.Metadata, a.seekUI.hovered || a.scrubbing, hover)
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
		if fine {
			width := a.timelineHandle.Bounds(c).W
			keys := seekFilmstripKeys(a.scrubTime, state.Metadata, width)
			ui.Box(c.Key("precise-filmstrip")).FillWidth().Height(seekFilmstripHeight).Radius(6).Clip().Background(ui.RGB(0, 0, 0)).Draw(func(p *ui.Painter, r ui.Rect) {
				a.paintFilmstrip(p, r, a.scrubTime, state.Metadata, width, keys)
			})
			return
		}
		ui.Row(c).FillWidth().Gap(6).Children(func() {
			smallText(c, formatTime(position), .48).Font("monospace")
			if len(state.Metadata.Chapters) > 0 {
				label := a.chapterTitle(state.Metadata.Chapters, media.ChapterAt(state.Metadata.Chapters, position))
				if label == "" {
					label = a.t("timeline.chapters")
				}
				button := ui.Button(c, "").Label(a.t("timeline.chapters")).MinWidth(0).MaxWidth(180).Height(20).Padding(0, 6).Radius(6).Border(0, ui.Transparent).Background(ink(c, .06))
				button.Children(func() {
					ui.Row(c).FillWidth().Gap(5).AlignItems(ui.Center).Children(func() {
						ui.Text(c, label).SingleLine().MinWidth(0).Grow(1).FontSize(10).TextColor(ink(c, .7))
						ui.Text(c, "›").FontSize(12).TextColor(ink(c, .5))
					})
				})
				if button.Clicked() {
					a.seekUI.chaptersOpen = !a.seekUI.chaptersOpen
					if a.seekUI.chaptersOpen {
						a.seekUI.chapterList.ScrollIntoView(max(0, media.ChapterAt(state.Metadata.Chapters, position)))
					}
					a.adjustmentsOpen, a.projectionOpen = false, false
				}
			}
			for i, label := range []string{"A", "B"} {
				text := label
				if i == 0 && a.loopA != nil {
					text += " " + formatTime(*a.loopA)
				}
				if i == 1 && a.loopB != nil {
					text += " " + formatTime(*a.loopB)
				}
				button := ui.Button(c, text).Font("monospace").FontSize(9).Height(20).Padding(0, 6).Radius(6).Border(0, ui.Transparent).Background(ink(c, .06))
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
						a.iconButton(c, "x", a.t("timeline.clearLoop"), 20, false, func() { a.loopA = nil; a.loopB = nil })
					} else {
						ui.Box(c).Width(8).Height(1).Background(ink(c, .15))
					}
				}
			}
			ui.Spacer(c).Grow(1)
			smallText(c, formatTime(state.Metadata.Duration), .6).Font("monospace")
		})
	})
}

func timelineTime(x, width float32, duration float64) float64 {
	return max(0, min(1, float64(x/max(1, width)))) * duration
}

func fineSecondsPerPixel(width float32, m media.Metadata) float64 {
	return min(m.Duration/float64(max(1, width)), 1/max(1, m.FrameRate))
}

func (a *application) moveScrub(e ui.InputEvent, width float32, m media.Metadata) {
	if !a.seekUI.fine && e.Y <= a.seekUI.downY-40 {
		a.seekUI.fine = true
		a.seekUI.fineTime = a.scrubTime
	} else if a.seekUI.fine {
		a.seekUI.fineTime += float64(e.X-a.seekUI.lastX) * fineSecondsPerPixel(width, m)
	} else {
		a.scrubTime = timelineTime(e.X, width, m.Duration)
	}
	if a.seekUI.fine {
		a.seekUI.fineTime = max(0, min(m.Duration, a.seekUI.fineTime))
		fps := max(1, m.FrameRate)
		a.scrubTime = min(m.Duration, math.Round(a.seekUI.fineTime*fps)/fps)
	}
	a.seekUI.lastX = e.X
}

func (a *application) timelineInput(e ui.InputEvent, width float32, state media.State) bool {
	switch e.Kind {
	case ui.InputPointerDown:
		if e.Button != 0 {
			return false
		}
		a.scrubbing = true
		a.scrubTime = timelineTime(e.X, width, state.Metadata.Duration)
		a.seekUI.fine = false
		a.seekUI.downY, a.seekUI.lastX = e.Y, e.X
		a.seekUI.chaptersOpen = false
		a.adjustmentsOpen, a.projectionOpen = false, false
		a.activity(mouseHideDelay)
		return true
	case ui.InputPointerMove:
		a.seekUI.hoverX = e.X
		a.seekUI.hoverTime = timelineTime(e.X, width, state.Metadata.Duration)
		if a.scrubbing {
			a.moveScrub(e, width, state.Metadata)
		}
		a.activity(mouseHideDelay)
		return true
	case ui.InputPointerUp:
		if !a.scrubbing || e.Button != 0 {
			return false
		}
		if !e.Canceled {
			a.moveScrub(e, width, state.Metadata)
			a.player.Seek(a.scrubTime)
		}
		a.scrubbing, a.seekUI.fine = false, false
		a.activity(mouseHideDelay)
		return true
	case ui.InputKeyDown:
		position := state.Position
		if a.scrubbing {
			position = a.scrubTime
		}
		step := 1 / max(1, state.Metadata.FrameRate)
		switch e.Key {
		case ui.KeyEscape:
			if !a.scrubbing {
				return false
			}
			a.scrubbing, a.seekUI.fine = false, false
			return true
		case ui.KeyLeft:
			position -= step
		case ui.KeyRight:
			position += step
		case ui.KeyHome:
			position = 0
		case ui.KeyEnd:
			position = state.Metadata.Duration
		default:
			return false
		}
		position = max(0, min(state.Metadata.Duration, position))
		if a.scrubbing {
			a.scrubTime, a.seekUI.fineTime = position, position
		} else {
			a.player.Seek(position)
		}
		a.activity(mouseHideDelay)
		return true
	}
	return false
}

func paintTimelineTrack(p *ui.Painter, r ui.Rect, position float64, m media.Metadata, active bool, hover float64) {
	if len(m.Chapters) == 0 {
		paintGlassTrack(p, r, float32(position/m.Duration))
	} else {
		const baseHeight float32 = 4.48
		emphasisAt := position
		if active {
			emphasisAt = hover
		}
		emphasisAt = max(0, min(m.Duration, emphasisAt))
		// Chapter boundaries are gaps in the track, leaving the mapping to
		// presentation time linear even with very short chapters.
		start := 0.0
		for i := 0; i <= len(m.Chapters); i++ {
			end := m.Duration
			if i < len(m.Chapters) {
				end = m.Chapters[i].Start
			}
			if end <= start {
				continue
			}
			height := baseHeight
			if emphasisAt >= start && (emphasisAt < end || end == m.Duration) {
				height = 8
			}
			left := r.X + r.W*float32(start/m.Duration)
			right := r.X + r.W*float32(end/m.Duration)
			gap := min(float32(2), (right-left)/4)
			if start > 0 {
				left += gap / 2
			}
			if end < m.Duration {
				right -= gap / 2
			}
			line := ui.Rect{X: left, Y: r.Y + r.H/2 - height/2, W: max(0, right-left), H: height}
			p.Fill(line, ink(p, .22), height/2)
			played := min(right, r.X+r.W*float32(position/m.Duration)) - left
			if played > 0 {
				part := line
				part.W = played
				p.Fill(part, ink(p, .82), height/2)
			}
			start = end
		}
		knob := ui.Rect{X: r.X + r.W*float32(position/m.Duration) - 8, Y: r.Y + r.H/2 - 8, W: 16, H: 16}
		paintGlass(p, knob, 8, false, false)
		p.Stroke(knob, ink(p, .34), 8, 1)
	}
	if active {
		x := r.X + r.W*float32(hover/m.Duration)
		p.Fill(ui.Rect{X: x - 1, Y: r.Y + r.H/2 - 7, W: 2, H: 14}, ink(p, .55), 1)
	}
}

func (a *application) chapterTitle(chapters []media.Chapter, index int) string {
	if index < 0 || index >= len(chapters) {
		return ""
	}
	if title := chapters[index].Title; title != "" {
		return title
	}
	return fmt.Sprintf(a.t("timeline.chapterNumber"), index+1)
}

func formatPreciseTime(at float64) string {
	// Frame-sized movement remains visible even within the same second.
	millis := int64(math.Round(max(0, at) * 1000))
	return fmt.Sprintf("%s.%03d", formatTime(float64(millis/1000)), millis%1000)
}

const seekFilmstripHeight float32 = 64

func seekFilmstripKeys(at float64, metadata media.Metadata, width float32) []int64 {
	spacing := max(.5, 112*fineSecondsPerPixel(width, metadata))
	center := math.Floor(at/spacing) * spacing
	var keys []int64
	for _, offset := range []float64{0, 1, -1, 2, -2, 3, -3, 4, -4} {
		sample := center + offset*spacing
		if sample >= 0 && sample < metadata.Duration {
			keys = append(keys, previewKey(sample, metadata, true))
		}
	}
	return keys
}

func (a *application) timelinePreview(c *ui.Context, state media.State) {
	if state.Metadata.Duration <= 0 {
		return
	}
	if a.seekUI.chaptersOpen {
		a.chapterPicker(c, state)
		return
	}
	if !a.scrubbing && !a.seekUI.hovered {
		if a.seekPreviews != nil {
			a.seekPreviews.request(nil, true)
		}
		return
	}
	bounds := a.timelineHandle.Bounds(c)
	if bounds.W <= 0 {
		return
	}
	at, x := a.seekUI.hoverTime, a.seekUI.hoverX
	if a.scrubbing {
		at, x = a.scrubTime, a.seekUI.lastX
	}
	fine := a.scrubbing && a.seekUI.fine
	key := previewKey(at, state.Metadata, fine)
	keys := []int64{key}
	if fine {
		keys = append(keys, seekFilmstripKeys(at, state.Metadata, bounds.W)...)
	} else if next := previewKey(at+.5, state.Metadata, false); next != key {
		keys = append(keys, next)
	}
	if previews := a.currentSeekPreviews(c, state); previews != nil {
		previews.request(keys, fine)
	}

	width := min(float32(240), a.width-24)
	pictureHeight := min(width*9/16, max(float32(24), bounds.Y-64))
	height := pictureHeight + 44 // 14px hint, 22px caption, and two 4px gaps.
	left := max(float32(12), min(a.width-width-12, bounds.X+x-width/2))
	offset := left - (bounds.X + bounds.W/2 - width/2)
	ui.Box(c.Key("seek-preview")).AttachTo(c.Resolve(a.timelineHandle), ui.AnchorTop, ui.AnchorBottom).AttachEdgeMargin(12).Left(offset).Bottom(8).Width(width).Height(height).Gap(4).IgnoreInput(true).Children(func() {
		hint := "timeline.pullUp"
		if fine {
			hint = "timeline.releaseToSeek"
		}
		ui.Row(c).FillWidth().Height(14).Gap(3).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
			if !fine {
				ui.Icon(c, icons["caret-down"]).Size(10, 10).Rotate(180).TextColor(white(.85))
			}
			ui.Text(c, a.t(hint)).FontSize(9).TextColor(white(.85)).TextShadow(0, 1, 2, ui.RGBA(0, 0, 0, .8)).SingleLine().MinWidth(0).MaxWidth(width - 14)
		})
		ui.Box(c).Size(width, pictureHeight).Radius(6).Clip().Center().Children(func() { a.previewPicture(c, key) })
		ui.Row(c).MaxWidth(width).Height(22).Margin(0, ui.Auto).Padding(3, 8).Gap(6).AlignItems(ui.Center).Radius(11).Background(ui.RGBA(0, 0, 0, .5)).Children(func() {
			label := formatTime(at)
			if fine {
				label = formatPreciseTime(at)
			}
			ui.Text(c, label).Font("monospace").FontSize(11).FontWeight(500).TextColor(white(.96))
			if title := a.chapterTitle(state.Metadata.Chapters, media.ChapterAt(state.Metadata.Chapters, at)); title != "" {
				ui.Text(c, title).FontSize(11).TextColor(white(.96)).SingleLine().MinWidth(0).Grow(1)
			}
		})
	})
}

func (a *application) previewPicture(c *ui.Context, key int64) {
	bitmap, ready := a.seekPreviewImage(key)
	if bitmap != nil {
		ui.Image(c.Key(bitmap), bitmap).Fill().Fit(ui.Contain).Background(ui.RGB(0, 0, 0)).Radius(6).Border(1, white(.65)).Transition(ui.ElementTransition{
			Duration: 120 * time.Millisecond,
			Enter:    &ui.Motion{Opacity: 0},
			Exit:     &ui.Motion{Opacity: 0},
		})
		return
	}
	if !ready {
		return
	}
	ui.Text(c, a.t("timeline.previewUnavailable")).FontSize(10).TextColor(ink(c, .4)).TextAlign(ui.Center).Wrap()
}

func (a *application) paintFilmstrip(p *ui.Painter, r ui.Rect, at float64, m media.Metadata, trackWidth float32, keys []int64) {
	secondsPerPixel := fineSecondsPerPixel(trackWidth, m)
	p.Clip(r, 6, func() {
		for _, key := range keys {
			x := r.X + r.W/2 + float32((float64(key)/1e6-at)/secondsPerPixel) - 56
			if bitmap, _ := a.seekPreviewImage(key); bitmap != nil {
				p.Image(bitmap, ui.Rect{X: x, Y: r.Y, W: 112, H: r.H}, ui.Contain)
			}
		}
	})
	p.Fill(ui.Rect{X: r.X + r.W/2 - 1, Y: r.Y, W: 2, H: r.H}, ink(p, 1), 1)
	p.Stroke(r, ink(p, .25), 6, 1)
}

func (a *application) chapterPicker(c *ui.Context, state media.State) {
	bounds := a.timelineHandle.Bounds(c)
	rowHeight := float32(40)
	var previews *seekPreviews
	if state.Metadata.HasChapterArtwork {
		rowHeight = 56
		previews = a.currentSeekPreviews(c, state)
	} else if a.seekPreviews != nil {
		a.seekPreviews.request(nil, true)
	}
	var keys []int64
	width, height := min(float32(224), a.width-24), min(float32(360), 42+float32(len(state.Metadata.Chapters))*rowHeight, bounds.Y-24)
	if height <= 0 {
		return
	}
	box := panel(c.Key("chapter-picker"), 12).Absolute().Left(max(float32(12), min(bounds.X, a.width-width-12))).Top(bounds.Y-height-12).Size(width, height).Padding(8).Gap(4).Label(a.t("timeline.chapters"))
	hold(a, box)
	box.Children(func() {
		ui.Text(c, a.t("timeline.chapters")).FontSize(12).FontWeight(600).Height(22)
		current := media.ChapterAt(state.Metadata.Chapters, state.Position)
		a.seekUI.chapterList.Key = func(i int) any { return state.Metadata.Chapters[i].Start }
		ui.List(c.Key("chapter-list"), &a.seekUI.chapterList, len(state.Metadata.Chapters), func(i int) {
			chapter := state.Metadata.Chapters[i]
			ui.Box(c).FillWidth().Height(rowHeight).Padding(0, 0, 2).Children(func() {
				title := a.chapterTitle(state.Metadata.Chapters, i)
				button := ui.Button(c.Key(i), "").Label(title).Fill().Padding(4, 6).Border(0, ui.Transparent).Radius(8).Background(ui.Transparent)
				if i == current {
					button.Background(ink(c, .12))
				}
				button.Children(func() {
					ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
						if state.Metadata.HasChapterArtwork {
							key := chapterArtworkKey(i)
							keys = append(keys, key)
							ui.Box(c).Size(80, 45).Radius(4).Clip().Background(ink(c, .05)).Center().Children(func() {
								if bitmap, _ := a.seekPreviewImage(key); bitmap != nil {
									ui.Image(c, bitmap).Fill().Fit(ui.Contain)
								}
							})
						}
						ui.Column(c).MinWidth(0).Grow(1).Gap(3).Children(func() {
							ui.Text(c, title).SingleLine().MinWidth(0).FillWidth().FontSize(12).FontWeight(500)
							ui.Text(c, formatTime(chapter.Start)).Font("monospace").FontSize(10).TextColor(ink(c, .5))
						})
					})
				})
				if button.Clicked() {
					a.player.Seek(chapter.Start)
					a.seekUI.chaptersOpen = false
					a.activity(mouseHideDelay)
				}
			})
		}).FillWidth().Grow(1).MinHeight(0)
	})
	if previews != nil {
		previews.request(keys, true)
	}
}
