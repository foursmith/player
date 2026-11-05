package main

import (
	"encoding/json"
	"fmt"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"io"
	"net/http"
	"time"
)

type aboutState struct {
	stars          string
	starsRequested bool
}

func (a *application) requestStars() {
	if a.starsRequested {
		return
	}
	a.starsRequested = true
	go func() {
		client := &http.Client{Timeout: 5 * time.Second}
		r, err := client.Get("https://api.github.com/repos/foursmith/vr")
		if err != nil {
			return
		}
		defer r.Body.Close()
		if r.StatusCode != 200 {
			return
		}
		var repo struct {
			Count int `json:"stargazers_count"`
		}
		if json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&repo) != nil {
			return
		}
		value := fmt.Sprint(repo.Count)
		if repo.Count >= 1000 {
			value = fmt.Sprintf("%.1fk", float64(repo.Count)/1000)
		}
		a.window.Update(func() { a.stars = value })
	}()
}
func (a *application) about(c *ui.Context) {
	a.requestStars()
	ui.Row(c).FillWidth().Padding(12).Gap(12).Children(func() {
		ui.Image(c, logo).Size(40, 40)
		ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
			ui.Text(c, "Foursmith VR").FontSize(12).FontWeight(600).SingleLine()
			smallText(c, a.t("common.tagline"), .48).SingleLine()
		})
		ui.Column(c).AlignItems(ui.End).Gap(2).Children(func() {
			linkButton(c, "v0.2.6", "https://github.com/foursmith/vr/releases/tag/v0.2.6").FontSize(8).Height(14).Background(ui.Hex("#34383c")).Padding(0, 6).Radius(3)
			if a.stars != "" {
				linkButton(c, "★ stars  "+a.stars, "https://github.com/foursmith/vr/stargazers").FontSize(8).Height(14).Background(ui.Hex("#34383c")).Padding(0, 4).Radius(3)
			}
		})
	})
	ui.Divider(c).Background(white(.07))
	ui.Row(c).FillWidth().Padding(10, 12).Gap(4).Children(func() {
		smallText(c, a.t("settings.by"), .42)
		linkButton(c, "ourongxing", "https://github.com/ourongxing")
		smallText(c, "·", .24)
		linkButton(c, "MPL 2.0", "https://github.com/foursmith/vr/blob/main/LICENSE")
		ui.Spacer(c).Grow(1)
		a.aboutAction(c, "bug", a.t("settings.debug"), a.debugOpen, func() { a.debugOpen = !a.debugOpen })
		a.aboutAction(c, "github", "GitHub", false, func() { go mygo.Shell.OpenExternal("https://github.com/foursmith/vr") })
	})
}
func linkButton(c *ui.Context, label, url string) ui.Element {
	button := ui.Button(c, label).Height(22).Padding(0, 2).FontSize(10).Border(0, ui.Transparent).Background(ui.Transparent).TextColor(white(.68)).Cursor(ui.CursorPointer)
	if button.Clicked() {
		go mygo.Shell.OpenExternal(url)
	}
	return button
}
func (a *application) aboutAction(c *ui.Context, icon, label string, pressed bool, action func()) {
	button := ui.Button(c, "").Height(24).Padding(4, 7).Radius(12).Border(0, ui.Transparent).Background(ui.Transparent).Label(label)
	color := white(.42)
	if pressed {
		button.Background(accent.Alpha(.1))
		color = accent
	}
	button.Children(func() {
		ui.Row(c).Gap(6).Children(func() {
			ui.Icon(c, icons[icon]).Size(14, 14).TextColor(color)
			ui.Text(c, label).FontSize(10).TextColor(color)
		})
	})
	if button.Clicked() {
		action()
	}
}
