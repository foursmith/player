package main

import (
	"fmt"
	"math"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

const (
	darkWindowBackground  = "#14171b"
	lightWindowBackground = "#f5f7f8"
)

type themedSurface interface {
	Theme() *ui.Theme
}

func white(alpha float32) ui.Color { return ui.RGBA(255, 255, 255, alpha) }

func ink(surface themedSurface, alpha float32) ui.Color {
	if surface.Theme().Dark {
		return white(alpha)
	}
	return surface.Theme().Text.Alpha(alpha)
}

func textColor(surface themedSurface, alpha float32) ui.Color {
	if !surface.Theme().Dark {
		alpha = 1 - (1-alpha)*.8
	}
	return ink(surface, alpha)
}

var darkPlayerTheme = newPlayerTheme(true)
var lightPlayerTheme = newPlayerTheme(false)

func newPlayerTheme(dark bool) *ui.Theme {
	theme := ui.LightTheme()
	if dark {
		theme = ui.DarkTheme()
		theme.Accent = ui.Hex("#62cfd8")
		theme.AccentText = ui.Hex("#062d36")
		theme.Warning = ui.Hex("#efad82")
		theme.Danger = ui.Hex("#fecaca")
	} else {
		theme.Text = ui.Hex("#18242b")
		theme.Accent = ui.Hex("#087784")
		theme.AccentText = white(1)
		theme.Warning = ui.Hex("#8c5700")
	}
	theme.Background = ui.Transparent
	foreground := theme.Text
	if dark {
		foreground = white(1)
	}
	theme.Surface = foreground.Alpha(.05)
	theme.SurfaceHover = foreground.Alpha(.07)
	theme.SurfacePressed = foreground.Alpha(.12)
	theme.AccentHover = theme.Accent
	theme.AccentPressed = theme.Accent
	theme.Focus = theme.Accent.Alpha(.6)
	theme.Selection = theme.Accent.Alpha(.25)
	theme.FontSize = 12
	theme.Font = "Inter, system-ui"
	theme.Radius = 12
	theme.Spacing = 4
	return theme
}

func (p preferences) theme(systemDark bool) *ui.Theme {
	if p.Appearance == mygo.ThemeLight || p.Appearance == mygo.ThemeSystem && !systemDark {
		return lightPlayerTheme
	}
	return darkPlayerTheme
}

func windowBackground(dark bool) string {
	if !dark {
		return lightWindowBackground
	}
	return darkWindowBackground
}

func (a *application) useTheme(c *ui.Context) {
	c.SetTheme(a.preferences.theme(c.Theme().Dark))
}

func (a *application) usePlayerTheme(c *ui.Context, playing bool) {
	if playing {
		a.useTheme(c)
	} else {
		c.SetTheme(darkPlayerTheme)
	}
}

func (a *application) applyAppearance() {
	if a.window != nil {
		mygo.Theme.SetSource(a.preferences.Appearance)
	}
	a.refreshAppearance()
}

func (a *application) refreshAppearance() {
	if a.shutdown || a.window == nil {
		return
	}
	background := windowBackground(mygo.Theme.IsDark())
	for _, window := range []*mygo.Window{a.settingsWindow, a.catalogWindow} {
		if window != nil {
			_ = window.SetBackgroundColor(background)
			window.SetWindowButtonsTheme(a.preferences.Appearance)
		}
	}
	a.refreshWindows()
}

func glass(c *ui.Context, radius float32) ui.Element {
	return ui.Box(c).Radius(radius).Background(ui.Transparent).Draw(func(p *ui.Painter, r ui.Rect) {
		paintGlass(p, r, radius, false, false)
	})
}

// Text-bearing surfaces need a stable background over bright video.
func panel(c *ui.Context, radius float32) ui.Element {
	background := ui.RGBA(26, 29, 34, .96)
	if !c.Theme().Dark {
		background = ui.RGBA(250, 252, 253, .98)
	}
	return ui.Box(c).Radius(radius).Background(background).Draw(func(p *ui.Painter, r ui.Rect) {
		radius := min(radius, r.W/2, r.H/2)
		p.FillGradient(r, ui.LinearGradient{Angle: 180, Stops: []ui.GradientStop{{Color: ink(p, .04), At: 0}, {Color: ink(p, .01), At: 1}}}, radius)
		p.Stroke(r, ink(p, .12), radius, .75)
	})
}

// Alpha composition preserves the separate native video layer.
func paintGlass(p *ui.Painter, r ui.Rect, radius float32, active, hover bool) {
	radius = min(radius, r.W/2, r.H/2)
	if !p.Theme().Dark {
		p.Fill(r, ui.RGBA(250, 252, 253, .94), radius)
	}
	if active {
		p.Fill(r, ink(p, .10), radius)
	}
	p.Fill(r, ink(p, .05), radius)
	if hover {
		p.Fill(r, ink(p, .07), radius)
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
		{Color: ink(p, alpha*8/15), At: .25},
		{Color: ink(p, alpha), At: .5},
		{Color: ink(p, alpha*8/15), At: .75},
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
		color := ink(p, .92)
		if style.surface == floatingIcon {
			paintGlass(p, r, radius, pressed, hover)
			if focus {
				p.Fill(r, ink(p, .10), radius)
			}
		} else {
			background := ui.Transparent
			color = ink(p, .46)
			if style.surface == subtleIcon {
				background, color = ink(p, .07), ink(p, .68)
			}
			if hover || focus {
				background, color = ink(p, .12), ink(p, 1)
			}
			p.Fill(r, background, radius)
		}
		if hover || pressed {
			color = ink(p, 1)
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
			p.Fill(dot, ink(p, .20), 3)
			p.Stroke(dot, ink(p, .34), 3, 1)
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
	return ui.Text(c, text).FontSize(10).TextColor(textColor(c, alpha))
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
	p.Fill(line, ink(p, .18), 2.24)
	line.W *= fraction
	p.Fill(line, ink(p, .82), 2.24)
	knob := ui.Rect{X: r.X + r.W*fraction - 8, Y: r.Y + r.H/2 - 8, W: 16, H: 16}
	paintGlass(p, knob, 8, false, false)
	p.FillGradient(knob, ui.LinearGradient{Angle: 145, Stops: []ui.GradientStop{{Color: ink(p, .26), At: 0}, {Color: ink(p, .12), At: 1}}}, 8)
	p.Stroke(knob, ink(p, .34), 8, 1)
}
