package main

import (
	"strings"

	"github.com/egoist/mygo/ui"
)

func (a *application) shortcutSettings(c *ui.Context) {
	ui.Row(c.Key("shortcuts-help")).FillWidth().Padding(0, 12, 10).Gap(16).AlignItems(ui.Center).Children(func() {
		smallText(c, a.t("shortcuts.editDescription"), .42).FontSize(10).Grow(1).MinWidth(0).Wrap()
		button := glassButton(c, a.t("shortcuts.resetAll")).FontSize(10).Height(26).Disabled(len(a.preferences.Shortcuts) == 0)
		if !button.IsDisabled() && (button.Hovered() || button.FocusVisible()) {
			button.TextColor(white(.9))
		}
		if button.Clicked() {
			a.preferences.Shortcuts = nil
			a.setShortcutRecording("")
			a.persist()
		}
	})
	if a.shortcutError != "" {
		ui.Text(c.Key("shortcuts-error"), a.shortcutError).FontSize(11).TextColor(ui.Hex("#f0b178")).Wrap().FillWidth().Padding(0, 0, 12)
	}
	settingsGroup(c.Key("shortcut-bindings")).Padding(4, 12).Children(func() {
		for _, def := range playbackShortcuts {
			ui.Row(c.Key(def.id)).FillWidth().Height(38).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, a.t(def.label)).FontSize(11).TextColor(white(.78)).SingleLine().Grow(1).MinWidth(0)
				var labels []string
				for _, chord := range a.preferences.shortcutKeys(def) {
					labels = append(labels, chord.String())
				}
				label := strings.Join(labels, " / ")
				if a.shortcutRecording == def.id {
					label = a.t("shortcuts.recording")
				}
				button := glassButton(c, label).MinWidth(92).MaxWidth(200).Height(26).FontSize(10).Label(a.t("shortcuts.edit", a.t(def.label)))
				button.HandleInput(func(event ui.InputEvent) bool { return a.captureShortcut(def.id, event) })
				if a.shortcutRecording == def.id {
					button.Background(accent.Alpha(.14)).TextColor(accent)
					if !button.Focused() {
						a.setShortcutRecording("")
					}
				}
				if button.Clicked() {
					if a.shortcutRecording == def.id {
						a.setShortcutRecording("")
					} else {
						a.setShortcutRecording(def.id)
						button.Focus()
					}
				}
				_, customized := a.preferences.Shortcuts[def.id]
				reset := quietIcon(c, "rotate-ccw", a.t("shortcuts.reset", a.t(def.label))).Size(24, 24).OnClick(func() { a.resetShortcut(def) })
				reset.Disabled(!customized)
			})
		}
	})
}
