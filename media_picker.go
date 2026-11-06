package main

import (
	"math"
	"time"

	"github.com/egoist/mygo/ui"
)

type mediaPickerAction struct {
	icon, label, compact string
	run                  func()
}

const mediaPickerHeight float32 = 36

var pickerTransition = ui.Animation{
	Duration: 150 * time.Millisecond,
	Ease: func(t float32) float32 {
		return float32(bezierProgress(float64(t), .4, 0, .2, 1))
	},
}

func (a *application) pickers(c *ui.Context, width float32, appendOnly bool) {
	items := a.mediaOpenActions()
	if appendOnly {
		items = []mediaPickerAction{
			{icon: "file-video", label: "media.addFiles", run: func() { a.choose(false, true) }},
			{icon: "folder", label: "media.addFolder", run: func() { a.choose(true, true) }},
		}
	}
	a.mediaPicker(c, width, items)
}

func (a *application) mediaPicker(c *ui.Context, width float32, items []mediaPickerAction) {
	compact := len(items) == 3 && width < 380
	root := ui.Box(c).Width(width).Height(mediaPickerHeight)
	x, y, over := root.PointerPosition()
	dx, dy, sx, sy := float32(0), float32(0), float32(1), float32(1)
	if over && !c.Preferences().ReduceMotion {
		dx, dy = x-width/2, y-mediaPickerHeight/2
		distance := float32(math.Hypot(float64(dx), float64(dy)))
		if distance > 0 {
			intensity := min(distance/300, 1) * .12
			nx, ny := float32(math.Abs(float64(dx/distance))), float32(math.Abs(float64(dy/distance)))
			sx = 1 + nx*intensity*.3 - ny*intensity*.15
			sy = 1 + ny*intensity*.3 - nx*intensity*.15
		}
		dx, dy = dx*.012, dy*.012
	}
	// Keep pointer tracking on the stationary layout box so the elastic shell
	// cannot feed its own movement back into the next frame's cursor position.
	dx = root.Animate("x", dx, 72*time.Millisecond)
	dy = root.Animate("y", dy, 72*time.Millisecond)
	sx = root.Animate("sx", sx, 72*time.Millisecond)
	sy = root.Animate("sy", sy, 72*time.Millisecond)
	root.Children(func() {
		ui.Box(c).Fill().Radius(mediaPickerHeight/2).Clip().Translate(dx, dy).Scale(sx, sy).Draw(func(p *ui.Painter, r ui.Rect) {
			p.Fill(r, ink(c, .05), mediaPickerHeight/2)
			paintGlassRim(p, r, mediaPickerHeight/2, .14)
		}).Children(func() {
			ui.Row(c).Fill().Children(func() {
				for i, item := range items {
					if i > 0 {
						ui.Box(c).Width(1).Height(16).Background(ink(c, .12))
					}
					button := ui.Button(c.Key(item.label), "").Grow(1).Height(mediaPickerHeight).Padding(0).Radius(0).Background(ui.Transparent).Border(0, ui.Transparent).FocusRing(false).Label(a.t(item.label))
					background, foreground, scale := ink(c, 0), ink(c, .82), float32(1)
					if button.Hovered() {
						background, foreground = ink(c, .07), ink(c, 1)
					}
					if button.FocusVisible() {
						background, foreground = ink(c, .10), ink(c, 1)
					}
					if button.Pressed() {
						if c.Preferences().ReduceMotion {
							background = ink(c, .10)
						} else {
							scale = .95
						}
					}
					background = button.AnimateColor("background", background, pickerTransition)
					foreground = button.AnimateColor("foreground", foreground, pickerTransition)
					scale = button.AnimateTo("press", scale, pickerTransition)
					label, size := a.t(item.label), float32(12)
					if compact {
						label, size = a.t(item.compact), 11
					}
					first, last := i == 0, i == len(items)-1
					button.Draw(func(p *ui.Painter, r ui.Rect) {
						visual := ui.Rect{X: r.X + r.W*(1-scale)/2, Y: r.Y + r.H*(1-scale)/2, W: r.W * scale, H: r.H * scale}
						paintPickerSegment(p, visual, background, first, last)
						text := ui.Span{Text: label, Size: size * scale, Weight: 600, Color: foreground}
						w, h := p.MeasureText(0, text)
						x := r.X + (r.W-w-20*scale)/2
						p.Icon(icons[item.icon], ui.Rect{X: x, Y: r.Y + (r.H-14*scale)/2, W: 14 * scale, H: 14 * scale}, foreground)
						p.RichText(x+20*scale, r.Y+(r.H-h)/2, 0, text)
					})
					button.OnClick(item.run)
				}
			})
		})
	})
}

// Round only the outer ends of the group; the edges at each divider stay flat.
func paintPickerSegment(p *ui.Painter, r ui.Rect, color ui.Color, first, last bool) {
	if !first && !last {
		p.Fill(r, color, 0)
		return
	}
	clip, radius := r, r.H/2
	if !first {
		clip.X -= radius
		clip.W += radius
	}
	if !last {
		clip.W += radius
	}
	p.Clip(clip, radius, func() { p.Fill(r, color, 0) })
}
