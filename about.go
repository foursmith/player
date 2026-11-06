package main

import "github.com/egoist/mygo/ui"

func (a *application) about(c *ui.Context) {
	ui.Row(c).FillWidth().Padding(14, 12).Gap(12).Children(func() {
		ui.Image(c, logo).Size(40, 40)
		ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
			ui.Text(c, appInfo.Name).FontSize(12).FontWeight(600).SingleLine()
			smallText(c, a.t("common.tagline"), .4).FontSize(10).FillWidth().Wrap()
		})
		ui.Text(c, "v"+appInfo.Version).FontSize(9).TextColor(textColor(c, .45)).Background(ink(c, .04)).Padding(3, 6).Radius(8)
	})
	ui.Divider(c).Background(ink(c, .045))
	ui.Row(c).FillWidth().Padding(10, 12).Gap(4).Children(func() {
		smallText(c, a.t("settings.by"), .42)
		smallText(c, "ourongxing", .68)
		ui.Spacer(c).Grow(1)
		a.aboutAction(c, "bug", a.t("settings.debug"), a.debugOpen, func() {
			a.debugOpen = !a.debugOpen
			a.refreshWindows()
		})
	})
	ui.Divider(c).Background(ink(c, .045))
	ui.Column(c).FillWidth().Padding(12).Gap(8).Children(func() {
		smallText(c, "Copyright © 2026 ourongxing", .68)
		smallText(c, a.t("settings.licenseNotice"), .5).FillWidth().Wrap()
		smallText(c, a.t("settings.ffmpegNotice"), .5).FillWidth().Wrap()
		ui.Row(c).FillWidth().Gap(8).Children(func() {
			glassButton(c, appInfo.License).Height(24).Padding(0, 8).OnClick(func() {
				c.OpenURL("https://www.gnu.org/licenses/gpl-3.0.html")
			})
			glassButton(c, a.t("settings.sourceCode")).Height(24).Padding(0, 8).OnClick(func() {
				c.OpenURL("https://github.com/foursmith/player")
			})
		})
	})
}
func (a *application) aboutAction(c *ui.Context, icon, label string, pressed bool, action func()) {
	button := glassAction(c, icon, label).Height(24)
	if pressed {
		button.Background(c.Theme().Accent.Alpha(.1)).TextColor(c.Theme().Accent)
	}
	button.OnClick(action)
}
