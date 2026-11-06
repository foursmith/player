package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/foursmith/player/internal/library"
	"github.com/foursmith/player/internal/media"
)

func TestAppearancePreferenceDefaultsAndRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	for _, value := range []mygo.ThemeSource{"", "unknown", mygo.ThemeSystem, mygo.ThemeDark, mygo.ThemeLight} {
		t.Run(string(value), func(t *testing.T) {
			data := fmt.Sprintf(`{"Preferences":{"appearance":%q}}`, value)
			if value == "" {
				data = `{"Preferences":{}}`
			}
			if err := os.WriteFile(path, []byte(data), 0600); err != nil {
				t.Fatal(err)
			}
			state := loadState(path)
			want := mygo.ThemeDark
			if value == mygo.ThemeLight || value == mygo.ThemeSystem {
				want = value
			}
			if state.Preferences.Appearance != want {
				t.Fatalf("appearance = %q, want %q", state.Preferences.Appearance, want)
			}
			if err := saveState(path, state); err != nil {
				t.Fatal(err)
			}
			if got := loadState(path).Preferences.Appearance; got != want {
				t.Fatalf("appearance after restart = %q, want %q", got, want)
			}
		})
	}
}

func TestAppearanceSwitchAcrossLocales(t *testing.T) {
	if err := loadAssets(); err != nil {
		t.Fatal(err)
	}
	for _, locale := range []string{"en", "zh-CN", "ja"} {
		t.Run(locale, func(t *testing.T) {
			a := modeTestApplication(t)
			a.preferences.Locale = locale
			a.settingsTab = settingsGeneralTab
			view := ui.NewTester(a.settings, 720, 420)
			for _, choice := range []struct {
				key        string
				appearance mygo.ThemeSource
			}{
				{"settings.light", mygo.ThemeLight}, {"settings.dark", mygo.ThemeDark}, {"settings.system", mygo.ThemeSystem}, {"settings.light", mygo.ThemeLight},
			} {
				if err := view.Click(a.t(choice.key)); err != nil {
					t.Fatal(err)
				}
				if a.preferences.Appearance != choice.appearance || a.stored.Preferences.Appearance != choice.appearance {
					t.Fatal("theme choice was not applied and saved")
				}
				var libraryDark bool
				ui.NewTester(func(c *ui.Context) {
					a.catalogView(c)
					libraryDark = c.Theme().Dark
				}, 800, 460)
				if libraryDark != (choice.appearance == mygo.ThemeDark) {
					t.Fatal("library retained the previous theme")
				}
			}
			a.resetPreferences()
			if a.preferences.Appearance != mygo.ThemeDark {
				t.Fatal("reset did not restore the default appearance")
			}
		})
	}
}

func TestSystemAppearanceUpdatesOpenViewsAndKeepsManualOverride(t *testing.T) {
	if err := loadAssets(); err != nil {
		t.Fatal(err)
	}
	a := modeTestApplication(t)
	a.settingsTab = settingsGeneralTab
	var settingsDark, libraryDark bool
	settings := ui.NewTester(func(c *ui.Context) {
		a.settings(c)
		settingsDark = c.Theme().Dark
	}, 720, 420)
	library := ui.NewTester(func(c *ui.Context) {
		a.catalogView(c)
		libraryDark = c.Theme().Dark
	}, 800, 460)
	if err := settings.Click(a.t("settings.system")); err != nil {
		t.Fatal(err)
	}
	for _, dark := range []bool{false, true, false} {
		settings.SetDark(dark)
		library.SetDark(dark)
		if settingsDark != dark || libraryDark != dark {
			t.Fatalf("open views did not follow system appearance: system dark=%v, settings=%v, library=%v", dark, settingsDark, libraryDark)
		}
		if a.preferences.Appearance != mygo.ThemeSystem || a.stored.Preferences.Appearance != mygo.ThemeSystem {
			t.Fatal("effective appearance replaced the saved system preference")
		}
	}
	for _, choice := range []struct {
		key  string
		dark bool
	}{{"settings.dark", true}, {"settings.light", false}} {
		if err := settings.Click(a.t(choice.key)); err != nil {
			t.Fatal(err)
		}
		for _, systemDark := range []bool{true, false} {
			settings.SetDark(systemDark)
			library.SetDark(systemDark)
			if settingsDark != choice.dark || libraryDark != choice.dark {
				t.Fatal("system change replaced the manual appearance")
			}
		}
	}
}

