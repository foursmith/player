package scriptplugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MoeForge/moejs"
	"github.com/foursmith/player/library"
)

type memoryCredentials struct {
	mu     sync.Mutex
	values map[string]map[string]string
}

func (s *memoryCredentials) Lookup(ctx context.Context, key string) (map[string]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]string{}
	for key, value := range s.values[key] {
		out[key] = value
	}
	return out, ctx.Err()
}
func (s *memoryCredentials) Store(ctx context.Context, key string, values map[string]string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[key] = values
	return ctx.Err()
}
func (s *memoryCredentials) Delete(ctx context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.values, key)
	return ctx.Err()
}
func testPlugin(t *testing.T, script string, network []string) *Plugin {
	t.Helper()
	manifest := Descriptor{ID: "test-script", Name: "Test", Version: "1", APIVersion: 1, Main: "index.ts", Network: network, Settings: []library.Setting{{Key: "server", Label: "Server", Kind: "url"}}}
	p, err := Load(library.EncodeConfig(manifest), []byte(script), nil)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func source(p *Plugin, id, address string) library.Source {
	return library.Source{ID: id, Name: id, Plugin: p.Manifest().ID, Config: library.EncodeConfig(map[string]string{"server": address}), CredentialRef: id}
}

// The same compiled TS plugin uses each connection's credentials and source ID.
func TestTSConnectionsStaySeparateAndDecodeMedia(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprintf(w, `{"name":%q}`, r.Header.Get("X-Account")) }))
	defer server.Close()
	p := testPlugin(t, `
 export async function browse({config,credentials,signal}) {
  const response=await fetch(config.server,{headers:{"X-Account":credentials.token},signal});
  const data=await response.json();const id:string="same-item";
  return {items:[{name:data.name,itemId:id}],nextCursor:""};
 }
 export function resolve({config,credentials}){return {url:config.server+"/stream",headers:{Authorization:credentials.token},seekable:true};}
 `, []string{"$server"})
	store := &memoryCredentials{values: map[string]map[string]string{"one": {"token": "one-token"}, "two": {"token": "two-token"}}}
	registry := library.NewRegistry(library.Host{Credentials: store}, p)
	keys := map[string]bool{}
	for _, id := range []string{"one", "two"} {
		session, err := registry.Open(context.Background(), source(p, id, server.URL))
		if err != nil {
			t.Fatal(err)
		}
		items, err := library.ReadDirectory(context.Background(), session, "")
		if err != nil || len(items) != 1 || items[0].Name() != id+"-token" {
			t.Fatal(items, err)
		}
		playback, err := library.ResolvePlayback(context.Background(), session, items[0].Reference())
		if err != nil || playback.Headers.Get("Authorization") != id+"-token" {
			t.Fatal(playback, err)
		}
		if keys[items[0].Key()] {
			t.Fatal("accounts share identity")
		}
		keys[items[0].Key()] = true
		session.Close()
	}
}

// A denied origin and redirects cannot escape the plugin's declared server.
func TestNetworkPolicyChecksFetchAndPlaybackResources(t *testing.T) {
	forbidden := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("forbidden origin was reached") }))
	defer forbidden.Close()
	allowed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, forbidden.URL, http.StatusFound) }))
	defer allowed.Close()
	p := testPlugin(t, fmt.Sprintf(`export async function browse({config,signal}){await fetch(config.server,{signal});return {items:[]};} export function resolve(){return {url:%q,seekable:true};}`, forbidden.URL), []string{"$server"})
	session, err := p.Open(context.Background(), source(p, "one", allowed.URL), library.Host{})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if _, err = session.Browse(context.Background(), library.BrowseRequest{}); err == nil {
		t.Fatal("cross-origin redirect was allowed")
	}
	if _, err = session.Resolve(context.Background(), library.Reference{SourceID: "one", ItemID: "video"}); err == nil {
		t.Fatal("native playback bypassed declared origin")
	}
}

// Validation failure must retain known server session data for host cleanup.
func TestRejectedPreparedResourceRetainsSessionForCleanup(t *testing.T) {
	p := testPlugin(t, `export function browse(){return {items:[]};}
export function resolve(){return {url:"https://forbidden.test/video",session:{id:"created"},seekable:true};}`, []string{"$server"})
	src := source(p, "account", "https://allowed.test")
	registry := library.NewRegistry(library.Host{}, p)
	session, err := registry.Open(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	playback, err := library.PreparePlayback(context.Background(), session, library.PlayRequest{Reference: library.Reference{SourceID: src.ID, ItemID: "video"}})
	if err == nil || string(playback.SessionData) != `{"id":"created"}` {
		t.Fatal("rejected resource lost its server session", playback, err)
	}
}

// Canceling fetch reaches the server; a CPU loop is also interruptible.
func TestCancellationStopsNetworkAndBusyJavaScript(t *testing.T) {
	started, disconnected := make(chan struct{}, 1), make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started <- struct{}{}
		<-r.Context().Done()
		disconnected <- struct{}{}
	}))
	defer server.Close()
	for _, busy := range []bool{false, true} {
		body := `await fetch(config.server,{signal});`
		if busy {
			body = `entered();while(true){}`
		}
		p := testPlugin(t, `export async function browse({config,signal}){`+body+`return {items:[]};} export function resolve(){return {url:"http://unused"};}`, []string{"$server"})
		session, err := p.Open(context.Background(), source(p, "one", server.URL), library.Host{})
		if err != nil {
			t.Fatal(err)
		}
		if busy {
			ready := make(chan struct{})
			session.(*scriptSession).engine.loop.RunOnLoop(func(rt *moejs.Runtime) {
				_ = rt.SetGlobal("entered", moejs.NativeFunc(func(*moejs.Realm, moejs.Value, []moejs.Value) (moejs.Value, error) {
					started <- struct{}{}
					return moejs.Undefined(), nil
				}))
				close(ready)
			})
			<-ready
		}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { _, err := session.Browse(ctx, library.BrowseRequest{}); done <- err }()
		select {
		case <-started:
		case <-time.After(3 * time.Second):
			t.Fatal("operation did not start")
		}
		cancel()
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("cancel did not interrupt operation")
		}
		session.Close()
		if !busy {
			select {
			case <-disconnected:
			case <-time.After(3 * time.Second):
				t.Fatal("canceled fetch remained active")
			}
		}
	}
}

