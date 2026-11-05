package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func (a *application) openSettings() {
	if a.settingsWindow != nil {
		if a.settingsWindow.IsMinimized() {
			a.settingsWindow.Restore()
		}
		a.settingsWindow.Show()
		a.settingsWindow.Focus()
		return
	}
	a.settingsWindow = mygo.NewWindow(mygo.WindowOptions{
		Title: a.t("common.settings"), Width: 760, Height: 560,
		MinWidth: 720, MinHeight: 420, UseContentSize: true,
		TitleBarStyle: mygo.TitleBarHiddenInset, BackgroundColor: "#0b0c0e",
		DisableFullScreen: true, DisableMaximize: true,
		Parent: a.window, StateKey: "settings", Hidden: true,
		Content: ui.View(a.settings),
	})
	window := a.settingsWindow
	window.SetWindowButtonsTheme(mygo.ThemeDark)
	window.OnReadyToShow(window.Show)
	window.OnFocus(window.Invalidate)
	window.OnBlur(func() { a.setShortcutRecording("") })
	window.OnClosed(func() {
		a.settingsWindow = nil
		a.setShortcutRecording("")
	})
}

const (
	settingsPlaybackTab = iota
	settingsVRTab
	settingsShortcutsTab
	settingsGeneralTab
	settingsAboutTab
	settingsTabCount
)

var settingsTabs = [...]struct{ title, icon string }{
	{"settings.playback", "play"},
	{"settings.vr", "columns"},
	{"settings.shortcutsTab", "keyboard"},
	{"settings.general", "settings"},
	{"settings.about", "info"},
}

func (a *application) settings(c *ui.Context) {
	transparentTheme(c)
	ui.Row(c.Key("settings")).Fill().Background(ui.Hex("#0b0c0e")).Children(func() {
		ui.Column(c.Key("settings-sidebar")).Width(184).Shrink(0).FillHeight().Background(white(.025)).Children(func() {
			ui.Box(c.Key("settings-sidebar-titlebar")).FillWidth().Height(max(52, c.TitleBar().Height)).DragWindow()
			a.settingsNavigation(c)
		})
		ui.Box(c).Width(1).FillHeight().Background(white(.07))
		ui.Column(c.Key("settings-detail")).Grow(1).MinWidth(0).FillHeight().Children(func() {
			ui.Row(c.Key("settings-titlebar")).FillWidth().Height(max(52, c.TitleBar().Height)).Padding(0, 20).AlignItems(ui.Center).DragWindow().Children(func() {
				title := settingsTabs[a.settingsTab].title
				if a.settingsTab == settingsShortcutsTab {
					title = "settings.shortcuts"
				}
				ui.Text(c, a.t(title)).FontSize(18).FontWeight(600)
			})
			ui.Scroll(c.Key(a.settingsTab)).FillWidth().Grow(1).MinHeight(0).Padding(12, 20, 20).TrackScroll(&a.settingsScroll[a.settingsTab]).Children(func() {
				switch a.settingsTab {
				case settingsPlaybackTab:
					a.playbackSettings(c)
				case settingsVRTab:
					a.vrSettings(c)
				case settingsShortcutsTab:
					a.shortcutSettings(c)
				case settingsGeneralTab:
					a.generalSettings(c)
				case settingsAboutTab:
					ui.Column(c).FillWidth().Radius(16).Background(white(.04)).Children(func() { a.about(c) })
				}
			})
		})
	})
	if a.settingsTab != settingsShortcutsTab {
		a.setShortcutRecording("")
	}
	if a.shortcutRecording == "" && c.Shortcut(0, ui.KeyEscape) && a.settingsWindow != nil {
		a.settingsWindow.Close()
	}
}

