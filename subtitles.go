package main

import (
	"context"
	"net/url"
	"os"

	"github.com/foursmith/player/internal/subtitle"
	"github.com/foursmith/player/library"
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
// disk/DLNA reads and parsing never block the native event/render thread.
func (a *application) readSubtitle(entry library.Entry, path string, discover bool) {
	a.cancelSubtitle()
	ctx, cancel := context.WithCancel(a.ctx)
	a.subtitleCancel = cancel
	paths := append([]string(nil), a.subtitlePaths...)
	if entry.SourceID != "" {
		paths = nil
	}
	window := a.window
	go func() {
		if discover {
			path = library.MatchSubtitle(entry, paths)
			if path == "" && entry.SourceID == "" {
				if siblings, err := library.SubtitleSiblings(entry.Path); err == nil {
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
		if u, parseErr := url.Parse(path); parseErr == nil && (u.Scheme == "http" || u.Scheme == "https") {
			resource := library.Resource{URL: path}
			if entry.Playback != nil && entry.Playback.Subtitle.URL == path {
				resource = entry.Playback.Subtitle
			}
			data, err = library.ReadResource(ctx, resource, 8<<20)
			name = u.Path
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
