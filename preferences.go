package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"

	"github.com/egoist/mygo"
	"github.com/foursmith/player/internal/library"
	"github.com/foursmith/player/internal/projection"
)

type preferences struct {
	Volume                 float64             `json:"volume"`
	PlaybackRate           float64             `json:"playbackRate"`
	SplitCount             int                 `json:"splitCount"`
	PreferredSplitAspect   string              `json:"preferredSplitAspect"`
	FaceAutoCenter         bool                `json:"faceAutoCenter"`
	ResumeAfterMovement    bool                `json:"resumeFaceAutoCenterAfterViewChange"`
	VRDragSensitivity      float64             `json:"vrDragSensitivity"`
	CenteringResumeDelay   float64             `json:"centeringResumeDelay"`
	ProjectionAI           bool                `json:"projectionAI"`
	AutoResume             bool                `json:"autoResumePlayback"`
	ResumePlaybackPosition bool                `json:"resumePlaybackPosition"`
	RecordPlaybackHistory  bool                `json:"recordPlaybackHistory"`
	RecentFilesEnabled     bool                `json:"recentFilesEnabled"`
	LibraryAutoRefresh     bool                `json:"libraryAutoRefresh"`
	LibrarySort            int                 `json:"librarySort"`
	LibraryFilter          int                 `json:"libraryFilter"`
	SubtitlesEnabled       bool                `json:"subtitlesEnabled"`
	RepeatMode             string              `json:"repeatMode"`
	Locale                 string              `json:"locale"`
	Appearance             mygo.ThemeSource    `json:"appearance"`
	QuitOnWindowClose      bool                `json:"quitOnWindowClose"`
	Shortcuts              map[string]keyChord `json:"shortcuts,omitempty"`
}
type videoHistory struct {
	Projection       projection.Mode `json:",omitempty"`
	AutoProjection   string          `json:",omitempty"`
	Position         float64
	UpdatedAt        int64
	Duration         float64 `json:",omitempty"`
	Watched          bool    `json:",omitempty"`
	WatchOverride    string  `json:",omitempty"`
	ProjectionManual bool    `json:",omitempty"`
}

func (h videoHistory) automaticProjection() (projection.Mode, bool) {
	if index := slices.Index(projection.IDs[:], h.AutoProjection); index >= 0 {
		return projection.Mode(index), true
	}
	return projection.Flat, false
}

type storedState struct {
	Preferences preferences
	History     map[string]videoHistory
	LastFile    string
	RecentFiles []string
	Libraries   []library.Saved
	DLNAUUID    string `json:",omitempty"`
}

func defaultPreferences() preferences {
	return preferences{
		Volume: 1, PlaybackRate: 1,
		SplitCount: 2, PreferredSplitAspect: "1:1", FaceAutoCenter: true, ResumeAfterMovement: true,
		VRDragSensitivity: 1, CenteringResumeDelay: 1, ProjectionAI: true,
		ResumePlaybackPosition: true, RecordPlaybackHistory: true, RecentFilesEnabled: true,
		SubtitlesEnabled: true, RepeatMode: "file", Locale: "en", Appearance: mygo.ThemeDark, LibraryAutoRefresh: true,
		QuitOnWindowClose: true,
	}
}
func loadState(path string) storedState {
	state := storedState{Preferences: defaultPreferences(), History: map[string]videoHistory{}}
	data, err := os.ReadFile(path)
	if err == nil {
		candidate := storedState{Preferences: defaultPreferences(), History: map[string]videoHistory{}}
		if json.Unmarshal(data, &candidate) == nil {
			state = candidate
		}
	}
	p := &state.Preferences
	p.Volume = projection.Clamp(p.Volume, 0, 1)
	p.PlaybackRate = projection.Clamp(p.PlaybackRate, .25, 4)
	p.SplitCount = max(1, min(3, p.SplitCount))
	p.VRDragSensitivity = projection.Clamp(p.VRDragSensitivity, .25, 3)
	p.CenteringResumeDelay = projection.Clamp(p.CenteringResumeDelay, .25, 5)
	p.LibrarySort = max(0, min(catalogSortCount-1, p.LibrarySort))
	p.LibraryFilter = max(0, min(catalogFilterCount-1, p.LibraryFilter))
	p.normalizeShortcuts()
	if p.PreferredSplitAspect != "9:16" {
		p.PreferredSplitAspect = "1:1"
	}
	if p.Locale != "zh-CN" && p.Locale != "ja" {
		p.Locale = "en"
	}
	if p.Appearance != mygo.ThemeLight && p.Appearance != mygo.ThemeSystem {
		p.Appearance = mygo.ThemeDark
	}
	if p.RepeatMode != "file" && p.RepeatMode != "folder" {
		p.RepeatMode = "off"
	}
	if state.History == nil {
		state.History = map[string]videoHistory{}
	}
	for key, history := range state.History {
		if !library.ValidIdentity(key) || history.Projection < 0 || history.Projection > projection.Flat || history.Position < 0 {
			delete(state.History, key)
			continue
		}
		if _, valid := history.automaticProjection(); history.AutoProjection != "" && !valid {
			history.AutoProjection = ""
		}
		if history.Duration < 0 {
			history.Duration = 0
		}
		if history.WatchOverride != "watched" && history.WatchOverride != "unwatched" {
			history.WatchOverride = ""
		}
		state.History[key] = history
	}
	if !p.RecordPlaybackHistory || !p.AutoResume {
		state.LastFile = ""
	}
	var recent []string
	for _, path := range state.RecentFiles {
		if filepath.IsAbs(path) && !slices.Contains(recent, path) && len(recent) < 10 {
			recent = append(recent, path)
		}
	}
	state.RecentFiles = recent
	state.Libraries = library.NormalizeSaved(state.Libraries)
	return state
}

func rememberFile(recent []string, path string) []string {
	out := []string{path}
	for _, previous := range recent {
		if previous != path && len(out) < 10 {
			out = append(out, previous)
		}
	}
	return out
}
func saveState(path string, state storedState) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".state-*")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	if err = file.Chmod(0600); err == nil {
		_, err = file.Write(data)
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(name, path)
}