// Installing validates and stages code, and never executes its top level.
func TestInstallValidatesWithoutRunningAndRejectsTraversal(t *testing.T) {
	root := t.TempDir()
	manifest := Descriptor{ID: "external", Name: "External", APIVersion: 1, Main: "index.js"}
	for name, data := range map[string][]byte{"plugin.json": library.EncodeConfig(manifest), "index.js": []byte(`throw Error("must not execute during installation");export function browse(){};export function resolve(){}`)} {
		if err := os.WriteFile(filepath.Join(root, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	registry := library.NewRegistry(library.Host{})
	catalog := &Catalog{Dir: t.TempDir(), Registry: registry}
	if _, err := catalog.Install(root); err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.Install(root); err == nil {
		t.Fatal("duplicate plugin replaced active code")
	}
	manifest.Main = "../outside.js"
	if _, err := Load(library.EncodeConfig(manifest), []byte("export function browse(){};export function resolve(){}"), nil); err == nil {
		t.Fatal("traversing entry was accepted")
	}
}

// The bundled connector authenticates, pages, resolves and reports via real HTTP.
func TestMediaServerEndToEnd(t *testing.T) {
	var mu sync.Mutex
	var events []string
	var isPlayback []bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/Users/AuthenticateByName":
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["Username"] != "user" || body["Pw"] != "secret" {
				t.Error("credentials lost")
			}
			fmt.Fprint(w, `{"AccessToken":"session-token","User":{"Id":"user-id"}}`)
		case r.URL.Path == "/Users/user-id/Items":
			if r.Header.Get("X-Emby-Token") != "session-token" {
				t.Error("missing browse token")
			}
			fmt.Fprint(w, `{"Items":[{"Id":"movie","Name":"A Movie","MediaType":"Video","RunTimeTicks":6000000000}],"TotalRecordCount":1}`)
		case r.URL.Path == "/Items/movie/PlaybackInfo":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			mu.Lock()
			isPlayback = append(isPlayback, body["IsPlayback"] == true)
			mu.Unlock()
			fmt.Fprint(w, `{"PlaySessionId":"play-session","MediaSources":[{"Id":"media","SupportsDirectPlay":true,"MediaStreams":[]}]}`)
		case strings.HasPrefix(r.URL.Path, "/Sessions/Playing"):
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["PlaySessionId"] != "play-session" || body["ItemId"] != "movie" {
				t.Error("playback session lost")
			}
			mu.Lock()
			events = append(events, r.URL.Path)
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Error("unexpected route", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	plugins, err := Bundled()
	if err != nil {
		t.Fatal(err)
	}
	p := plugins[0]
	src := source(p, "account", server.URL)
	credentials, err := p.Authenticate(context.Background(), library.Source{ID: src.ID, Name: src.Name, Plugin: src.Plugin, Config: library.EncodeConfig(map[string]string{"server": server.URL, "username": "user"})}, map[string]string{"password": "secret"})
	if err != nil || credentials["token"] != "session-token" || credentials["password"] != "" {
		t.Fatal("login retained password or lost token", err)
	}
	store := &memoryCredentials{values: map[string]map[string]string{src.ID: credentials}}
	host := library.Host{Credentials: store}
	registry := library.NewRegistry(host, p)
	session, err := registry.Open(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	items, err := library.ReadDirectory(context.Background(), session, "")
	if err != nil || len(items) != 1 || items[0].Duration != 600 {
		t.Fatal(items, err)
	}
	if _, err = library.ResolvePlayback(context.Background(), session, items[0].Reference()); err != nil {
		t.Fatal(err)
	}
	playback, err := library.PreparePlayback(context.Background(), session, library.PlayRequest{Reference: items[0].Reference(), Position: 42})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(playback.URL, "/Videos/movie/stream?") || playback.Headers.Get("X-Emby-Token") != "session-token" {
		t.Fatal(playback)
	}
	for _, kind := range []string{"start", "progress", "stop"} {
		if err = registry.Report(context.Background(), src, items[0].Reference(), playback, library.PlaybackEvent{Type: kind, Position: 42}); err != nil {
			t.Fatal(err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(isPlayback) != 2 || isPlayback[0] || !isPlayback[1] || len(events) != 3 {
		t.Fatal("preview created a playback session or events were lost", isPlayback, events)
	}
}

// Large repeated values are rejected before decoding them into host data.
func TestResultBudgetRejectsLargePluginResponses(t *testing.T) {
	p := testPlugin(t, `export function browse(){const text="x".repeat(1024*1024);return {items:Array(10).fill({itemId:"x",name:text})};} export function resolve(){}`, nil)
	session, err := p.Open(context.Background(), source(p, "one", "http://example.test"), library.Host{})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if _, err = session.Browse(context.Background(), library.BrowseRequest{}); !errors.Is(err, moejs.ErrTooLarge) {
		t.Fatal("plugin result exceeded host budget", err)
	}
}
