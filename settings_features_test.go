package main

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/foursmith/player/internal/library"
	"github.com/foursmith/player/internal/media"
	"github.com/foursmith/player/internal/projection"
)

func TestNewPreferencesRoundTripAndValidation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	state := loadState(path)
	state.Preferences.VRDragSensitivity = 2
	state.Preferences.CenteringResumeDelay = 2.5
	state.Preferences.ProjectionAI = false
	state.Preferences.ResumePlaybackPosition = false
	state.Preferences.RecordPlaybackHistory = false
	state.Preferences.RecentFilesEnabled = false
	state.Preferences.QuitOnWindowClose = false
	state.Preferences.Shortcuts = map[string]keyChord{"playPause": {Key: ui.KeyP, Mods: ui.Shift}}
	if err := saveState(path, state); err != nil {
		t.Fatal(err)
	}
	if got := loadState(path); !reflect.DeepEqual(got, state) {
		t.Fatalf("new settings did not round trip: %+v", got.Preferences)
	}
	state.Preferences.VRDragSensitivity = 100
	state.Preferences.CenteringResumeDelay = -1
	state.Preferences.Shortcuts["volumeUp"] = keyChord{Key: ui.KeyLeft}
	state.Preferences.Shortcuts["unknown"] = keyChord{Key: ui.KeyK}
	state.Preferences.Shortcuts["resetView"] = keyChord{Key: ui.KeyEscape}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	got := loadState(path).Preferences
	if got.VRDragSensitivity != 3 || got.CenteringResumeDelay != .25 || len(got.Shortcuts) != 1 {
		t.Fatalf("invalid settings were retained: %+v", got)
	}
}

func TestVRDragUsesConfiguredSensitivityAndResumeDelay(t *testing.T) {
	a := modeTestApplication(t)
	a.mode, a.debugOpen = projection.SBS180, true
	a.view = projection.DefaultView()
	a.preferences.VRDragSensitivity = 2
	a.preferences.CenteringResumeDelay = 2.5
	a.stageInput(ui.InputEvent{Kind: ui.InputPointerDown, Clicks: 1}, 1.5, false)
	before := time.Now()
	a.stageInput(ui.InputEvent{Kind: ui.InputPointerMove, X: 10, Y: 5}, 1.5, false)
	if math.Abs(a.view.Yaw-1.6) > .0001 || math.Abs(a.view.Pitch-.8) > .0001 {
		t.Fatalf("drag sensitivity ignored: %+v", a.view)
	}
	if delay := a.manualUntil.Sub(before); delay < 2500*time.Millisecond || delay > 2600*time.Millisecond {
		t.Fatalf("resume delay ignored: %v", delay)
	}
}

func TestAISettingRestartsAutomaticDetectionAndPreservesManualSelection(t *testing.T) {
	if err := loadAssets(); err != nil {
		t.Fatal(err)
	}
	a := modeTestApplication(t)
	a.settingsTab = settingsVRTab
	canceled := false
	a.modeDetection = &modeDetectionTask{cancel: func() { canceled = true }}
	view := ui.NewTester(a.settings, 720, 560)
	if err := view.Click(a.t("settings.projectionAI")); err != nil {
		t.Fatal(err)
	}
	if a.preferences.ProjectionAI || !canceled || a.modeDetection != nil || !a.detectMode {
		t.Fatal("AI setting did not cancel and restart automatic detection")
	}
	canceled = false
	a.modeManual = true
	a.modeDetection = &modeDetectionTask{cancel: func() { canceled = true }}
	if err := view.Click(a.t("settings.projectionAI")); err != nil {
		t.Fatal(err)
	}
	if !a.preferences.ProjectionAI || !a.modeManual || canceled {
		t.Fatal("AI setting replaced a manual type")
	}
}

func TestClearRecentFilesPreservesPlaybackCache(t *testing.T) {
	a := modeTestApplication(t)
	entry := a.entries[0]
	key := library.Identity(entry)
	saved := videoHistory{Position: 42, AutoProjection: "sbs_180_eqr", ProjectionManual: true, Projection: projection.SBS180}
	a.stored.History[key] = saved
	a.stored.LastPlayback = &savedPlayback{Path: entry.Path}
	a.stored.RecentMedia = []savedPlayback{{Path: entry.Path}}
	a.clearRecentFiles()
	if a.stored.History[key] != saved || a.stored.LastPlayback != nil || len(a.stored.RecentMedia) != 0 {
		t.Fatalf("clearing recent files changed playback cache or left visible history: %+v", a.stored)
	}
	a.recordPlaybackHistory(entry, media.State{URL: entry.Path, Position: 100})
	if a.stored.History[key].Position != 100 || len(a.stored.RecentMedia) != 0 {
		t.Fatal("clearing recent files interrupted resume caching or repopulated recent history")
	}
}

func TestResetPreferencesKeepsPlaybackHistory(t *testing.T) {
	a := modeTestApplication(t)
	history := videoHistory{Position: 42, AutoProjection: "sbs_180_eqr"}
	a.stored.History["saved"] = history
	a.preferences.Locale = "ja"
	a.preferences.RecordPlaybackHistory = false
	a.preferences.RecentFilesEnabled = false
	a.preferences.ProjectionAI = false
	a.preferences.VRDragSensitivity = 2
	a.preferences.QuitOnWindowClose = false
	a.preferences.Shortcuts = map[string]keyChord{"playPause": {Key: ui.KeyP}}
	a.resetPreferences()
	if !reflect.DeepEqual(a.preferences, defaultPreferences()) || a.stored.History["saved"] != history {
		t.Fatal("reset did not restore preferences while preserving history")
	}
	if a.stored.LastPlayback != nil {
		t.Fatal("reset kept a plaintext startup path with startup restoration disabled")
	}
}
