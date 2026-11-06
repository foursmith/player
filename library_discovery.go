package main

import (
	"fmt"
	"log"
	"slices"
	"time"

	"github.com/foursmith/player/internal/dlna"
	"github.com/foursmith/player/internal/providers"
	"github.com/foursmith/player/library"
)

func rememberDiscoveredLibraries(saved, incoming []library.Source) ([]library.Source, string) {
	out := library.CloneSources(saved)
	first := ""
	for _, source := range libraryPlugins.NormalizeSources(incoming) {
		match := slices.IndexFunc(out, func(current library.Source) bool {
			if current.Plugin != source.Plugin {
				return false
			}
			if current.ID == source.ID {
				return true
			}
			plugin, err := libraryPlugins.Plugin(source.Plugin)
			if err != nil {
				return false
			}
			keyer, ok := plugin.(library.SourceKeyer)
			return ok && keyer.SourceKey(source) != "" && keyer.SourceKey(current) == keyer.SourceKey(source)
		})
		if match >= 0 {
			out[match].Config = source.Config
			if first == "" {
				first = out[match].ID
			}
		} else {
			out = append(out, source)
			if first == "" {
				first = source.ID
			}
		}
	}
	return out, first
}

func (a *application) scanDLNA() {
	self := ""
	if a.stored.DLNAUUID != "" {
		self = dlna.DeviceID("uuid:" + a.stored.DLNAUUID)
	}
	a.scanLibraryPlugin(providers.DLNAID, self)
}

func (a *application) scanLibraryPlugin(id, excludeSource string) {
	a.openCatalog()
	if a.scanning {
		return
	}
	a.scanning = true
	a.catalog.error = ""
	a.refreshWindows()
	window, ctx := a.window, a.ctx
	go func() {
		started := time.Now()
		plugin, err := libraryPlugins.Plugin(id)
		var sources []library.Source
		if err == nil {
			if discoverer, ok := plugin.(library.Discoverer); ok {
				sources, err = discoverer.Discover(ctx)
			} else {
				err = fmt.Errorf("%w: %s discovery", library.ErrUnsupported, id)
			}
		}
		log.Printf("[library] discovery plugin=%s sources=%d elapsed=%s error=%v", id, len(sources), time.Since(started).Round(time.Millisecond), err)
		window.Update(func() {
			a.scanning = false
			if ctx.Err() != nil || a.shutdown {
				return
			}
			sources = slices.DeleteFunc(sources, func(source library.Source) bool { return source.ID == excludeSource })
			if err != nil {
				a.catalog.error = a.t("dlna.scanFailed") + ": " + err.Error()
			} else if len(sources) == 0 {
				a.catalog.error = a.t("dlna.noDevices")
			} else {
				var first string
				a.stored.Libraries, first = rememberDiscoveredLibraries(a.stored.Libraries, sources)
				a.persist()
				if a.catalogWindow != nil {
					if selected := a.selectedCatalog(); selected == nil || selected.Plugin != id {
						a.selectCatalog(first)
					} else {
						a.refreshCatalog()
					}
				}
			}
			a.refreshWindows()
		})
	}()
}
