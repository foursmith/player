package main

import (
	"fmt"
	"github.com/egoist/mygo/ui"
	"math"
	"time"
)

var accent = ui.Hex("#62cfd8")

func white(alpha float32) ui.Color { return ui.RGBA(255, 255, 255, alpha) }

var playerTheme = func() *ui.Theme {
	theme := *ui.DarkTheme()
	theme.Background = ui.Transparent
	theme.Surface = ui.RGBA(255, 255, 255, .05)
	theme.SurfaceHover = white(.07)
	theme.SurfacePressed = white(.12)
	theme.Accent = accent
	theme.AccentText = ui.Hex("#062d36")
	theme.FontSize = 12
	theme.Font = "Inter, system-ui"
	theme.Radius = 12
	theme.Spacing = 4
	return &theme
}()

func transparentTheme(c *ui.Context) { c.SetTheme(playerTheme) }
func glass(c *ui.Context, radius float32) ui.Element {
	return ui.Box(c).Radius(radius).Background(ui.Transparent).Draw(func(p *ui.Painter, r ui.Rect) {
		paintGlass(p, r, radius, false, false)
	})
}

// Text-bearing surfaces need a stable background over bright video. Keep
// them separate from the light glass used for individual floating controls.
func panel(c *ui.Context, radius float32) ui.Element {
	return ui.Box(c).Radius(radius).Background(ui.RGBA(26, 29, 34, .96)).Draw(func(p *ui.Painter, r ui.Rect) {
		radius := min(radius, r.W/2, r.H/2)
		p.FillGradient(r, ui.LinearGradient{Angle: 180, Stops: []ui.GradientStop{{Color: white(.04), At: 0}, {Color: white(.01), At: 1}}}, radius)
		p.Stroke(r, white(.12), radius, .75)
	})
}

// Match LiquidGlass's computed browser style, rather than interpreting its
// compound background class (which the browser currently does not apply).
// Alpha composition also preserves the separate native video layer.
func paintGlass(p *ui.Painter, r ui.Rect, radius float32, active, hover bool) {
	radius = min(radius, r.W/2, r.H/2)
	if active {
		p.Fill(r, white(.10), radius)
	}
	p.Fill(r, white(.05), radius)
	if hover {
		p.Fill(r, white(.07), radius)
	}
	paintGlassRim(p, r, radius, .075)
}

func paintGlassRim(p *ui.Painter, r ui.Rect, radius, alpha float32) {
	// Fade the upper reflection into the sides instead of clipping a full
	// outline at half height, which leaves abrupt ends on pill controls.
	x, y, width := r.X+.5, r.Y+.5, max(0, r.W-1)
	corner := max(0, radius-.5)
	k := corner * .55228475
	var highlight ui.Path
	highlight.MoveTo(x, y+corner).
		CubeTo(x, y+corner-k, x+corner-k, y, x+corner, y).
		LineTo(x+width-corner, y).
		CubeTo(x+width-corner+k, y, x+width, y+corner-k, x+width, y+corner)
	p.StrokePathGradient(&highlight, .75, ui.LinearGradient{Angle: 90, Stops: []ui.GradientStop{
		{Color: ui.Transparent, At: 0},
		{Color: white(alpha * 8 / 15), At: .25},
		{Color: white(alpha), At: .5},
		{Color: white(alpha * 8 / 15), At: .75},
		{Color: ui.Transparent, At: 1},
	}})
}

type iconSurface uint8

const (
	floatingIcon iconSurface = iota
	subtleIcon
	bareIcon
)

type iconStyle struct {
	size, iconSize float32
	surface        iconSurface
}

func (a *application) iconButton(c *ui.Context, name, label string, size float32, pressed bool, action func()) ui.Element {
	return a.iconControl(c, name, label, iconStyle{size, size / 2, floatingIcon}, pressed, action)
}

func (a *application) iconControl(c *ui.Context, name, label string, style iconStyle, pressed bool, action func()) ui.Element {
	radius := style.size / 2
	if style.surface == bareIcon {
		radius = 6
	}
	button := ui.Button(c, "").Size(style.size, style.size).Padding(0).Radius(radius).Background(ui.Transparent).Border(0, ui.Transparent).Label(label)
	hover := button.Hovered()
	down := button.Pressed()
	focus := button.FocusVisible()
	button.Draw(func(p *ui.Painter, r ui.Rect) {
		color := white(.92)
		if style.surface == floatingIcon {
			paintGlass(p, r, radius, pressed, hover)
			if focus {
				p.Fill(r, white(.10), radius)
			}
		} else {
			background := ui.Transparent
			color = white(.46)
			if style.surface == subtleIcon {
				background, color = white(.07), white(.68)
			}
			if hover || focus {
				background, color = white(.12), white(1)
			}
			p.Fill(r, background, radius)
		}
		if hover || pressed {
			color = white(1)
		}
		iconSize := style.iconSize
		if down {
			iconSize *= .95
		}
		if icon := icons[name]; icon != nil {
			bounds := ui.Rect{X: r.X + (r.W-iconSize)/2, Y: r.Y + (r.H-iconSize)/2, W: iconSize, H: iconSize}
			p.Icon(icon, bounds, color)
		}
		if pressed && style.surface == floatingIcon {
			dot := ui.Rect{X: r.X + r.W/2 - 3, Y: r.Y + r.H - 10, W: 6, H: 6}
			paintGlass(p, dot, 3, false, false)
			p.Fill(dot, white(.20), 3)
			p.Stroke(dot, white(.34), 3, 1)
		}
	})
	button.OnClick(action)
	return button
}

func formatTime(seconds float64) string {
	seconds = math.Max(0, seconds)
	total := int(seconds)
	if total >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", total/3600, (total/60)%60, total%60)
	}
	return fmt.Sprintf("%d:%02d", total/60, total%60)
}
func smallText(c *ui.Context, text string, alpha float32) ui.Element {
	return ui.Text(c, text).FontSize(10).TextColor(white(alpha))
}
func hold(a *application, element ui.Element) {
	if element.Hovered() || element.FocusWithin() || element.Pressed() {
		a.controlsHeld = true
	}
}

const mouseHideDelay = 2500 * time.Millisecond

func paintGlassTrack(p *ui.Painter, r ui.Rect, fraction float32) {
	fraction = max(0, min(1, fraction))
	line := ui.Rect{X: r.X, Y: r.Y + r.H/2 - 2.24, W: r.W, H: 4.48}
	p.Fill(line, white(.18), 2.24)
	line.W *= fraction
	p.Fill(line, white(.82), 2.24)
	knob := ui.Rect{X: r.X + r.W*fraction - 8, Y: r.Y + r.H/2 - 8, W: 16, H: 16}
	paintGlass(p, knob, 8, false, false)
	p.FillGradient(knob, ui.LinearGradient{Angle: 145, Stops: []ui.GradientStop{{Color: white(.26), At: 0}, {Color: white(.12), At: 1}}}, 8)
	p.Stroke(knob, white(.34), 8, 1)
}
