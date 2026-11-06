package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"strings"

	"github.com/egoist/mygo/ui"
)

//go:embed assets/icons assets/locales assets/models assets/logo.png
var assets embed.FS

//go:embed mygo.json
var appManifest []byte

var appInfo struct {
	Name, Version, License string
}

var icons = map[string]*ui.SVG{}
var dictionaries = map[string]map[string]string{}
var logo *ui.Bitmap

func loadAssets() error {
	if err := json.Unmarshal(appManifest, &appInfo); err != nil {
		return fmt.Errorf("application manifest: %w", err)
	}
	files, err := fs.ReadDir(assets, "assets/icons")
	if err != nil {
		return err
	}
	for _, f := range files {
		b, err := assets.ReadFile("assets/icons/" + f.Name())
		if err != nil {
			return err
		}
		icon, err := ui.ParseSVG(b)
		if err != nil {
			return fmt.Errorf("%s: %w", f.Name(), err)
		}
		icons[strings.TrimSuffix(f.Name(), ".svg")] = icon
	}
	for _, locale := range []string{"en", "zh-CN", "ja"} {
		b, err := assets.ReadFile("assets/locales/" + locale + ".json")
		if err != nil {
			return err
		}
		var dictionary map[string]string
		if err = json.Unmarshal(b, &dictionary); err != nil {
			return err
		}
		dictionary["common.appName"] = appInfo.Name
		dictionaries[locale] = dictionary
	}
	b, err := assets.ReadFile("assets/logo.png")
	if err != nil {
		return err
	}
	logo, err = ui.DecodeBitmap(b)
	return err
}
func (a *application) t(key string, args ...any) string {
	dictionary := dictionaries[a.preferences.Locale]
	if dictionary == nil {
		dictionary = dictionaries["en"]
	}
	value := dictionary[key]
	if value == "" {
		value = dictionaries["en"][key]
	}
	if value == "" {
		value = key
	}
	if len(args) > 0 {
		return fmt.Sprintf(value, args...)
	}
	return value
}

func (a *application) projectionLabel(mode int) string {
	keys := [...]string{"sbs180Eqr", "sbs180Fe", "mono360Eqr", "mono180Eqr", "mono180Fe", "tb360Eqr", "flat2d"}
	return a.t("projection.options." + keys[max(0, min(6, mode))])
}