func (a *application) settingsNavigation(c *ui.Context) {
	tabs := ui.TabsBase(c.Key("settings-tabs"), &a.settingsTab, len(settingsTabs))
	tabs.List.FillWidth().Grow(1).MinHeight(0).Label(a.t("common.settings")).FocusGroup(ui.Vertical).Children(func() {
		ui.Column(c).Fill().Padding(4, 12, 16).Gap(4).Children(func() {
			for i, tab := range settingsTabs {
				button := tabs.Tab(i).FillWidth().Height(40).Padding(0, 12).Gap(10).AlignItems(ui.Center).Radius(12).Background(ui.Transparent).Label(a.t(tab.title))
				color := white(.56)
				if a.settingsTab == i {
					button.Background(accent.Alpha(.12))
					color = accent
				} else if button.Hovered() {
					button.Background(white(.05))
					color = white(.9)
				}
				button.Children(func() {
					ui.Icon(c, icons[tab.icon]).Size(16, 16).TextColor(color)
					ui.Text(c, a.t(tab.title)).FontSize(12).FontWeight(600).TextColor(color).SingleLine().Grow(1)
				})
			}
		})
	})
}

func (a *application) playbackSettings(c *ui.Context) {
	ui.Column(c).FillWidth().Background(white(.04)).Radius(16).Children(func() {
		a.qualityMatrix(c)
		if a.toggleSetting(c, "settings.resumeLastVideo", "settings.resumeLastVideoDescription", "play", &a.preferences.AutoResume, !a.preferences.RecordPlaybackHistory) {
			a.updateHistoryRecording()
		}
	})
}

func (a *application) vrSettings(c *ui.Context) {
	a.sectionTitle(c, "settings.layout")
	ui.Column(c).FillWidth().Background(white(.04)).Radius(16).Children(func() {
		a.segmentSetting(c, "settings.portraitLayout", "settings.portraitLayoutDescription", "columns", []string{"1", "2", "3"}, a.preferences.SplitCount-1, func(i int) { a.preferences.SplitCount = i + 1 })
		a.segmentSetting(c, "settings.preferredSplitAspect", "settings.preferredSplitAspectDescription", "corners-in", []string{"1:1", "9:16"}, map[bool]int{false: 0, true: 1}[a.preferences.PreferredSplitAspect == "9:16"], func(i int) { a.preferences.PreferredSplitAspect = []string{"1:1", "9:16"}[i] })
	})
	a.sectionTitle(c, "settings.interaction")
	ui.Column(c).FillWidth().Radius(16).Background(white(.04)).Children(func() {
		a.numberSetting(c, "settings.dragSensitivity", "settings.dragSensitivityDescription", "sliders", &a.preferences.VRDragSensitivity, .25, 3, .05, "×", false)
	})
	a.sectionTitle(c, "settings.recognition")
	ui.Column(c).FillWidth().Radius(16).Background(white(.04)).Children(func() {
		if a.toggleSetting(c, "settings.projectionAI", "settings.projectionAIDescription", "file-video", &a.preferences.ProjectionAI, false) {
			a.restartAutomaticDetection()
		}
	})
	a.sectionTitle(c, "settings.centering")
	ui.Column(c).FillWidth().Radius(16).Background(white(.04)).Children(func() {
		if a.toggleSetting(c, "settings.faceCentering", "settings.faceCenteringDescription", "scan-face", &a.preferences.FaceAutoCenter, false) {
			a.persist()
		}
		if a.toggleSetting(c, "settings.resumeAfterMovement", "settings.resumeAfterMovementDescription", "rotate-ccw", &a.preferences.ResumeAfterMovement, !a.preferences.FaceAutoCenter) {
			a.persist()
		}
		a.numberSetting(c, "settings.centeringDelay", "settings.centeringDelayDescription", "rotate-ccw", &a.preferences.CenteringResumeDelay, .25, 5, .25, " s", !a.preferences.FaceAutoCenter || !a.preferences.ResumeAfterMovement)
	})
}

