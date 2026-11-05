package main

import (
	"fmt"
	"maps"
	"runtime"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/foursmith/player/internal/projection"
)

type keyChord struct {
	Key  ui.Key       `json:"key"`
	Mods ui.Modifiers `json:"modifiers"`
}

type playbackShortcut struct {
	id, label string
	keys      []keyChord
	run       func(*application)
}

var playbackShortcuts []playbackShortcut

func init() {
	keys := func(key ui.Key) []keyChord { return []keyChord{{Key: key}} }
	shiftAlias := func(key ui.Key) []keyChord { return []keyChord{{Key: key}, {Key: key, Mods: ui.Shift}} }
	seek := func(seconds float64) func(*application) {
		return func(a *application) { a.player.Seek(a.player.Snapshot().Position + seconds) }
	}
	zoom := func(scale float64) func(*application) {
		return func(a *application) { a.zoom(scale, float64(a.width/max(1, a.height))) }
	}
	definitions := []playbackShortcut{
		{"playPause", "shortcuts.playPause", keys(ui.KeySpace), func(a *application) { a.player.Toggle() }},
		{"seekBack10", "shortcuts.seekBack10", keys(ui.KeyLeft), seek(-10)},
		{"seekForward10", "shortcuts.seekForward10", keys(ui.KeyRight), seek(10)},
		{"seekBack60", "shortcuts.seekBack60", []keyChord{{Key: ui.KeyLeft, Mods: ui.Shift}}, seek(-60)},
		{"seekForward60", "shortcuts.seekForward60", []keyChord{{Key: ui.KeyRight, Mods: ui.Shift}}, seek(60)},
		{"volumeUp", "shortcuts.volumeUp", keys(ui.KeyUp), func(a *application) { a.setVolume(a.preferences.Volume + .05) }},
		{"volumeDown", "shortcuts.volumeDown", keys(ui.KeyDown), func(a *application) { a.setVolume(a.preferences.Volume - .05) }},
		{"muteUnmute", "shortcuts.muteUnmute", shiftAlias(ui.KeyM), (*application).mute},
		{"fullscreen", "shortcuts.fullscreen", shiftAlias(ui.KeyF), func(a *application) { a.window.ToggleFullScreen() }},
		{"resetView", "shortcuts.resetView", shiftAlias(ui.KeyR), (*application).resetView},
		{"zoomOut", "shortcuts.zoomOut", []keyChord{{Key: ui.KeyBracketLeft}, {Key: ui.KeyMinus}}, zoom(1 / 1.1)},
		{"zoomIn", "shortcuts.zoomIn", []keyChord{{Key: ui.KeyBracketRight}, {Key: ui.KeyEqual}}, zoom(1.1)},
	}
	labels := []string{"sbs180Eqr", "sbs180Fe", "mono360Eqr", "mono180Eqr", "mono180Fe", "tb360Eqr", "flat2d"}
	for i, id := range projection.IDs {
		definitions = append(definitions, playbackShortcut{
			id: "projection." + id, label: "projection.options." + labels[i], keys: keys(ui.Key1 + ui.Key(i)),
			run: func(a *application) { a.setMode(projection.Mode(i)) },
		})
	}
	playbackShortcuts = definitions
}

func (p preferences) shortcutKeys(def playbackShortcut) []keyChord {
	if chord, ok := p.Shortcuts[def.id]; ok {
		return []keyChord{chord}
	}
	return def.keys
}

func findPlaybackShortcut(id string) *playbackShortcut {
	for i := range playbackShortcuts {
		if playbackShortcuts[i].id == id {
			return &playbackShortcuts[i]
		}
	}
	return nil
}

func (k keyChord) valid() bool {
	return k.Key > ui.KeyUnknown && k.Key <= ui.KeyZ && k.Mods & ^(ui.Ctrl|ui.Shift|ui.Alt|ui.Super) == 0
}

func (k keyChord) reserved() bool {
	if runtime.GOOS != "darwin" && (k.Mods&ui.Super != 0 || k.Key == ui.KeyF11 || k.Key == ui.KeySpace && k.Mods&ui.Alt != 0) {
		return true
	}
	if k.Key == ui.KeyEscape || k.Key == ui.KeyTab || k.Key == ui.KeyF12 {
		return true
	}
	// Keep application menus, editing and OS window commands available.
	if k.Mods&ui.Cmd != 0 {
		switch k.Key {
		case ui.KeyO, ui.KeyA, ui.KeyD, ui.KeyW, ui.KeyQ, ui.KeyH, ui.KeyM,
			ui.KeyC, ui.KeyV, ui.KeyX, ui.KeyZ, ui.KeyComma, ui.KeyL, ui.KeyRight:
			return true
		case ui.KeyF:
			return runtime.GOOS == "darwin" && k.Mods&ui.Ctrl != 0
		case ui.KeyI:
			return k.Mods&(ui.Alt|ui.Shift) != 0
		}
	}
	return runtime.GOOS != "darwin" && k.Key == ui.KeyF4 && k.Mods&ui.Alt != 0
}

// shortcutConflict includes aliases, so a remapping never steals another
// action's secondary key. Empty means the combination is available.
func (p preferences) shortcutConflict(id string, chord keyChord) string {
	if !chord.valid() || chord.reserved() {
		return "reserved"
	}
	for _, def := range playbackShortcuts {
		if def.id == id {
			continue
		}
		for _, other := range p.shortcutKeys(def) {
			if chord == other {
				return def.id
			}
		}
	}
	return ""
}

