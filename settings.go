package main

import (
	"fmt"
	"github.com/egoist/mygo/ui"
	"github.com/foursmith/player/internal/projection"
	"time"
)

func (a *application) settings(c *ui.Context) {
	backdrop := ui.Box(c.Key("settings-overlay")).Absolute().Left(0).Right(0).Top(0).Bottom(0).Background(ui.Transparent).Transition(ui.ElementTransition{Duration: 180 * time.Millisecond, Enter: &ui.Motion{}, Exit: &ui.Motion{}})
	if backdrop.Clicked() {
		a.settingsOpen = false
	}
	backdrop.Children(func() {
		panel := ui.Box(c.Key("settings-content")).Background(ui.Hex("#0b0c0e")).Radius(24).Width(min(448, a.width-32)).MaxHeight(a.height-32).Attach(ui.AnchorCenter, ui.AnchorCenter).Role(ui.RoleDialog).Label(a.t("common.settings")).Focusable().AutoFocus().Transition(ui.ElementTransition{Duration: 220 * time.Millisecond, Enter: &ui.Motion{Y: 12, Scale: .96}, Exit: &ui.Motion{Y: 12, Scale: .96}})
		if a.width < 640 {
			panel.Attach(ui.AnchorBottom, ui.AnchorBottom).Width(a.width - 16).Transition(ui.ElementTransition{Duration: 320 * time.Millisecond, Enter: &ui.Motion{Y: a.height}, Exit: &ui.Motion{Y: a.height}})
		}
		panel.Children(func() {
			ui.Scroll(c).FillWidth().MaxHeight(a.height-16).Padding(12, 10, 10).Children(func() {
				ui.Row(c).FillWidth().Padding(12, 10).Children(func() {
					ui.Text(c, a.t("common.settings")).FontSize(16).FontWeight(600).Grow(1)
					a.iconControl(c, "x", a.t("settings.close"), iconStyle{32, 16, subtleIcon}, false, func() { a.settingsOpen = false })
				})
				a.sectionTitle(c, "settings.playback")
				ui.Column(c).FillWidth().Background(white(.04)).Radius(16).Children(func() {
					a.qualityMatrix(c)
					if a.selected < 0 || a.mode.IsVR() {
						a.segmentSetting(c, "settings.portraitLayout", "settings.portraitLayoutDescription", "columns", []string{"1", "2", "3"}, a.preferences.SplitCount-1, func(i int) { a.preferences.SplitCount = i + 1 })
						a.segmentSetting(c, "settings.preferredSplitAspect", "settings.preferredSplitAspectDescription", "corners-in", []string{"1:1", "9:16"}, map[bool]int{false: 0, true: 1}[a.preferences.PreferredSplitAspect == "9:16"], func(i int) { a.preferences.PreferredSplitAspect = []string{"1:1", "9:16"}[i] })
					}
					a.toggleSetting(c, "settings.resumeLastVideo", "settings.resumeLastVideoDescription", "play", &a.preferences.AutoResume, false)
					if a.width >= 640 {
						ui.Divider(c).Background(white(.07))
						button := ui.Button(c, "").FillWidth().Height(52).Padding(4, 12).Background(ui.Transparent).Border(0, ui.Transparent).Label(a.t("settings.shortcuts"))
						button.Children(func() {
							ui.Row(c).Fill().Gap(12).Children(func() {
								settingIcon(c, "keyboard")
								ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
									ui.Text(c, a.t("settings.shortcuts")).FontSize(12).FontWeight(600)
									smallText(c, a.t("settings.shortcutsDescription"), .48).FontSize(11).SingleLine()
								})
								ui.Icon(c, icons["caret-down"]).Size(14, 14).TextColor(white(.42))
							})
						})
						if button.Clicked() {
							a.shortcutsOpen = !a.shortcutsOpen
						}
						if a.shortcutsOpen {
							a.shortcutHelp(c)
						}
					}
				})
				if a.selected < 0 || a.mode.IsVR() {
					a.sectionTitle(c, "settings.centering")
					ui.Column(c).FillWidth().Radius(16).Background(white(.04)).Children(func() {
						a.toggleSetting(c, "settings.faceCentering", "settings.faceCenteringDescription", "scan-face", &a.preferences.FaceAutoCenter, false)
						a.toggleSetting(c, "settings.resumeAfterMovement", "settings.resumeAfterMovementDescription", "rotate-ccw", &a.preferences.ResumeAfterMovement, !a.preferences.FaceAutoCenter)
					})
				}
				a.sectionTitle(c, "settings.more")
				ui.Column(c).FillWidth().Radius(16).Background(white(.04)).Children(func() {
					selected := map[string]int{"zh-CN": 0, "en": 1, "ja": 2}[a.preferences.Locale]
					a.segmentSetting(c, "language.title", "language.description", "translate", []string{"简体中文", "English", "日本語"}, selected, func(i int) { a.preferences.Locale = []string{"zh-CN", "en", "ja"}[i] })
					ui.Divider(c).Background(white(.07))
					a.about(c)
				})
			})
		})
	})
}
func (a *application) sectionTitle(c *ui.Context, key string) {
	smallText(c, a.t(key), .42).FontSize(9).FontWeight(600).Padding(6, 12, 6)
}
func (a *application) toggleSetting(c *ui.Context, title, description, icon string, value *bool, disabled bool) {
	row := ui.Row(c.Key(title)).FillWidth().Height(52).Padding(4, 12, 4, 12).Gap(12)
	if disabled {
		row.Opacity(.42)
	}
	row.Children(func() {
		settingIcon(c, icon)
		ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
			ui.Text(c, a.t(title)).FontSize(12).FontWeight(600).TextColor(white(.92))
			smallText(c, a.t(description), .48).FontSize(11).SingleLine()
		})
		switchButton := ui.Button(c, "").Size(56, 44).Background(ui.Transparent).Border(0, ui.Transparent).Radius(22).Disabled(disabled).Role(ui.RoleSwitch).Label(a.t(title)).Checked(*value)
		target := float32(0)
		if *value {
			target = 1
		}
		progress := switchButton.Spring("checked", target, ui.SpringOptions{Duration: 320 * time.Millisecond, Bounce: .3})
		switchButton.Draw(func(p *ui.Painter, r ui.Rect) {
			track := ui.Rect{X: r.X + (r.W-36)/2, Y: r.Y + (r.H-20)/2, W: 36, H: 20}
			fill := ui.RGBA(0, 0, 0, .2)
			border := white(.14)
			if *value {
				fill = accent
				border = accent
			}
			p.Fill(track, fill, 10)
			p.Stroke(track, border, 10, 1)
			diameter := 14 + 2.52*progress
			p.Fill(ui.Rect{X: track.X + 2 + 16*progress, Y: track.Y + 10 - diameter/2, W: diameter, H: diameter}, white(1), diameter/2)
		})
		if switchButton.Clicked() {
			*value = !*value
			a.tracker.Reset()
			a.persist()
		}
	})
	if row.Clicked() && !disabled {
		*value = !*value
		a.tracker.Reset()
		a.persist()
	}
}
func (a *application) segmentSetting(c *ui.Context, title, description, icon string, options []string, selected int, onChange func(int)) {
	ui.Row(c.Key(title)).FillWidth().Height(52).Padding(4, 12, 4, 12).Gap(12).Children(func() {
		settingIcon(c, icon)
		ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
			ui.Text(c, a.t(title)).FontSize(12).FontWeight(600).TextColor(white(.92))
			smallText(c, a.t(description), .48).FontSize(11).SingleLine()
		})
		ui.Row(c).Padding(4).Radius(16).Background(ui.RGBA(0, 0, 0, .24)).Children(func() {
			for i, label := range options {
				buttonLabel := label
				if title == "settings.preferredSplitAspect" {
					buttonLabel = ""
				}
				button := ui.Button(c, buttonLabel).Height(24).MinWidth(26).Padding(0, 8).FontSize(10).Radius(12).Border(0, ui.Transparent).Background(ui.Transparent).TextColor(white(.46))
				if title == "language.title" {
					button.Height(32).Width(54).Padding(0, 2)
				}
				if title == "settings.preferredSplitAspect" {
					button.Width(44).Label(label).Draw(func(p *ui.Painter, r ui.Rect) {
						w, h := float32(14), float32(14)
						if i == 1 {
							w, h = 9, 16
						}
						color := white(.46)
						if i == selected {
							color = ui.Hex("#062d36")
						}
						rect := ui.Rect{X: r.X + (r.W-w)/2, Y: r.Y + (r.H-h)/2, W: w, H: h}
						p.Fill(rect, color.Alpha(.1), 3)
						p.Stroke(rect, color, 3, 2)
					})
				}
				if i == selected {
					button.Background(accent).TextColor(ui.Hex("#062d36"))
				}
				if button.Clicked() {
					onChange(i)
					a.persist()
				}
			}
		})
	})
}
func (a *application) shortcutHelp(c *ui.Context) {
	definitions := []struct{ key, label string }{{"Space", "shortcuts.playPause"}, {"←", "shortcuts.seekBack10"}, {"→", "shortcuts.seekForward10"}, {"Shift + ←", "shortcuts.seekBack60"}, {"Shift + →", "shortcuts.seekForward60"}, {"↑", "shortcuts.volumeUp"}, {"↓", "shortcuts.volumeDown"}, {"M", "shortcuts.muteUnmute"}, {"F", "shortcuts.fullscreen"}, {"R", "shortcuts.resetView"}, {"[ / −", "shortcuts.zoomOut"}, {"] / +", "shortcuts.zoomIn"}, {",", "shortcuts.previousQuality"}, {".", "shortcuts.nextQuality"}}
	for i := range projection.Labels {
		label := a.projectionLabel(i)
		definitions = append(definitions, struct{ key, label string }{fmt.Sprint(i + 1), label})
	}
	ui.Column(c).FillWidth().Padding(8, 16).Children(func() {
		for i := 0; i < len(definitions); i += 2 {
			ui.Row(c).FillWidth().Gap(16).Children(func() {
				for j := i; j < min(i+2, len(definitions)); j++ {
					d := definitions[j]
					ui.Row(c).Grow(1).MinWidth(0).Height(35).Gap(8).Children(func() {
						label := d.label
						if len(label) > 10 && label[:10] == "shortcuts." {
							label = a.t(label)
						}
						smallText(c, label, .66).FontSize(10).SingleLine().Grow(1)
						smallText(c, d.key, .72).FontSize(9).Background(white(.06)).Radius(6).Padding(4, 6)
					})
				}
			})
		}
	})
}

