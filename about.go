package main

import (
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func (a *application) about(c *ui.Context) {
	ui.Row(c).FillWidth().Padding(12).Gap(12).Children(func() {
		ui.Image(c, logo).Size(40, 40)
		ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
			ui.Text(c, appInfo.Name).FontSize(12).FontWeight(600).SingleLine()
			smallText(c, a.t("common.tagline"), .48).SingleLine()
		})
		ui.Text(c, "v"+appInfo.Version).FontSize(8).TextColor(white(.68)).Background(ui.Hex("#34383c")).Padding(0, 6).Radius(3)
	})
	ui.Divider(c).Background(white(.07))
	ui.Row(c).FillWidth().Padding(10, 12).Gap(4).Children(func() {
		smallText(c, a.t("settings.by"), .42)
		linkButton(c, "ourongxing", "https://github.com/ourongxing")
		smallText(c, "·", .24)
		linkButton(c, "MPL 2.0", "https://github.com/foursmith/player/blob/main/LICENSE")
		ui.Spacer(c).Grow(1)
		a.aboutAction(c, "bug", a.t("settings.debug"), a.debugOpen, func() { a.debugOpen = !a.debugOpen })
		a.aboutAction(c, "github", "GitHub", false, func() { go mygo.Shell.OpenExternal("https://github.com/foursmith/player") })
	})
}
func linkButton(c *ui.Context, label, url string) ui.Element {
	button := ui.Button(c, label).Height(22).Padding(0, 2).FontSize(10).Border(0, ui.Transparent).Background(ui.Transparent).TextColor(white(.68)).Cursor(ui.CursorPointer)
	if button.Clicked() {
		go mygo.Shell.OpenExternal(url)
	}
	return button
}
func (a *application) aboutAction(c *ui.Context, icon, label string, pressed bool, action func()) {
	button := ui.Button(c, "").Height(24).Padding(4, 7).Radius(12).Border(0, ui.Transparent).Background(ui.Transparent).Label(label)
	color := white(.42)
	if pressed {
		button.Background(accent.Alpha(.1))
		color = accent
	}
	button.Children(func() {
		ui.Row(c).Gap(6).Children(func() {
			ui.Icon(c, icons[icon]).Size(14, 14).TextColor(color)
			ui.Text(c, label).FontSize(10).TextColor(color)
		})
	})
	if button.Clicked() {
		action()
	}
}