func TestAppearanceSurfaceSnapshots(t *testing.T) {
	if os.Getenv("FSVR_UI_SNAPSHOT_DIR") == "" {
		t.Skip("diagnostic snapshots are opt-in")
	}
	if err := loadAssets(); err != nil {
		t.Fatal(err)
	}
	for _, appearance := range []mygo.ThemeSource{mygo.ThemeDark, mygo.ThemeLight} {
		for _, locale := range []string{"en", "zh-CN", "ja"} {
			a := modeTestApplication(t)
			a.preferences.Appearance, a.preferences.Locale = appearance, locale
			for tab := range settingsTabs {
				a.settingsTab = tab
				for _, size := range [][2]int{{720, 420}, {760, 560}} {
					a.settingsScroll[tab] = ui.ScrollState{}
					view := ui.NewTester(a.settings, size[0], size[1])
					view.SetScale(2)
					writeUISnapshot(t, fmt.Sprintf("%s-%s-settings-%d-%d.png", appearance, locale, tab, size[0]), view)
				}
			}
			a.stored.Libraries = []library.Saved{{ID: "appearance-preview", Name: "Weekend videos", Paths: []string{"/Media/Clouds.mp4"}}}
			a.catalog.selected, a.catalog.name = "appearance-preview", "Weekend videos"
			a.catalog.items = []library.SavedItem{{Entry: library.Entry{Name: "Clouds.mp4", Path: "/Media/Clouds.mp4"}}}
			a.filterCatalog()
			view := ui.NewTester(a.catalogView, 920, 600)
			view.SetScale(2)
			writeUISnapshot(t, fmt.Sprintf("%s-%s-library.png", appearance, locale), view)
		}
	}
}

func TestPlaybackAppearancePanels(t *testing.T) {
	if err := loadAssets(); err != nil {
		t.Fatal(err)
	}
	for _, appearance := range []mygo.ThemeSource{mygo.ThemeDark, mygo.ThemeLight} {
		t.Run(string(appearance), func(t *testing.T) {
			a := modeTestApplication(t)
			a.preferences.Appearance, a.preferences.Locale = appearance, "zh-CN"
			a.playlistExpanded = true
			state := media.State{URL: "current.mp4", Position: 90, Metadata: media.Metadata{Duration: 600}}
			view := ui.NewTester(func(c *ui.Context) {
				a.usePlayerTheme(c, true)
				a.width, a.height = c.Size()
				ui.Box(c).Fill().Background(ui.Hex("#344552")).Children(func() {
					ui.Themed(c, darkPlayerTheme).Absolute().Left(24).Right(24).Bottom(24).Padding(16).Bind(&a.controlsPanel).Children(func() {
						ui.Row(c).FillWidth().Children(func() {
							ui.Spacer(c).Grow(1)
							a.iconButton(c, "sliders", a.t("controls.adjustVolumeSpeed"), 36, true, func() {}).Bind(&a.adjustmentsButton)
						})
						a.timeline(c, state)
					})
					a.playlist(c, state)
					a.topControls(c)
					a.adjustments(c)
				})
			}, 1000, 720)
			view.Frame()
			for _, key := range []string{"playlist.title", "common.settings", "media.closeVideo", "controls.volume", "controls.speed", "media.addFiles", "media.addFolder"} {
				if bounds, ok := view.Find(a.t(key)); !ok || bounds.W <= 0 || bounds.H <= 0 || bounds.X < 0 || bounds.Y < 0 || bounds.X+bounds.W > 1000 || bounds.Y+bounds.H > 720 {
					t.Fatalf("playback control is missing or clipped: %s %+v", key, bounds)
				}
			}
			view.SetScale(2)
			writeUISnapshot(t, string(appearance)+"-playback-panels.png", view)
		})
	}
}
