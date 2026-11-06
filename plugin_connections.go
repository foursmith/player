package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"maps"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/foursmith/player/library"
)

type nativeCredentials struct{}

func (nativeCredentials) Lookup(ctx context.Context, key string) (map[string]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	data, err := mygo.Secrets.Get(key)
	if errors.Is(err, mygo.ErrSecretNotFound) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	var values map[string]string
	err = json.Unmarshal(data, &values)
	return values, err
}
func (nativeCredentials) Store(ctx context.Context, key string, values map[string]string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	data, err := json.Marshal(values)
	if err != nil {
		return err
	}
	return mygo.Secrets.Set(key, data)
}
func (nativeCredentials) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return mygo.Secrets.Delete(key)
}

type connectionEditor struct {
	source library.Source
	name   string
	fields map[string]*string
	error  string
	saving bool
	cancel context.CancelFunc
}

func (a *application) editConnection(source library.Source) {
	p, err := libraryPlugins.Plugin(source.Plugin)
	if err != nil {
		a.catalog.error = err.Error()
		return
	}
	configurable, ok := p.(library.Configurable)
	if !ok {
		return
	}
	a.cancelConnectionEditor()
	editor := &connectionEditor{source: source, name: source.Name, fields: map[string]*string{}}
	var config map[string]string
	_ = json.Unmarshal(source.Config, &config)
	for _, setting := range configurable.Settings() {
		value := ""
		if !setting.Secret {
			value = config[setting.Key]
		}
		editor.fields[setting.Key] = &value
	}
	a.connectionEditor = editor
	a.refreshWindows()
}
func (a *application) newConnection(id string) {
	p, err := libraryPlugins.Plugin(id)
	if err != nil {
		a.catalog.error = err.Error()
		return
	}
	a.editConnection(library.Source{ID: rand.Text(), Name: p.Manifest().Name, Plugin: id, Config: library.EncodeConfig(map[string]string{})})
}
func (a *application) cancelConnectionEditor() {
	if editor := a.connectionEditor; editor != nil && editor.cancel != nil {
		editor.cancel()
	}
	a.connectionEditor = nil
}

