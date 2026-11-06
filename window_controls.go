package main

import "github.com/egoist/mygo/ui"

func windowSurface(c *ui.Context) ui.Element {
	return ui.Row(c).Fill().Background(ui.Hex(windowBackground(c.Theme().Dark)))
}

func windowSidebar(c *ui.Context) ui.Element {
	return ui.Column(c).Width(200).Shrink(0).FillHeight().Background(ink(c, .018))
}

func navigationStyle(c *ui.Context, button ui.Element, selected bool) ui.Element {
	button.FillWidth().Height(34).Padding(0, 10).Radius(10).Background(ui.Transparent).Border(0, ui.Transparent).FocusRing(false)
	background, foreground := ink(c, 0), ink(c, .58)
	if selected {
		background, foreground = c.Theme().Accent.Alpha(.1), c.Theme().Accent
	}
	if button.Hovered() || button.FocusVisible() {
		background, foreground = ink(c, .065), ink(c, .95)
		if selected {
			background = c.Theme().Accent.Alpha(.15)
		}
	}
	background = button.AnimateColor("background", background, pickerTransition)
	foreground = button.AnimateColor("foreground", foreground, pickerTransition)
	return button.TextColor(foreground).Draw(func(p *ui.Painter, r ui.Rect) {
		p.Fill(r, background, 10)
		if selected {
			paintGlassRim(p, r, 10, .075)
		}
		if button.FocusVisible() {
			p.Stroke(r, c.Theme().Accent.Alpha(.45), 10, 1)
		}
	})
}

func controlSurface(c *ui.Context, button ui.Element, filled bool) ui.Element {
	background, foreground := ink(c, 0), ink(c, .58)
	if filled {
		background, foreground = ink(c, .045), ink(c, .8)
	}
	if button.Hovered() || button.FocusVisible() {
		background, foreground = ink(c, .085), ink(c, 1)
	}
	if button.Pressed() {
		background = ink(c, .12)
	}
	background = button.AnimateColor("background", background, pickerTransition)
	foreground = button.AnimateColor("foreground", foreground, pickerTransition)
	button.TextColor(foreground).Draw(func(p *ui.Painter, r ui.Rect) {
		p.Fill(r, background, r.H/2)
		if filled {
			paintGlassRim(p, r, r.H/2, .09)
		}
		if button.FocusVisible() {
			p.Stroke(ui.Rect{X: r.X + 1, Y: r.Y + 1, W: r.W - 2, H: r.H - 2}, c.Theme().Accent.Alpha(.6), r.H/2-1, 1)
		}
	})
	return button
}

func glassButton(c *ui.Context, label string) ui.Element {
	button := ui.Button(c, label).Height(28).Padding(0, 10).FontSize(11).FontWeight(500).Radius(14).Border(0, ui.Transparent).Background(ui.Transparent).FocusRing(false)
	return controlSurface(c, button, true)
}

func glassAction(c *ui.Context, icon, label string) ui.Element {
	button := ui.Button(c, "").Height(28).Padding(0, 10).Radius(14).Border(0, ui.Transparent).Background(ui.Transparent).FocusRing(false).Label(label)
	controlSurface(c, button, true).Children(func() {
		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, icons[icon]).Size(12, 12).Shrink(0)
			ui.Text(c, label).FontSize(11).FontWeight(500).SingleLine()
		})
	})
	return button
}

func quietIcon(c *ui.Context, icon, label string) ui.Element {
	button := ui.Button(c, "").Size(28, 28).Padding(0).Radius(14).Border(0, ui.Transparent).Background(ui.Transparent).FocusRing(false).Label(label)
	controlSurface(c, button, false).Children(func() {
		ui.Icon(c, icons[icon]).Size(14, 14).Attach(ui.AnchorCenter, ui.AnchorCenter)
	})
	return button
}
