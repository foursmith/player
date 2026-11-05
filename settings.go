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
		TitleBarStyle: mygo.TitleBarHiddenInset, BackgroundColor: windowBackground,
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
	windowSurface(c.Key("settings")).Children(func() {
		windowSidebar(c.Key("settings-sidebar")).Children(func() {
			ui.Box(c.Key("settings-sidebar-titlebar")).FillWidth().Height(max(48, c.TitleBar().Height)).DragWindow()
			a.settingsNavigation(c)
		})
		ui.Box(c).Width(1).FillHeight().Background(white(.055))
		ui.Column(c.Key("settings-detail")).Grow(1).MinWidth(0).FillHeight().Children(func() {
			ui.Row(c.Key("settings-titlebar")).FillWidth().Height(max(64, c.TitleBar().Height)).Padding(0, max(20, c.TitleBar().Right+8), 0, 20).Gap(12).DragWindow().Children(func() {
				tab := settingsTabs[a.settingsTab]
				title := tab.title
				if a.settingsTab == settingsShortcutsTab {
					title = "settings.shortcuts"
				}
				glass(c, 10).Size(32, 32).Center().Children(func() {
					ui.Icon(c, icons[tab.icon]).Size(17, 17).TextColor(accent.Alpha(.8))
				})
				ui.Text(c, a.t(title)).FontSize(18).FontWeight(600).TextColor(white(.9)).SingleLine().Grow(1).MinWidth(0)
			})
			ui.Scroll(c.Key(a.settingsTab)).FillWidth().Grow(1).MinHeight(0).Padding(8, 20, 20).TrackScroll(&a.settingsScroll[a.settingsTab]).Children(func() {
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
					settingsGroup(c).Children(func() { a.about(c) })
				}
			})
		})
	})
	if a.settingsTab != settingsShortcutsTab {
		a.setShortcutRecording("")
	}
	if c.Shortcut(0, ui.KeyEscape) {
		if a.shortcutRecording != "" {
			a.setShortcutRecording("")
		} else if a.settingsWindow != nil {
			a.settingsWindow.Close()
		}
	}
}

func (a *application) settingsNavigation(c *ui.Context) {
	tabs := ui.TabsBase(c.Key("settings-tabs"), &a.settingsTab, len(settingsTabs))
	tabs.List.FillWidth().Grow(1).MinHeight(0).Label(a.t("common.settings")).FocusGroup(ui.Vertical).Children(func() {
		ui.Column(c).Fill().Padding(2, 10, 16).Gap(3).Children(func() {
			ui.Text(c, a.t("common.settings")).FontSize(10).FontWeight(500).TextColor(white(.4)).Padding(10, 8, 8)
			for i, tab := range settingsTabs {
				navigationStyle(tabs.Tab(i), a.settingsTab == i).Label(a.t(tab.title)).Children(func() {
					ui.Row(c).Fill().Gap(9).Children(func() {
						ui.Icon(c, icons[tab.icon]).Size(14, 14).Shrink(0)
						ui.Text(c, a.t(tab.title)).FontSize(11).FontWeight(500).SingleLine().Grow(1).MinWidth(0)
					})
				})
			}
		})
	})
}

func (a *application) playbackSettings(c *ui.Context) {
	settingsGroup(c).Children(func() {
		if a.toggleSetting(c, "settings.resumeLastVideo", "settings.resumeLastVideoDescription", "play", &a.preferences.AutoResume, !a.preferences.RecordPlaybackHistory) {
			a.updateHistoryRecording()
		}
	})
}