// A changed server/account never receives the previous connection's token.
func saveConnection(ctx context.Context, source library.Source, name string, fields map[string]string, store library.CredentialWriter) (library.Source, error) {
	p, err := libraryPlugins.Plugin(source.Plugin)
	if err != nil {
		return source, err
	}
	configurable, ok := p.(library.Configurable)
	if !ok {
		return source, library.ErrUnsupported
	}
	config, secrets := map[string]string{}, map[string]string{}
	for _, setting := range configurable.Settings() {
		if setting.Secret {
			if fields[setting.Key] != "" {
				secrets[setting.Key] = fields[setting.Key]
			}
		} else {
			config[setting.Key] = fields[setting.Key]
		}
	}
	oldAccountID := ""
	original := source
	source.Name = strings.TrimSpace(name)
	source.Config = library.EncodeConfig(config)
	if err = library.ValidateSource(source); err != nil {
		return source, err
	}
	source, err = p.Normalize(source)
	if err != nil {
		return source, err
	}
	var originalConfig map[string]string
	_ = json.Unmarshal(original.Config, &originalConfig)
	// A different backend/account is a new media identity. Existing queue and
	// history snapshots keep their original connection and credentials.
	if len(originalConfig) > 0 && string(original.Config) != string(source.Config) {
		source.ID = rand.Text()
	}
	if original.CredentialRef != "" && string(original.Config) == string(source.Config) {
		if store == nil {
			return source, errors.New("system credential store is unavailable")
		}
		old, err := store.Lookup(ctx, original.CredentialRef)
		if err != nil {
			return source, err
		}
		oldAccountID = old["accountId"]
		old = maps.Clone(old)
		if old == nil {
			old = map[string]string{}
		}
		for key, value := range secrets {
			old[key] = value
		}
		secrets = old
	}
	if authenticator, ok := p.(library.Authenticator); ok {
		secrets, err = authenticator.Authenticate(ctx, source, secrets)
		if err != nil {
			return source, err
		}
	}
	if oldAccountID != "" && secrets["accountId"] != "" && oldAccountID != secrets["accountId"] {
		source.ID = rand.Text()
	}
	if len(secrets) == 0 {
		source.CredentialRef = ""
		return source, ctx.Err()
	}
	if store == nil {
		return source, errors.New("system credential store is unavailable")
	}
	source.CredentialRef = "library:" + rand.Text()
	if err = store.Store(ctx, source.CredentialRef, secrets); err != nil {
		return source, err
	}
	if err = ctx.Err(); err != nil {
		_ = store.Delete(context.Background(), source.CredentialRef)
		return source, err
	}
	return source, nil
}
func (a *application) saveConnection() {
	editor := a.connectionEditor
	if editor == nil || editor.saving {
		return
	}
	fields := map[string]string{}
	for key, value := range editor.fields {
		fields[key] = *value
	}
	ctx, cancel := context.WithTimeout(a.ctx, 45*time.Second)
	editor.cancel = cancel
	editor.saving = true
	editor.error = ""
	source := library.CloneSources([]library.Source{editor.source})[0]
	name := editor.name
	store := a.credentials
	go func() {
		saved, err := saveConnection(ctx, source, name, fields, store)
		a.window.Update(func() {
			defer cancel()
			if a.shutdown || ctx.Err() == context.Canceled || a.connectionEditor != editor {
				if err == nil && store != nil && saved.CredentialRef != "" {
					go store.Delete(context.Background(), saved.CredentialRef)
				}
				return
			}
			editor.saving = false
			editor.cancel = nil
			if err != nil {
				editor.error = err.Error()
				a.refreshWindows()
				return
			}
			if i := a.catalogIndex(editor.source.ID); i >= 0 {
				a.stored.Libraries[i] = saved
			} else {
				a.stored.Libraries = append(a.stored.Libraries, saved)
			}
			a.playlistSnapshot = nil
			a.connectionEditor = nil
			a.persist()
			a.selectCatalog(saved.ID)
		})
	}()
}
func (a *application) connectionMenu(m *ui.Menu) {
	for _, manifest := range libraryPlugins.Manifests() {
		p, _ := libraryPlugins.Plugin(manifest.ID)
		if _, ok := p.(library.Configurable); !ok {
			continue
		}
		if m.Item(manifest.Name).Chosen() {
			a.newConnection(manifest.ID)
		}
	}
	m.Separator()
	if m.Item(a.t("plugins.install")).Chosen() {
		a.installLibraryPlugin()
	}
}
func (a *application) installLibraryPlugin() {
	a.chooseCatalogPaths(true, "plugins.install", func(paths []string) {
		if a.pluginCatalog == nil {
			a.catalog.error = a.t("plugins.unavailable")
			return
		}
		catalog := a.pluginCatalog
		ctx := a.ctx
		go func() {
			p, err := catalog.Install(paths[0])
			a.window.Update(func() {
				if ctx.Err() != nil || a.shutdown {
					return
				}
				if err != nil {
					a.catalog.error = err.Error()
				} else {
					a.newConnection(p.Manifest().ID)
				}
				a.refreshWindows()
			})
		}()
	})
}
func (a *application) connectionView(c *ui.Context) {
	editor := a.connectionEditor
	p, err := libraryPlugins.Plugin(editor.source.Plugin)
	if err != nil {
		return
	}
	configurable := p.(library.Configurable)
	ui.Column(c.Key("connection-editor")).FillWidth().Grow(1).MinHeight(0).Padding(20).Gap(12).Children(func() {
		ui.Text(c, a.t("plugins.connection")).FontSize(18).FontWeight(600)
		ui.Text(c, p.Manifest().Name).FontSize(11).TextColor(textColor(c, .5))
		ui.Scroll(c).Grow(1).FillWidth().MinHeight(0).Children(func() {
			ui.Column(c).FillWidth().Gap(12).Children(func() {
				ui.Text(c, a.t("library.name")).FontSize(11)
				ui.TextInput(c.Key("connection-name"), &editor.name).Label(a.t("library.name")).FillWidth().Disabled(editor.saving)
				for _, setting := range configurable.Settings() {
					label := configurable.Text(setting.Label, a.preferences.Locale)
					ui.Text(c, label).FontSize(11)
					input := ui.TextInput(c.Key(setting.Key), editor.fields[setting.Key]).Label(label).FillWidth().Disabled(editor.saving)
					if setting.Secret {
						input.Password()
					}
				}
				if permissions, ok := p.(library.NetworkAccess); ok {
					rules := permissions.NetworkPermissions()
					for i, rule := range rules {
						if strings.HasPrefix(rule, "$") {
							key := rule[1:]
							if value := editor.fields[key]; value != nil && *value != "" {
								rules[i] = *value
							} else {
								for _, setting := range configurable.Settings() {
									if setting.Key == key {
										rules[i] = configurable.Text(setting.Label, a.preferences.Locale)
									}
								}
							}
						}
					}
					scope := strings.Join(rules, ", ")
					if scope == "" {
						scope = a.t("plugins.noNetwork")
					}
					ui.Text(c, a.t("plugins.networkAccess", scope)).FontSize(10).TextColor(textColor(c, .5)).Wrap()
				}
				ui.Text(c, a.t("plugins.credentialsHelp")).FontSize(10).TextColor(textColor(c, .5)).Wrap()
				if editor.error != "" {
					ui.Text(c, editor.error).FontSize(11).TextColor(c.Theme().Warning).Wrap()
				}
			})
		})
		ui.Row(c).FillWidth().Gap(8).Children(func() {
			glassButton(c, a.t("common.cancel")).OnClick(func() { a.cancelConnectionEditor(); a.refreshWindows() })
			label := a.t("plugins.connect")
			if editor.saving {
				label = a.t("plugins.connecting")
			}
			glassButton(c, label).Disabled(editor.saving || editor.name == "").OnClick(a.saveConnection)
		})
	})
}
