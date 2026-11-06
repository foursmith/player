package main

import (
	"testing"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func TestWindowCloseSettingAcrossLocales(t *testing.T) {
	if err := loadAssets(); err != nil {
		t.Fatal(err)
	}
	for _, locale := range []string{"zh-CN", "en", "ja"} {
		t.Run(locale, func(t *testing.T) {
			a := modeTestApplication(t)
			a.preferences.Locale = locale
			a.settingsTab = settingsGeneralTab
			view := ui.NewTester(a.settings, 720, 560)
			writeUISnapshot(t, "settings-close-window-"+locale+".png", view)
			label := a.t("settings.quitOnWindowClose")
			for _, key := range []string{"settings.appBehavior", "settings.quitOnWindowClose", "settings.quitOnWindowCloseDescription", "media.showPlayer"} {
				if dictionaries[locale][key] == "" {
					t.Fatalf("missing localized window control: %s", key)
				}
			}
			if !a.preferences.QuitOnWindowClose {
				t.Fatal("closing the player must quit by default")
			}
			if rect, ok := view.Find(label); !ok || rect.X < 200 || rect.Y < 64 || rect.X+rect.W > 720 || rect.Y+rect.H > 560 {
				t.Fatalf("window-close control is outside the general settings pane: %+v", rect)
			}
			for _, want := range []bool{false, true} {
				if err := view.Click(label); err != nil {
					t.Fatal(err)
				}
				if a.preferences.QuitOnWindowClose != want || a.stored.Preferences.QuitOnWindowClose != want {
					t.Fatalf("close behavior was not updated and saved: want quit=%v", want)
				}
			}
		})
	}
}

func TestWindowCloseDoesNotPreventExplicitApplicationQuit(t *testing.T) {
	a := &application{shutdown: true}
	event := &mygo.CloseEvent{}
	a.closePlayerWindow(event)
	if event.DefaultPrevented() {
		t.Fatal("keeping the app running after a window close must not block an explicit app quit")
	}
}