func (a *application) vrSettings(c *ui.Context) {
	a.sectionTitle(c, "settings.layout")
	settingsGroup(c).Children(func() {
		a.segmentSetting(c, "settings.portraitLayout", "settings.portraitLayoutDescription", "columns", []string{"1", "2", "3"}, a.preferences.SplitCount-1, func(i int) { a.preferences.SplitCount = i + 1 })
		a.segmentSetting(c, "settings.preferredSplitAspect", "settings.preferredSplitAspectDescription", "corners-in", []string{"1:1", "9:16"}, map[bool]int{false: 0, true: 1}[a.preferences.PreferredSplitAspect == "9:16"], func(i int) { a.preferences.PreferredSplitAspect = []string{"1:1", "9:16"}[i] })
	})
	a.sectionTitle(c, "settings.interaction")
	settingsGroup(c).Children(func() {
		a.numberSetting(c, "settings.dragSensitivity", "settings.dragSensitivityDescription", "sliders", &a.preferences.VRDragSensitivity, .25, 3, .05, "×", false)
	})
	a.sectionTitle(c, "settings.recognition")
	settingsGroup(c).Children(func() {
		if a.toggleSetting(c, "settings.projectionAI", "settings.projectionAIDescription", "file-video", &a.preferences.ProjectionAI, false) {
			a.restartAutomaticDetection()
		}
	})
	a.sectionTitle(c, "settings.centering")
	settingsGroup(c).Children(func() {
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
	smallText(c, a.t(key), .4).FontSize(10).FontWeight(500).Padding(14, 10, 6)
}
func (a *application) toggleSetting(c *ui.Context, title, description, icon string, value *bool, disabled bool) bool {
	row := ui.Row(c.Key(title)).FillWidth().MinHeight(52).Padding(10, 12).Gap(10)
	if disabled {
		row.Opacity(.42)
	}
	var clicked bool
	row.Children(func() {
		settingIcon(c, icon)
		ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
			ui.Text(c, a.t(title)).FontSize(12).FontWeight(500).TextColor(white(.88))
			if description != "" {
				smallText(c, a.t(description), .4).FontSize(10).FillWidth().Wrap()
			}
		})
		switchButton := ui.Button(c, "").Size(40, 28).Shrink(0).Background(ui.Transparent).Border(0, ui.Transparent).Radius(14).FocusRing(false).Disabled(disabled).Role(ui.RoleSwitch).Label(a.t(title)).Checked(*value)
		target := float32(0)
		fill := white(.09)
		if *value {
			target, fill = 1, accent.Alpha(.85)
		}
		progress := switchButton.Spring("checked", target, ui.SpringOptions{Duration: 280 * time.Millisecond, Bounce: .15})
		fill = switchButton.AnimateColor("fill", fill, pickerTransition)
		switchButton.Draw(func(p *ui.Painter, r ui.Rect) {
			track := ui.Rect{X: r.X + (r.W-32)/2, Y: r.Y + (r.H-18)/2, W: 32, H: 18}
			p.Fill(track, fill, 9)
			p.Stroke(track, white(.1), 9, .75)
			p.Fill(ui.Rect{X: track.X + 2 + 14*progress, Y: track.Y + 2, W: 14, H: 14}, white(.96), 7)
			if switchButton.FocusVisible() {
				p.Stroke(ui.Rect{X: r.X + 1, Y: r.Y + 1, W: r.W - 2, H: r.H - 2}, accent.Alpha(.6), 13, 1)
			}
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
	ui.Row(c.Key(title)).FillWidth().MinHeight(52).Padding(10, 12).Gap(10).Children(func() {
		settingIcon(c, icon)
		ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
			ui.Text(c, a.t(title)).FontSize(12).FontWeight(500).TextColor(white(.88))
			smallText(c, a.t(description), .4).FontSize(10).FillWidth().Wrap()
		})
		ui.Row(c).Padding(3).Radius(15).Background(white(.035)).Children(func() {
			for i, label := range options {
				buttonLabel := label
				if title == "settings.preferredSplitAspect" {
					buttonLabel = ""
				}
				button := ui.Button(c, buttonLabel).Height(24).MinWidth(26).Padding(0, 8).FontSize(10).Radius(12).Border(0, ui.Transparent).Background(ui.Transparent).FocusRing(false)
				if title == "language.title" {
					button.Height(26).Width(54).Padding(0, 2)
				}
				background, foreground := white(0), white(.46)
				if i == selected {
					background, foreground = accent.Alpha(.14), accent
				}
				if button.Hovered() || button.FocusVisible() {
					if i != selected {
						background = white(.06)
					}
					foreground = white(.95)
				}
				background = button.AnimateColor("background", background, pickerTransition)
				foreground = button.AnimateColor("foreground", foreground, pickerTransition)
				button.Background(background).TextColor(foreground)
				aspect := title == "settings.preferredSplitAspect"
				if aspect {
					button.Width(40).Label(label)
				}
				button.Draw(func(p *ui.Painter, r ui.Rect) {
					if i == selected {
						paintGlassRim(p, r, r.H/2, .09)
					}
					if button.FocusVisible() {
						p.Stroke(ui.Rect{X: r.X + 1, Y: r.Y + 1, W: r.W - 2, H: r.H - 2}, accent.Alpha(.6), r.H/2-1, 1)
					}
					if aspect {
						w, h := float32(12), float32(12)
						if i == 1 {
							w, h = 8, 14
						}
						rect := ui.Rect{X: r.X + (r.W-w)/2, Y: r.Y + (r.H-h)/2, W: w, H: h}
						p.Fill(rect, foreground.Alpha(.08), 2)
						p.Stroke(rect, foreground, 2, 1.5)
					}
				})
				if button.Clicked() {
					onChange(i)
					a.persist()
				}
			}
		})
	})
}

func settingsGroup(c *ui.Context) ui.Element {
	return ui.Column(c).FillWidth().Radius(14).Background(white(.025)).Draw(func(p *ui.Painter, r ui.Rect) { paintGlassRim(p, r, 14, .06) })
}

func settingIcon(c *ui.Context, icon string) {
	glass(c, 9).Size(28, 28).Center().Children(func() { ui.Icon(c, icons[icon]).Size(14, 14).TextColor(accent.Alpha(.65)) })
}

func (a *application) numberSetting(c *ui.Context, title, description, icon string, value *float64, lo, hi, step float64, suffix string, disabled bool) {
	row := ui.Row(c.Key(title)).FillWidth().MinHeight(52).Padding(10, 12).Gap(10)
	if disabled {
		row.Opacity(.42)
	}
	row.Children(func() {
		settingIcon(c, icon)
		ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
			ui.Text(c, a.t(title)).FontSize(12).FontWeight(500).TextColor(white(.88))
			smallText(c, a.t(description), .4).FontSize(10).FillWidth().Wrap()
		})
		slider := ui.SliderBase(c, value, lo, hi).Step(step).Width(100).Height(24).Disabled(disabled).Label(a.t(title))
		slider.Draw(func(p *ui.Painter, r ui.Rect) { paintGlassTrack(p, r, float32((*value-lo)/(hi-lo))) })
		label := smallText(c, strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.2f", *value), "0"), ".")+suffix, .68).Width(38).TextAlign(ui.End)
		if suffix == " s" {
			label.Font("monospace")
		}
		if slider.Changed() {
			a.persist()
		}
	})
}

func (a *application) generalSettings(c *ui.Context) {
	settingsGroup(c).Children(func() {
		selected := map[string]int{"zh-CN": 0, "en": 1, "ja": 2}[a.preferences.Locale]
		a.segmentSetting(c, "language.title", "language.description", "translate", []string{"简体中文", "English", "日本語"}, selected, func(i int) { a.preferences.Locale = []string{"zh-CN", "en", "ja"}[i] })
	})
	a.historySettings(c)
	a.sectionTitle(c, "settings.data")
	settingsGroup(c).Children(func() {
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
	settingsGroup(c.Key("history-settings")).Children(func() {
		resumeChanged = a.toggleSetting(c, "settings.resumePosition", "", "rotate-ccw", &a.preferences.ResumePlaybackPosition, !a.preferences.RecordPlaybackHistory)
		ui.Divider(c).Margin(0, 12, 0, 50).Background(white(.045))
		recordingChanged = a.toggleSetting(c, "settings.recordHistory", "", "file-video", &a.preferences.RecordPlaybackHistory, false)
		ui.Divider(c).Margin(0, 12, 0, 50).Background(white(.045))
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
	ui.Row(c.Key(title)).FillWidth().Padding(10, 12).Gap(10).AlignItems(ui.Center).Children(func() {
		settingIcon(c, icon)
		ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
			ui.Text(c, a.t(title)).FontSize(12).FontWeight(500).TextColor(white(.88))
			smallText(c, a.t(description), .4).FontSize(10).FillWidth().Wrap()
		})
		button := glassButton(c, a.t(title)).Height(26).FontSize(10).Disabled(a.dialogOpen)
		if button.Clicked() {
			action()
		}
	})
}
