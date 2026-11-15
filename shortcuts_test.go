package main

import (
	"context"
	"math"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/foursmith/player/internal/media"
)

func TestSeekShortcutsWithTimelineFocus(t *testing.T) {
	encoder, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("FFmpeg fixture encoder unavailable")
	}
	file := filepath.Join(t.TempDir(), "shortcuts.mp4")
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if output, err := exec.CommandContext(ctx, encoder, "-v", "error", "-f", "lavfi", "-i", "color=c=red:s=64x32:r=24:d=180", "-c:v", "libx264", "-threads", "1", "-g", "24", file).CombinedOutput(); err != nil {
		t.Fatalf("fixture: %v: %s", err, output)
	}
	player := media.NewPlayer(nil)
	defer player.Close()
	player.Open(media.Source{URL: file, Start: 80})
	waitRenderState(t, player, func(s media.State) bool { return !s.Loading && s.FrameID != 0 })
	player.Pause()
	waitRenderState(t, player, func(s media.State) bool { return !s.Playing })
	a := &application{player: player, preferences: defaultPreferences()}
	view := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(darkPlayerTheme)
		a.timeline(c, player.Snapshot())
		a.timelineHandle.Focus(c)
		a.playbackKeyboard(c, false)
	}, 640, 120)
	for _, test := range []struct {
		mods  ui.Modifiers
		key   ui.Key
		delta float64
	}{{0, ui.KeyRight, 10}, {0, ui.KeyLeft, -10}, {ui.Shift, ui.KeyRight, 60}, {ui.Shift, ui.KeyLeft, -60}} {
		before := player.Snapshot()
		view.Key(test.mods, test.key)
		after := waitRenderState(t, player, func(s media.State) bool { return s.Generation > before.Generation && !s.Loading })
		if math.Abs(after.Position-before.Position-test.delta) > .2 {
			t.Fatalf("%v + %v sought %.3fs, want %.3fs", test.mods, test.key, after.Position-before.Position, test.delta)
		}
		player.Pause()
		waitRenderState(t, player, func(s media.State) bool { return !s.Playing })
		view.Advance(time.Millisecond)
	}
	before := player.Snapshot()
	a.preferences.Shortcuts = map[string]keyChord{"seekForward10": {Key: ui.KeyP}}
	view.Advance(time.Millisecond)
	view.Key(0, ui.KeyRight)
	player.SetVolume(.5)
	waitRenderState(t, player, func(s media.State) bool { return s.Volume == .5 })
	if player.Snapshot().Generation != before.Generation {
		t.Fatal("timeline kept seeking on a remapped arrow key")
	}
	view.Key(0, ui.KeyP)
	after := waitRenderState(t, player, func(s media.State) bool { return s.Generation > before.Generation && !s.Loading })
	if math.Abs(after.Position-before.Position-10) > .2 {
		t.Fatal("remapped seek did not move ten seconds", before.Position, after.Position)
	}

	// Dragging still previews single frames without sending a playback seek.
	a.preferences.Shortcuts = nil
	a.scrubbing, a.scrubTime, a.seekUI.fineTime = true, 80, 80
	view.Advance(time.Millisecond)
	before = player.Snapshot()
	view.Key(0, ui.KeyRight)
	if math.Abs(a.scrubTime-80-1/before.Metadata.FrameRate) > .0001 {
		t.Fatal("drag preview lost frame stepping", a.scrubTime)
	}
	view.Key(0, ui.KeyLeft)
	if math.Abs(a.scrubTime-80) > .0001 || player.Snapshot().Generation != before.Generation {
		t.Fatal("preview arrows committed a seek or failed to step back")
	}
}

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
		a.playbackKeyboard(c, false)
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
