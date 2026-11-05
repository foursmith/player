package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"reflect"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/foursmith/player/internal/dlnaserver"
	"github.com/foursmith/player/internal/library"
)

type dlnaSharingState struct {
	server     *dlnaserver.Server
	cancel     context.CancelFunc
	generation uint64
	starting   bool
	name       string
	locations  []string
	sources    []library.Saved
	error      string
}

// syncDLNASharing belongs to the UI thread. The worker owns all source paths;
// queued completions recheck identity before publishing a running server.
func (a *application) syncDLNASharing() {
	sources := dlnaserver.SharedSources(a.stored.Libraries)
	if len(sources) == 0 || a.shutdown {
		a.stopDLNASharing()
		a.dlnaSharing.error = ""
		return
	}
	if a.dlnaSharing.error != "" && reflect.DeepEqual(a.dlnaSharing.sources, sources) {
		return
	}
	if a.dlnaSharing.server != nil {
		a.dlnaSharing.server.SetSources(sources)
		a.dlnaSharing.sources = sources
		return
	}
	if a.dlnaSharing.starting {
		if reflect.DeepEqual(a.dlnaSharing.sources, sources) {
			return
		}
		a.stopDLNASharing()
	}
	if a.window == nil {
		return
	}
	if !dlnaserver.ValidUUID(a.stored.DLNAUUID) {
		a.stored.DLNAUUID = dlnaserver.NewUUID()
	}
	host, _ := os.Hostname()
	host = strings.TrimSuffix(host, ".local")
	name := appInfo.Name
	if host != "" {
		name += " (" + host + ")"
	}
	state := &a.dlnaSharing
	state.generation++
	state.starting, state.error, state.name = true, "", name
	state.sources = sources
	ctx, cancel := context.WithCancel(a.ctx)
	state.cancel = cancel
	generation, window := state.generation, a.window
	options := dlnaserver.Options{UUID: a.stored.DLNAUUID, Name: name, Sources: state.sources}
	go func() {
		server, err := dlnaserver.Start(ctx, options)
		window.Update(func() {
			if a.shutdown || ctx.Err() != nil || generation != state.generation {
				return
			}
			state.starting = false
			if err != nil {
				log.Printf("[dlna-server] start failed: %v", err)
				a.failDLNASharing(a.t("sharing.failed") + ": " + err.Error())
				return
			}
			state.server, state.locations = server, server.Locations()
			a.refreshWindows()
			go func() {
				<-server.Done()
				window.Update(func() {
					if a.shutdown || ctx.Err() != nil || generation != state.generation {
						return
					}
					a.failDLNASharing(a.t("sharing.stopped"))
				})
			}()
		})
	}()
}

func (a *application) failDLNASharing(message string) {
	sources := a.dlnaSharing.sources
	a.stopDLNASharing()
	a.dlnaSharing.sources, a.dlnaSharing.error = sources, message
	a.refreshWindows()
}

func (a *application) setCatalogDLNASharing(id string, shared bool) {
	i := a.catalogIndex(id)
	if i < 0 || a.stored.Libraries[i].DeviceID != "" {
		return
	}
	a.stored.Libraries[i].ShareDLNA = shared
	a.dlnaSharing.error = ""
	a.persist()
}

func (a *application) stopDLNASharing() {
	state := &a.dlnaSharing
	if state.cancel != nil {
		state.cancel()
		state.cancel = nil
		state.generation++
	}
	state.server = nil
	state.starting, state.locations, state.sources = false, nil, nil
}

func (a *application) catalogSharingButton(c *ui.Context, saved library.Saved) {
	if saved.DeviceID != "" {
		return
	}
	button := glassAction(c.Key("dlna-sharing-toggle"), "source-dlna", "DLNA").Label(a.t("sharing.title")).Role(ui.RoleSwitch).Checked(saved.ShareDLNA)
	if saved.ShareDLNA {
		button.Background(accent.Alpha(.12)).TextColor(accent)
	}
	button.OnClick(func() { a.setCatalogDLNASharing(saved.ID, !saved.ShareDLNA) })
}

func (a *application) catalogSharingStatus(c *ui.Context, saved library.Saved) {
	if saved.DeviceID != "" || !saved.ShareDLNA {
		return
	}
	status := a.dlnaSharing.error
	if a.dlnaSharing.starting {
		status = a.t("sharing.starting")
	} else if a.dlnaSharing.server != nil {
		var addresses []string
		for _, location := range a.dlnaSharing.locations {
			addresses = append(addresses, strings.TrimSuffix(location, "/device.xml"))
		}
		status = fmt.Sprintf("%s · %s", a.dlnaSharing.name, strings.Join(addresses, " · "))
	}
	if status == "" {
		return
	}
	ui.Column(c.Key("dlna-sharing")).FillWidth().Padding(0, 16, 6).Gap(3).Children(func() {
		ui.Text(c, status).FontSize(10).TextColor(white(.45)).Padding(0, 12, 4).FillWidth().Wrap()
		if a.dlnaSharing.error != "" {
			glassAction(c, "rotate-ccw", a.t("sharing.retry")).AlignSelf(ui.Start).OnClick(func() {
				a.dlnaSharing.error = ""
				a.syncDLNASharing()
				a.refreshWindows()
			})
		}
	})
}