func (a *application) sectionTitle(c *ui.Context, key string) {
	smallText(c, a.t(key), .42).FontSize(9).FontWeight(600).Padding(6, 12, 6)
}
func (a *application) toggleSetting(c *ui.Context, title, description, icon string, value *bool, disabled bool) bool {
	row := ui.Row(c.Key(title)).FillWidth().Height(52).Padding(4, 12, 4, 12).Gap(12)
	if disabled {
		row.Opacity(.42)
	}
	var clicked bool
	row.Children(func() {
		settingIcon(c, icon)
		ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
			ui.Text(c, a.t(title)).FontSize(12).FontWeight(600).TextColor(white(.92))
			if description != "" {
				smallText(c, a.t(description), .48).FontSize(11).SingleLine()
			}
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
		clicked = switchButton.Clicked()
	})
	clicked = row.Clicked() || clicked
	if clicked && !disabled {
		*value = !*value
		a.tracker.Reset()
		return true
	}
	return false
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

func (a *application) numberSetting(c *ui.Context, title, description, icon string, value *float64, lo, hi, step float64, suffix string, disabled bool) {
	row := ui.Row(c.Key(title)).FillWidth().Height(52).Padding(4, 12).Gap(12)
	if disabled {
		row.Opacity(.42)
	}
	row.Children(func() {
		settingIcon(c, icon)
		ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
			ui.Text(c, a.t(title)).FontSize(12).FontWeight(600).TextColor(white(.92))
			smallText(c, a.t(description), .48).FontSize(11).SingleLine()
		})
		slider := ui.SliderBase(c, value, lo, hi).Step(step).Width(100).Height(24).Disabled(disabled).Label(a.t(title))
		slider.Draw(func(p *ui.Painter, r ui.Rect) { paintGlassTrack(p, r, float32((*value-lo)/(hi-lo))) })
		smallText(c, strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.2f", *value), "0"), ".")+suffix, .68).Width(42).TextAlign(ui.End)
		if slider.Changed() {
			a.persist()
		}
	})
}

func (a *application) generalSettings(c *ui.Context) {
	ui.Column(c).FillWidth().Radius(16).Background(white(.04)).Children(func() {
		selected := map[string]int{"zh-CN": 0, "en": 1, "ja": 2}[a.preferences.Locale]
		a.segmentSetting(c, "language.title", "language.description", "translate", []string{"简体中文", "English", "日本語"}, selected, func(i int) { a.preferences.Locale = []string{"zh-CN", "en", "ja"}[i] })
	})
	a.historySettings(c)
	a.sectionTitle(c, "settings.data")
	ui.Column(c).FillWidth().Radius(16).Background(white(.04)).Children(func() {
		a.settingsAction(c, "settings.clearHistory", "settings.clearHistoryDescription", "trash", func() {
			a.confirmSettingsAction("settings.clearHistory", "settings.clearHistoryConfirm", a.clearRecentFiles)
		})
		a.settingsAction(c, "settings.resetPreferences", "settings.resetPreferencesDescription", "rotate-ccw", func() {
			a.confirmSettingsAction("settings.resetPreferences", "settings.resetPreferencesConfirm", a.resetPreferences)
		})
	})
}

func (a *application) historySettings(c *ui.Context) {
	a.sectionTitle(c, "settings.history")
	var resumeChanged, recordingChanged, recentChanged bool
	ui.Column(c.Key("history-settings")).FillWidth().Radius(16).Background(white(.04)).Children(func() {
		resumeChanged = a.toggleSetting(c, "settings.resumePosition", "", "rotate-ccw", &a.preferences.ResumePlaybackPosition, !a.preferences.RecordPlaybackHistory)
		ui.Divider(c).Margin(0, 12, 0, 56).Background(white(.07))
		recordingChanged = a.toggleSetting(c, "settings.recordHistory", "", "file-video", &a.preferences.RecordPlaybackHistory, false)
		ui.Divider(c).Margin(0, 12, 0, 56).Background(white(.07))
		recentChanged = a.toggleSetting(c, "settings.recentFiles", "settings.recentFilesDescription", "playlist", &a.preferences.RecentFilesEnabled, false)
	})
	if recordingChanged {
		a.updateHistoryRecording()
	} else if resumeChanged || recentChanged {
		a.persist()
	}
	if recentChanged {
		a.syncRecentFilesVisibility()
	}
}

func (a *application) settingsAction(c *ui.Context, title, description, icon string, action func()) {
	ui.Row(c.Key(title)).FillWidth().Padding(12).Gap(12).AlignItems(ui.Center).Children(func() {
		settingIcon(c, icon)
		ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
			ui.Text(c, a.t(title)).FontSize(12).FontWeight(600)
			smallText(c, a.t(description), .48).FontSize(11).Wrap()
		})
		button := ui.Button(c, a.t(title)).Height(28).FontSize(10).Padding(0, 10).Radius(10).Border(0, ui.Transparent).Background(white(.07)).Disabled(a.dialogOpen)
		if button.Clicked() {
			action()
		}
	})
}
