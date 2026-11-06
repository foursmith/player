package main

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestShortcutRecorderConflictsCancelAndReset(t *testing.T) {
	if err := loadAssets(); err != nil {
		t.Fatal(err)
	}
	a := modeTestApplication(t)
	a.settingsTab = settingsShortcutsTab
	view := ui.NewTester(a.settings, 720, 560)
	edit := a.t("shortcuts.edit", a.t("shortcuts.playPause"))
	if err := view.Click(edit); err != nil {
		t.Fatal(err)
	}
	if a.shortcutRecording != "playPause" {
		t.Fatal("recorder did not start")
	}
	view.Key(0, ui.KeyLeft)
	if a.shortcutError == "" || a.shortcutRecording != "playPause" || len(a.preferences.Shortcuts) != 0 {
		t.Fatalf("conflicting key was accepted or recording stopped: recording=%q error=%q shortcuts=%v", a.shortcutRecording, a.shortcutError, a.preferences.Shortcuts)
	}
	view.Key(ui.Cmd, ui.KeyO)
	if a.shortcutError != a.t("shortcuts.reserved") {
		t.Fatal("menu shortcut was not reserved")
	}
	view.Key(ui.Shift, ui.KeyP)
	if a.shortcutRecording != "" || a.preferences.Shortcuts["playPause"] != (keyChord{Key: ui.KeyP, Mods: ui.Shift}) {
		t.Fatal("shortcut was not recorded")
	}
	if err := view.Click(edit); err != nil {
		t.Fatal(err)
	}
	view.Key(0, ui.KeyEscape)
	if a.shortcutRecording != "" || a.preferences.Shortcuts["playPause"].Key != ui.KeyP {
		t.Fatal("Escape changed the saved binding")
	}
	if err := view.Click(a.t("shortcuts.reset", a.t("shortcuts.playPause"))); err != nil {
		t.Fatal(err)
	}
	if len(a.preferences.Shortcuts) != 0 {
		t.Fatal("reset did not restore the default shortcut")
	}
}

func TestCustomShortcutRunsActionAndReplacesDefault(t *testing.T) {
	a := modeTestApplication(t)
	a.preferences.Volume = .5
	a.preferences.Shortcuts = map[string]keyChord{"volumeUp": {Key: ui.KeyP}}
	view := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(darkPlayerTheme)
		ui.Box(c).Fill().Focusable().AutoFocus()
		a.playbackKeyboard(c)
	}, 320, 200)
	view.Key(0, ui.KeyUp)
	if a.preferences.Volume != .5 {
		t.Fatal("old key still ran a remapped action")
	}
	view.Key(0, ui.KeyP)
	if a.preferences.Volume != .55 {
		t.Fatal("custom key did not run its playback action")
	}
}

func TestShortcutAliasesAndRestoredDefaultsCannotBeStolen(t *testing.T) {
	a := modeTestApplication(t)
	if a.preferences.shortcutConflict("playPause", keyChord{Key: ui.KeyMinus}) != "zoomOut" {
		t.Fatal("secondary default alias was not checked")
	}
	if !a.setShortcut("playPause", keyChord{Key: ui.KeyP}) || !a.setShortcut("volumeUp", keyChord{Key: ui.KeySpace}) {
		t.Fatal("free combinations were rejected")
	}
	a.resetShortcut(*findPlaybackShortcut("playPause"))
	if a.shortcutError == "" || a.preferences.Shortcuts["playPause"].Key != ui.KeyP {
		t.Fatal("restoring a default stole another action's key")
	}
}
