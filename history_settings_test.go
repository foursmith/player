package main

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/foursmith/player/internal/library"
	"github.com/foursmith/player/internal/media"
	"github.com/foursmith/player/internal/projection"
)

func TestHistoryControlsResumeAndRecordingIndependently(t *testing.T) {
	a := modeTestApplication(t)
	a.preferences.AutoResume = true
	entry := a.entries[0]
	key := library.Identity(entry)
	saved := videoHistory{Position: 42, Projection: projection.SBS180, ProjectionManual: true}
	a.stored.History[key] = saved
	a.preferences.ResumePlaybackPosition = false
	if start := a.prepareVideoMode(entry); start != 0 || !a.modeManual || a.mode != projection.SBS180 {
		t.Fatal("disabling position restoration discarded the saved video type or resumed playback")
	}
	a.preferences.ResumePlaybackPosition = true
	if start := a.prepareVideoMode(entry); start != 42 {
		t.Fatal("saved position was not restored")
	}
	a.preferences.RecordPlaybackHistory = false
	a.stored.LastFile = entry.Path
	a.updateHistoryRecording()
	if start := a.prepareVideoMode(entry); start != 0 || a.modeManual || a.stored.LastFile != "" {
		t.Fatal("disabled history was used to restore a video")
	}
	a.recordPlaybackHistory(entry, media.State{URL: entry.Path, Position: 100})
	if a.stored.History[key] != saved {
		t.Fatal("disabling recording overwrote or deleted existing history")
	}
	a.preferences.RecordPlaybackHistory = true
	a.updateHistoryRecording()
	if a.stored.LastFile != entry.Path {
		t.Fatal("reenabling history did not restore startup tracking")
	}
}

func TestRecentFilesRespectMenuPreferenceAndIndependentHistorySwitch(t *testing.T) {
	a := &application{preferences: defaultPreferences()}
	first := library.Entry{Path: filepath.Join(t.TempDir(), "first.mp4")}
	second := library.Entry{Path: filepath.Join(t.TempDir(), "second.mp4")}
	if !a.rememberRecentFile(first) || !a.rememberRecentFile(second) {
		t.Fatal("enabled recent-file preference did not record played local files")
	}
	if !a.rememberRecentFile(first) || a.stored.RecentFiles[0] != first.Path || len(a.stored.RecentFiles) != 2 {
		t.Fatal("replaying a file did not move it to the top without duplication")
	}
	a.preferences.RecentFilesEnabled = false
	before := append([]string(nil), a.stored.RecentFiles...)
	if a.rememberRecentFile(second) || !reflect.DeepEqual(before, a.stored.RecentFiles) {
		t.Fatal("disabled recent files still recorded playback or discarded the old list")
	}
	a.preferences.RecordPlaybackHistory = false
	a.preferences.RecentFilesEnabled = true
	if !a.rememberRecentFile(second) {
		t.Fatal("playback-history switch incorrectly disabled the separate recent-file preference")
	}
	if a.rememberRecentFile(library.Entry{Path: "https://nas/video.mp4", DeviceID: "nas"}) {
		t.Fatal("DLNA URL was added to local recent files")
	}
}

func TestHistorySettingsDependenciesAcrossLocales(t *testing.T) {
	if err := loadAssets(); err != nil {
		t.Fatal(err)
	}
	for _, locale := range []string{"zh-CN", "en", "ja"} {
		t.Run(locale, func(t *testing.T) {
			a := modeTestApplication(t)
			a.preferences.Locale = locale
			a.settingsTab = settingsGeneralTab
			view := ui.NewTester(a.settings, 720, 560)
			click := func(key string) {
				t.Helper()
				if err := view.Click(a.t(key)); err != nil {
					t.Fatal(err)
				}
			}
			click("settings.recordHistory")
			click("settings.resumePosition")
			if a.preferences.RecordPlaybackHistory || !a.preferences.ResumePlaybackPosition {
				t.Fatal("disabled resume switch remained interactive")
			}
			click("settings.recentFiles")
			if a.preferences.RecentFilesEnabled {
				t.Fatal("recent-file switch did not turn off")
			}
			click("settings.recentFiles")
			if !a.preferences.RecentFilesEnabled {
				t.Fatal("recent-file switch did not turn on")
			}
		})
	}
}

func TestClearingHistoryAlsoRemovesPreservedRecentFilesAcrossRestart(t *testing.T) {
	a := modeTestApplication(t)
	// Turning the recent menu off retains the old list; clearing history
	// must erase that list too, so startup or reenabling cannot replay it.
	a.preferences.RecentFilesEnabled = false
	a.stored.RecentFiles = []string{filepath.Join(t.TempDir(), "old.mp4")}
	a.stored.LastFile = a.stored.RecentFiles[0]
	cacheKey := library.HashedIdentity("old")
	saved := videoHistory{Position: 42, AutoProjection: "sbs_180_eqr"}
	a.stored.History[cacheKey] = saved
	a.clearRecentFiles()
	path := filepath.Join(t.TempDir(), "state.json")
	if err := saveState(path, a.stored); err != nil {
		t.Fatal(err)
	}
	reloaded := loadState(path)
	if reloaded.History[cacheKey] != saved || len(reloaded.RecentFiles) != 0 || reloaded.LastFile != "" {
		t.Fatalf("recent history survived clearing or the resume cache was lost: %+v", reloaded)
	}
	if reloaded.Preferences.RecentFilesEnabled {
		t.Fatal("clearing history changed the recent menu preference")
	}
}
