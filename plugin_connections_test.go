package main

import (
	"context"
	"encoding/json"
	"maps"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/foursmith/player/library"
)

type connectionCredentials struct{ values map[string]map[string]string }

func (s *connectionCredentials) Lookup(ctx context.Context, key string) (map[string]string, error) {
	return maps.Clone(s.values[key]), ctx.Err()
}
func (s *connectionCredentials) Store(ctx context.Context, key string, values map[string]string) error {
	s.values[key] = maps.Clone(values)
	return ctx.Err()
}
func (s *connectionCredentials) Delete(ctx context.Context, key string) error {
	delete(s.values, key)
	return ctx.Err()
}

type connectionPlugin struct {
	accountID string
	integrationPlugin
	got map[string]string
}

func (p *connectionPlugin) Settings() []library.Setting {
	return []library.Setting{{Key: "server", Label: "Server", Kind: "url"}, {Key: "password", Label: "Password", Kind: "password", Secret: true}}
}
func (p *connectionPlugin) Text(text, locale string) string { return text }
func (p *connectionPlugin) Authenticate(ctx context.Context, source library.Source, credentials map[string]string) (map[string]string, error) {
	p.got = maps.Clone(credentials)
	result := map[string]string{"token": "new-token"}
	if p.accountID != "" {
		result["accountId"] = p.accountID
	}
	return result, ctx.Err()
}

// Saving a connection strips passwords and never sends the old token to a changed server.
func TestConnectionSecretsStayOutsideStateAndServerBoundaries(t *testing.T) {
	plugin := &connectionPlugin{integrationPlugin: integrationPlugin{id: t.Name()}}
	if err := libraryPlugins.Register(plugin); err != nil {
		t.Fatal(err)
	}
	store := &connectionCredentials{values: map[string]map[string]string{"old": {"token": "old-token"}}}
	source := library.Source{ID: "account", Name: "Account", Plugin: plugin.id, Config: library.EncodeConfig(map[string]string{"server": "https://one.test"}), CredentialRef: "old"}
	saved, err := saveConnection(context.Background(), source, " Account ", map[string]string{"server": "https://one.test"}, store)
	if err != nil || plugin.got["token"] != "old-token" || saved.Name != "Account" || saved.ID != source.ID {
		t.Fatal(saved, err)
	}
	saved, err = saveConnection(context.Background(), source, "Account", map[string]string{"server": "https://two.test", "password": "secret"}, store)
	if err != nil || plugin.got["token"] != "" || plugin.got["password"] != "secret" || saved.ID == source.ID {
		t.Fatal("credential crossed server boundary", err)
	}
	data, _ := json.Marshal(saved)
	if strings.Contains(string(data), "secret") || strings.Contains(string(data), "new-token") || saved.CredentialRef == source.CredentialRef {
		t.Fatal("credential leaked into config", string(data))
	}
	if store.values[saved.CredentialRef]["token"] != "new-token" || store.values["old"]["token"] != "old-token" {
		t.Fatal("active playback credentials were overwritten")
	}
}

// Canceling the editor discards in-memory credentials without creating a library.
func TestConnectionEditorCancelDiscardsInput(t *testing.T) {
	if err := loadAssets(); err != nil {
		t.Fatal(err)
	}
	a := modeTestApplication(t)
	a.newConnection("mediaserver")
	*a.connectionEditor.fields["password"] = "typed-secret"
	view := ui.NewTester(a.catalogView, 800, 600)
	if err := view.Click(a.t("common.cancel")); err != nil {
		t.Fatal(err)
	}
	if a.connectionEditor != nil || len(a.stored.Libraries) != 0 {
		t.Fatal("cancel saved a connection or retained the editor")
	}
}

// Reconnecting the same account supersedes stale queue credentials for new opens.
func TestConnectionLookupUsesLatestSavedLogin(t *testing.T) {
	a := modeTestApplication(t)
	old := library.Source{ID: "account", Name: "Account", Plugin: "test", Config: library.EncodeConfig(map[string]string{}), CredentialRef: "old"}
	current := old
	current.CredentialRef = "new"
	a.stored.Libraries = []library.Source{current}
	a.playlistRemote = map[string]*playlistRemoteSource{old.ID: {saved: old}}
	found, ok := a.playbackSource(old.ID)
	if !ok || found.CredentialRef != "new" {
		t.Fatal("reconnection retained stale credentials", found)
	}
}

// A token for another user rotates media identity even at the same server URL.
func TestConnectionTokenAccountChangeRotatesIdentity(t *testing.T) {
	plugin := &connectionPlugin{integrationPlugin: integrationPlugin{id: t.Name()}, accountID: "second-user"}
	if err := libraryPlugins.Register(plugin); err != nil {
		t.Fatal(err)
	}
	store := &connectionCredentials{values: map[string]map[string]string{"old": {"token": "old-token", "accountId": "first-user"}}}
	original := library.Source{ID: "original", Name: "Account", Plugin: plugin.id, Config: library.EncodeConfig(map[string]string{"server": "https://same.test"}), CredentialRef: "old"}
	saved, err := saveConnection(context.Background(), original, "Account", map[string]string{"server": "https://same.test"}, store)
	if err != nil || saved.ID == original.ID {
		t.Fatal("different token user retained another user's history identity", saved, err)
	}
}