var matrixColors = [...]ui.Color{ui.Hex("#58bde2"), ui.Hex("#5ed59a"), ui.Hex("#efa557"), ui.Hex("#9d74f7")}

func (a *application) qualityMatrix(c *ui.Context) {
	ui.Column(c.Key("quality-matrix")).FillWidth().Padding(12).Gap(6).Children(func() {
		ui.Row(c).FillWidth().Children(func() {
			smallText(c, a.t("settings.renderer"), .78).Width(62).FontSize(11)
			for _, key := range []string{"low", "medium", "high", "ultra"} {
				ui.Box(c).Grow(1).Center().Children(func() { smallText(c, a.t("settings.quality."+key), .42).FontSize(9).NoWrap() })
			}
		})
		ui.Column(c).FillWidth().Height(132).Draw(func(p *ui.Painter, r ui.Rect) {
			p.Fill(ui.Rect{X: r.X + 62, Y: r.Y, W: r.W - 62, H: r.H}, ui.Hex("#202326"), 20)
		}).Children(func() {
			for row, fps := range []int{24, 30, 60} {
				active := row == a.preferences.FrameRateID-1
				color := matrixColors[a.preferences.QualityID]
				ui.Row(c.Key(row)).FillWidth().Height(44).Children(func() {
					label := smallText(c, fmt.Sprintf("%d fps", fps), .42).Width(62).FontSize(10)
					if active {
						label.TextColor(color)
					}
					ui.Row(c).Grow(1).Height(44).Draw(func(p *ui.Painter, r ui.Rect) {
						if !active {
							return
						}
						fraction := (float32(a.preferences.QualityID) + .5) / 4
						x := r.X + r.W*fraction
						band := ui.Rect{X: r.X + 6, Y: r.Y + 8, W: max(0, x-r.X-6), H: 28}
						p.FillGradient(band, ui.LinearGradient{Angle: 180, Stops: []ui.GradientStop{{Color: color, At: 0}, {Color: color, At: 1}}}, 14)
						thumb := ui.Rect{X: x - 16, Y: r.Y + 6, W: 32, H: 32}
						p.Fill(thumb, ui.Hex("#f2f2f2"), 16)
						p.Stroke(thumb, white(.72), 16, 1)
					}).Children(func() {
						for col := range 4 {
							label := a.t("settings.quality." + []string{"low", "medium", "high", "ultra"}[col])
							button := ui.Button(c.Key(col), "").Grow(1).Height(44).Padding(0).Radius(12).Background(ui.Transparent).Border(0, ui.Transparent).Role(ui.RoleRadio).Checked(active && col == a.preferences.QualityID).Label(fmt.Sprintf("%s, %d fps", label, fps))
							button.Draw(func(p *ui.Painter, r ui.Rect) {
								if active && col == a.preferences.QualityID {
									return
								}
								dotColor := ui.Hex("#595c5e")
								if active && col < a.preferences.QualityID {
									dotColor = white(1)
								}
								p.Fill(ui.Rect{X: r.X + r.W/2 - 3, Y: r.Y + r.H/2 - 3, W: 6, H: 6}, dotColor, 3)
							})
							if button.Clicked() {
								a.preferences.QualityID = col
								a.preferences.FrameRateID = row + 1
								a.persist()
							}
						}
					})
				})
			}
		})
	})
}

func settingIcon(c *ui.Context, icon string) {
	ui.Box(c).Size(32, 32).Background(white(.08)).Radius(12).Center().Children(func() { ui.Icon(c, icons[icon]).Size(16, 16).TextColor(white(.78)) })
}
