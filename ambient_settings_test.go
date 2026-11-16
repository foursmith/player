package main

import (
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestAmbientLightSettingDefaultsPersistsAndApplies(t *testing.T) {
	if err := loadAssets(); err != nil {
		t.Fatal(err)
	}
	for _, locale := range []string{"zh-CN", "en", "ja"} {
		t.Run(locale, func(t *testing.T) {
			a := modeTestApplication(t)
			a.preferences.Locale = locale
			if !a.preferences.AmbientLight || a.params(false).DisableAmbientLight {
				t.Fatal("ambient light is not enabled by default")
			}
			view := ui.NewTester(a.settings, 720, 420)
			if err := view.Click(a.t("settings.ambientLight")); err != nil {
				t.Fatal(err)
			}
			if a.preferences.AmbientLight || !a.params(false).DisableAmbientLight || !a.params(true).DisableAmbientLight {
				t.Fatal("switch did not disable main and PiP rendering")
			}
			path := filepath.Join(t.TempDir(), "state.json")
			if err := saveState(path, a.stored); err != nil {
				t.Fatal(err)
			}
			if loadState(path).Preferences.AmbientLight {
				t.Fatal("explicitly disabled light did not survive reload")
			}
			if err := view.Click(a.t("settings.ambientLight")); err != nil {
				t.Fatal(err)
			}
			if !a.preferences.AmbientLight || a.params(false).DisableAmbientLight {
				t.Fatal("switch did not enable ambient light again")
			}
		})
	}
}
