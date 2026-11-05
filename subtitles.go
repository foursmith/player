package main

import (
	"context"
	"os"
	"strings"

	"github.com/foursmith/vr/app/internal/library"
	"github.com/foursmith/vr/app/internal/subtitle"
)

func (a *application) cancelSubtitle() {
	if a.subtitleCancel != nil {
		a.subtitleCancel()
		a.subtitleCancel = nil
	}
}

func (a *application) loadSubtitle(path string) {
	var entry library.Entry
	if a.selected >= 0 && a.selected < len(a.entries) {
		entry = a.entries[a.selected]
	}
	a.readSubtitle(entry, path, false)
}

// A selection or manual subtitle load replaces the previous request. Discovery,
// disk/network reads and parsing never block the native event/render thread.
func (a *application) readSubtitle(entry library.Entry, path string, discover bool) {
	a.cancelSubtitle()
	ctx, cancel := context.WithCancel(context.Background())
	a.subtitleCancel = cancel
	paths := append([]string(nil), a.subtitlePaths...)
	client, window := a.server, a.window
	remote := func(path string) bool {
		return strings.HasPrefix(path, "https://") || strings.HasPrefix(path, "http://")
	}
	go func() {
		if discover {
			path = library.MatchSubtitle(entry, paths)
			if path == "" && !remote(entry.Path) {
				if _, siblings, err := library.Siblings(entry.Path); err == nil {
					path = library.MatchSubtitle(entry, siblings)
				}
			}
		}
		if path == "" || ctx.Err() != nil {
			return
		}
		name := path
		var data []byte
		var err error
		if client != nil && remote(path) {
			data, err = client.ReadSubtitle(ctx, path)
			if entry.SubtitleName != "" {
				name = entry.SubtitleName
			}
		} else {
			data, err = os.ReadFile(path)
		}
		if ctx.Err() != nil {
			return
		}
		var cues []subtitle.Cue
		if err == nil {
			cues = subtitle.Parse(string(data), name)
		}
		if ctx.Err() != nil {
			return
		}
		window.Update(func() {
			if ctx.Err() != nil {
				return
			}
			if err != nil {
				a.notice = err.Error()
				return
			}
			a.cues = cues
		})
	}()
}
