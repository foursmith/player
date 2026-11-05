package main

import (
	"context"
	"encoding/base64"
	"github.com/egoist/mygo/ui"
	"github.com/foursmith/player/internal/fsvr"
	"github.com/foursmith/player/internal/library"
	"net/url"
	"strings"
)

type remoteState struct {
	server                              *fsvr.Client
	serverStatus, serverError, password string
	scanning                            bool
	loadingFolders                      map[string]bool
	serverContext                       context.Context
	serverCancel                        context.CancelFunc
}

func (a *application) connectServer(endpoint string) {
	client, err := fsvr.New(endpoint)
	if err != nil {
		a.notice = err.Error()
		return
	}
	if a.serverCancel != nil {
		a.serverCancel()
	}
	ctx, cancel := context.WithCancel(a.ctx)
	a.serverContext, a.serverCancel = ctx, cancel
	a.server = client
	a.serverStatus = "connecting"
	a.loadingFolders = map[string]bool{}
	window := a.window
	go func() {
		authenticated, err := client.Connect(ctx)
		window.Update(func() {
			if ctx.Err() != nil || a.server != client {
				return
			}
			if err != nil {
				a.serverStatus = "error"
				a.serverError = a.t("server.connectionFailed")
			} else if !authenticated {
				a.serverStatus = "authentication-required"
			} else {
				a.loadServerLibrary(false)
			}
		})
	}()
}
func (a *application) authenticate() {
	if strings.TrimSpace(a.password) == "" || a.serverStatus == "connecting" {
		return
	}
	password := a.password
	a.password = ""
	a.serverStatus = "connecting"
	a.serverError = ""
	client, ctx, window := a.server, a.serverContext, a.window
	go func() {
		err := client.Authenticate(ctx, password)
		window.Update(func() {
			if ctx.Err() != nil || a.server != client {
				return
			}
			if err != nil {
				a.serverStatus = "authentication-required"
				a.serverError = a.t("server.invalidPassword")
			} else {
				a.loadServerLibrary(false)
			}
		})
	}()
}
func (a *application) loadServerLibrary(scan bool) {
	client, ctx, window := a.server, a.serverContext, a.window
	go func() {
		var err error
		if scan {
			err = client.Scan(ctx)
		}
		var folders []library.Folder
		if err == nil {
			folders, err = client.Sources(ctx)
		}
		window.Update(func() {
			if ctx.Err() != nil || a.server != client {
				return
			}
			a.scanning = false
			if err != nil {
				a.serverStatus = "error"
				a.serverError = err.Error()
				return
			}
			a.serverStatus = "connected"
			a.serverError = ""
			if scan {
				existing := map[string]bool{}
				for _, f := range a.folders {
					existing[f.ID] = true
				}
				for _, f := range folders {
					if !existing[f.ID] {
						a.folders = append(a.folders, f)
					}
				}
			} else {
				a.folders = folders
			}
			a.invalidatePlaylist()
			if !scan {
				a.resumeServer()
			}
		})
	}()
}
func (a *application) resumeServer() {
	if !a.preferences.AutoResume {
		return
	}
	u, err := url.Parse(a.stored.LastFile)
	if err != nil {
		return
	}
	parts := strings.Split(u.Path, "/")
	if len(parts) != 6 || parts[4] != "local" {
		return
	}
	decoded, err := base64.RawURLEncoding.DecodeString(parts[5])
	if err != nil {
		return
	}
	segments := strings.FieldsFunc(string(decoded), func(r rune) bool { return r == '/' || r == '\\' })
	if len(segments) == 0 {
		return
	}
	separator := "/"
	if strings.Contains(string(decoded), "\\") && !strings.Contains(string(decoded), "/") {
		separator = "\\"
	}
	ids := []string{"source:local"}
	for i := 1; i < len(segments); i++ {
		ids = append(ids, "local:"+base64.RawURLEncoding.EncodeToString([]byte(strings.Join(segments[:i], separator))))
	}
	a.resumeFolders(ids, a.server.MediaURL("local", parts[5]))
}
func (a *application) resumeFolders(ids []string, mediaURL string) {
	if len(ids) == 0 {
		for i, e := range a.entries {
			if e.Path == mediaURL {
				a.openEntry(i)
				return
			}
		}
		return
	}
	for _, f := range a.folders {
		if f.ID == ids[0] {
			a.expandedFolders[f.ID] = true
			a.playlistRowsValid = false
			a.loadRemoteFolder(f, func() { a.resumeFolders(ids[1:], mediaURL) })
			return
		}
	}
}
func (a *application) loadRemoteFolder(folder library.Folder, done func()) {
	if folder.Loaded {
		if done != nil {
			done()
		}
		return
	}
	if a.loadingFolders[folder.ID] {
		return
	}
	a.loadingFolders[folder.ID] = true
	client, ctx, window := a.server, a.serverContext, a.window
	go func() {
		entries, folders, err := client.Entries(ctx, folder)
		window.Update(func() {
			if ctx.Err() != nil || a.server != client {
				return
			}
			delete(a.loadingFolders, folder.ID)
			if err != nil {
				a.serverError = err.Error()
				return
			}
			for i := range a.folders {
				if a.folders[i].ID == folder.ID {
					a.folders[i].Loaded = true
				}
			}
			a.folders = append(a.folders, folders...)
			a.entries = append(a.entries, entries...)
			a.invalidatePlaylist()
			if done != nil {
				done()
			}
		})
	}()
}
func (a *application) authPicker(c *ui.Context) {
	ui.Column(c).AlignItems(ui.Center).Gap(10).Children(func() {
		glass(c, 999).Width(288).Height(40).Children(func() {
			ui.Row(c).Fill().Padding(0, 8).Children(func() {
				input := ui.TextInput(c, &a.password).Password().Grow(1).Height(40).Background(ui.Transparent).Border(0, ui.Transparent).Placeholder(a.t("empty.password")).Label(a.t("empty.password")).Disabled(a.serverStatus == "connecting")
				input.OnTextKey(func(k ui.TextKey) bool {
					if k.Key == ui.KeyEnter {
						a.authenticate()
						return true
					}
					return false
				})
				label := a.t("empty.unlock")
				if a.serverStatus == "connecting" {
					label = a.t("empty.checking")
				}
				if ui.Button(c, label).Height(36).Background(ui.Transparent).Border(0, ui.Transparent).FontSize(10).Disabled(a.password == "" || a.serverStatus == "connecting").Clicked() {
					a.authenticate()
				}
			})
		})
		smallText(c, a.t("empty.enterPassword"), .52)
		if a.serverError != "" {
			smallText(c, a.serverError, .8).TextColor(ui.Hex("#fca5a5"))
		}
	})
}