func (p *preferences) normalizeShortcuts() {
	for id, chord := range p.Shortcuts {
		if findPlaybackShortcut(id) == nil || !chord.valid() || chord.reserved() {
			delete(p.Shortcuts, id)
		}
	}
	// Dropping an invalid override restores its defaults; check again in
	// case another override was using one of those restored aliases.
	for {
		var invalid []string
		for _, def := range playbackShortcuts {
			if chord, ok := p.Shortcuts[def.id]; ok && p.shortcutConflict(def.id, chord) != "" {
				invalid = append(invalid, def.id)
			}
		}
		if len(invalid) == 0 {
			break
		}
		for _, id := range invalid {
			delete(p.Shortcuts, id)
		}
	}
	if len(p.Shortcuts) == 0 {
		p.Shortcuts = nil
	}
}

func (a *application) shortcutConflictMessage(conflict string) string {
	if conflict == "reserved" {
		return a.t("shortcuts.reserved")
	}
	if def := findPlaybackShortcut(conflict); def != nil {
		return a.t("shortcuts.conflict", a.t(def.label))
	}
	return ""
}

func (a *application) setShortcut(id string, chord keyChord) bool {
	if findPlaybackShortcut(id) == nil {
		return false
	}
	if conflict := a.preferences.shortcutConflict(id, chord); conflict != "" {
		a.shortcutError = a.shortcutConflictMessage(conflict)
		return false
	}
	overrides := maps.Clone(a.preferences.Shortcuts)
	if overrides == nil {
		overrides = map[string]keyChord{}
	}
	overrides[id] = chord
	a.preferences.Shortcuts = overrides
	a.setShortcutRecording("")
	a.persist()
	return true
}

func (a *application) resetShortcut(def playbackShortcut) {
	candidate := a.preferences
	candidate.Shortcuts = maps.Clone(candidate.Shortcuts)
	delete(candidate.Shortcuts, def.id)
	for _, chord := range def.keys {
		if conflict := candidate.shortcutConflict(def.id, chord); conflict != "" {
			a.shortcutError = a.shortcutConflictMessage(conflict)
			return
		}
	}
	if len(candidate.Shortcuts) == 0 {
		candidate.Shortcuts = nil
	}
	a.preferences = candidate
	a.setShortcutRecording("")
	a.persist()
}

func (a *application) setShortcutRecording(id string) {
	changed := a.shortcutRecording != id
	a.shortcutRecording, a.shortcutError = id, ""
	if changed && a.window != nil {
		a.refreshMenu()
	}
}

func (a *application) captureShortcut(id string, event ui.InputEvent) bool {
	if a.shortcutRecording != id || event.Kind != ui.InputKeyDown {
		return false
	}
	if event.Key == ui.KeyEscape {
		a.setShortcutRecording("")
		return true
	}
	key := event.Key
	if key == ui.KeyUnknown && event.Mods != 0 {
		key = event.PhysicalKey
	}
	if key != ui.KeyUnknown && !event.Repeat {
		a.setShortcut(id, keyChord{Key: key, Mods: event.Mods})
	}
	return true
}

func (k keyChord) String() string {
	var parts []string
	if k.Mods&ui.Ctrl != 0 {
		parts = append(parts, "Ctrl")
	}
	if k.Mods&ui.Alt != 0 {
		parts = append(parts, "Alt")
	}
	if k.Mods&ui.Shift != 0 {
		parts = append(parts, "Shift")
	}
	if k.Mods&ui.Super != 0 {
		if runtime.GOOS == "darwin" {
			parts = append(parts, "⌘")
		} else {
			parts = append(parts, "Win")
		}
	}
	var label string
	switch {
	case k.Key >= ui.KeyA && k.Key <= ui.KeyZ:
		label = string(rune('A' + k.Key - ui.KeyA))
	case k.Key >= ui.Key0 && k.Key <= ui.Key9:
		label = string(rune('0' + k.Key - ui.Key0))
	case k.Key >= ui.KeyF1 && k.Key <= ui.KeyF12:
		label = fmt.Sprintf("F%d", k.Key-ui.KeyF1+1)
	default:
		label = shortcutKeyLabels[k.Key]
	}
	parts = append(parts, label)
	return strings.Join(parts, " + ")
}

var shortcutKeyLabels = map[ui.Key]string{
	ui.KeySpace: "Space", ui.KeyEnter: "Return", ui.KeyBackspace: "⌫", ui.KeyDelete: "Delete", ui.KeyInsert: "Insert",
	ui.KeyHome: "Home", ui.KeyEnd: "End", ui.KeyPageUp: "Page Up", ui.KeyPageDown: "Page Down",
	ui.KeyLeft: "←", ui.KeyRight: "→", ui.KeyUp: "↑", ui.KeyDown: "↓",
	ui.KeyMinus: "−", ui.KeyEqual: "=", ui.KeyComma: ",", ui.KeyPeriod: ".", ui.KeySlash: "/", ui.KeySemicolon: ";",
	ui.KeyQuote: "'", ui.KeyBracketLeft: "[", ui.KeyBracketRight: "]", ui.KeyBackslash: "\\", ui.KeyBackquote: "`", ui.KeyContextMenu: "Menu",
}
