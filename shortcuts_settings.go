package main

import (
	"strings"

	"github.com/egoist/mygo/ui"
)

func (a *application) shortcutSettings(c *ui.Context) {
	ui.Row(c.Key("shortcuts-help")).FillWidth().Padding(0, 12, 10).Gap(16).AlignItems(ui.Center).Children(func() {
		smallText(c, a.t("shortcuts.editDescription"), .42).FontSize(10).Grow(1).MinWidth(0).Wrap()
		button := ui.Button(c, a.t("shortcuts.resetAll")).FontSize(10).FontWeight(400).Height(24).Padding(0).Radius(6).Border(0, ui.Transparent).Background(ui.Transparent).TextColor(white(.56)).Disabled(len(a.preferences.Shortcuts) == 0)
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
	ui.Column(c.Key("shortcut-bindings")).FillWidth().Background(white(.04)).Radius(16).Padding(4, 12).Children(func() {
		for _, def := range playbackShortcuts {
			ui.Row(c.Key(def.id)).FillWidth().Height(42).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, a.t(def.label)).FontSize(11).TextColor(white(.78)).SingleLine().Grow(1).MinWidth(0)
				var labels []string
				for _, chord := range a.preferences.shortcutKeys(def) {
					labels = append(labels, chord.String())
				}
				label := strings.Join(labels, " / ")
				if a.shortcutRecording == def.id {
					label = a.t("shortcuts.recording")
				}
				button := ui.Button(c, label).MinWidth(92).MaxWidth(200).Height(28).Padding(0, 9).FontSize(10).Radius(8).Background(white(.07)).Border(0, ui.Transparent).Label(a.t("shortcuts.edit", a.t(def.label)))
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
					}
				}
				_, customized := a.preferences.Shortcuts[def.id]
				reset := a.iconControl(c, "rotate-ccw", a.t("shortcuts.reset", a.t(def.label)), iconStyle{24, 13, bareIcon}, false, func() { a.resetShortcut(def) })
				reset.Disabled(!customized)
			})
		}
	})
}
