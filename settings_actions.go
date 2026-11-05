package main

import (
	"time"

	"github.com/egoist/mygo"
)

func (p preferences) centeringDelay() time.Duration {
	return time.Duration(p.CenteringResumeDelay * float64(time.Second))
}

func (a *application) refreshWindows() {
	for _, window := range []*mygo.Window{a.window, a.pipWindow, a.settingsWindow} {
		if window != nil {
			window.Invalidate()
		}
	}
}

func (a *application) restartAutomaticDetection() {
	if !a.modeManual && a.player != nil && a.selected >= 0 && a.selected < len(a.entries) {
		a.redetectMode()
		return
	}
	a.persist()
}

func (a *application) resetPreferences() {
	previous := a.preferences
	a.preferences = defaultPreferences()
	a.setShortcutRecording("")
	a.lastAudible = a.preferences.Volume
	if a.player != nil {
		a.player.SetVolume(a.preferences.Volume)
		a.player.SetRate(a.preferences.PlaybackRate)
	}
	a.tracker.Reset()
	a.manuallyPaused = false
	a.manualUntil = time.Time{}
	if previous.ProjectionAI != a.preferences.ProjectionAI {
		a.restartAutomaticDetection()
	} else {
		a.persist()
	}
	if previous.RecentFilesEnabled != a.preferences.RecentFilesEnabled {
		a.syncRecentFilesVisibility()
	}
}

func (a *application) updateHistoryRecording() {
	a.stored.LastFile = ""
	if a.preferences.RecordPlaybackHistory && a.preferences.AutoResume && a.selected >= 0 && a.selected < len(a.entries) && a.entries[a.selected].DeviceID == "" {
		a.stored.LastFile = a.entries[a.selected].Path
	}
	a.persist()
}

func (a *application) confirmSettingsAction(title, detail string, action func()) {
	if a.dialogOpen {
		return
	}
	a.dialogOpen = true
	parent := a.settingsWindow
	if parent == nil {
		parent = a.window
	}
	options := mygo.MessageOptions{
		Parent: parent, Type: mygo.MessageWarning, Title: a.t(title), Message: a.t(title), Detail: a.t(detail),
		Buttons: []string{a.t("common.cancel"), a.t(title)}, DefaultButton: 0, CancelButton: 0, DestructiveButtons: []int{1},
	}
	go func() {
		result, err := mygo.Dialog.Message(options)
		a.window.Update(func() {
			a.dialogOpen = false
			if a.shutdown {
				return
			}
			if err != nil {
				a.notice = err.Error()
			} else if result.Button == 1 {
				action()
			}
			a.refreshWindows()
		})
	}()
}
