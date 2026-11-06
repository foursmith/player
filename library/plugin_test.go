package library

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"
)

type stubPlugin struct {
	manifest Manifest
	open     func(context.Context, Source, Host) (Session, error)
}

func (p stubPlugin) Manifest() Manifest                 { return p.manifest }
func (p stubPlugin) Normalize(s Source) (Source, error) { return s, nil }
func (p stubPlugin) Open(ctx context.Context, s Source, h Host) (Session, error) {
	return p.open(ctx, s, h)
}

type stubSession struct {
	source  Source
	browse  func(context.Context, BrowseRequest) (Page, error)
	resolve func(context.Context, Reference) (Playback, error)
	close   func() error
}

func (s *stubSession) Source() Source { return s.source }
func (s *stubSession) Browse(ctx context.Context, r BrowseRequest) (Page, error) {
	return s.browse(ctx, r)
}
func (s *stubSession) Resolve(ctx context.Context, r Reference) (Playback, error) {
	return s.resolve(ctx, r)
}
func (s *stubSession) Close() error {
	if s.close != nil {
		return s.close()
	}
	return nil
}
func testSource(id string) Source {
	return Source{ID: id, Name: id, Plugin: "test", Config: EncodeConfig(map[string]string{"account": "user"}), CredentialRef: "secure-store:account"}
}

type testCredentials struct{}

func (testCredentials) Lookup(ctx context.Context, key string) (map[string]string, error) {
	return map[string]string{"token": key}, ctx.Err()
}

func TestPluginRegistryRetainsUnavailableSourcesAndPassesHostCredentials(t *testing.T) {
	source := testSource("account-one")
	source.ShareDLNA = true
	registry := NewRegistry(Host{Credentials: testCredentials{}})
	normalized := registry.NormalizeSources([]Source{source, source})
	if len(normalized) != 1 || normalized[0].CredentialRef != source.CredentialRef || normalized[0].ShareDLNA {
		t.Fatal("unavailable source was lost, duplicated or re-shared", normalized)
	}
	if _, err := registry.Open(context.Background(), normalized[0]); !errors.Is(err, ErrPluginUnavailable) {
		t.Fatal(err)
	}
	closed := false
	plugin := stubPlugin{manifest: Manifest{ID: "test", Name: "Test"}, open: func(ctx context.Context, s Source, h Host) (Session, error) {
		values, err := h.Credentials.Lookup(ctx, s.CredentialRef)
		if err != nil || values["token"] != source.CredentialRef {
			t.Fatal("credential reference did not reach the plugin")
		}
		return &stubSession{source: s, close: func() error { closed = true; return nil }}, nil
	}}
	if err := registry.Register(plugin); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(plugin); err == nil {
		t.Fatal("duplicate plugin replaced a registered implementation")
	}
	session, err := registry.Open(context.Background(), normalized[0])
	if err != nil {
		t.Fatal(err)
	}
	_ = session.Close()
	if !closed {
		t.Fatal("session was not closed")
	}
	clone := CloneSources(normalized)
	clone[0].Config[0] = ' '
	if reflect.DeepEqual(clone[0].Config, normalized[0].Config) {
		t.Fatal("source snapshots share mutable plugin configuration")
	}
	data, err := json.MarshalIndent(normalized, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	var roundTrip []Source
	if err := json.Unmarshal(data, &roundTrip); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(roundTrip, normalized) {
		t.Fatal("indented state changed opaque plugin configuration")
	}
}

func TestPagedPluginDirectoryKeepsSourceIdentityAndRejectsBadCursors(t *testing.T) {
	source := testSource("account-one")
	session := &stubSession{source: source, browse: func(ctx context.Context, r BrowseRequest) (Page, error) {
		if r.ParentID != "" || r.Limit != 200 {
			t.Fatal("opaque empty root was rewritten", r)
		}
		switch r.Cursor {
		case "":
			return Page{Items: []Item{{Entry: Entry{Name: "Movie", SourceID: "backend", ItemID: "same-id"}}}, NextCursor: "next"}, nil
		case "next":
			return Page{Items: []Item{{Entry: Entry{Name: "Duplicate", ItemID: "same-id"}}, {Entry: Entry{Name: "Channel", ItemID: "channel"}}}}, nil
		default:
			t.Fatal("unexpected cursor", r.Cursor)
			return Page{}, nil
		}
	}}
	items, err := ReadDirectory(context.Background(), session, "")
	if err != nil || len(items) != 2 || items[0].SourceID != source.ID || items[0].ParentID != "" {
		t.Fatal(items, err)
	}
	same := items[0].Entry
	same.Path = "https://example.test/video?token=renewed"
	if Identity(same) != Identity(items[0].Entry) {
		t.Fatal("temporary URL changed media identity")
	}
	same.SourceID = "account-two"
	if Identity(same) == Identity(items[0].Entry) {
		t.Fatal("two accounts share a media identity")
	}
	if (Entry{SourceID: source.ID}).Key() == (Entry{SourceID: "account-two"}).Key() {
		t.Fatal("empty plugin roots collide")
	}
	session.browse = func(context.Context, BrowseRequest) (Page, error) { return Page{NextCursor: "repeating"}, nil }
	if _, err := ReadDirectory(context.Background(), session, ""); err == nil {
		t.Fatal("repeating cursor was accepted")
	}
	session.browse = func(context.Context, BrowseRequest) (Page, error) {
		return Page{Items: []Item{{Entry: Entry{Path: "temporary-url"}}}}, nil
	}
	if _, err := ReadDirectory(context.Background(), session, ""); err == nil {
		t.Fatal("URL-only remote identity was accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	session.browse = func(context.Context, BrowseRequest) (Page, error) { cancel(); return Page{Items: items}, nil }
	if _, err := ReadDirectory(ctx, session, ""); !errors.Is(err, context.Canceled) {
		t.Fatal("a canceled final page was returned", err)
	}
}

func TestResolvedPlaybackIsEphemeralAndSourceScoped(t *testing.T) {
	headers := http.Header{"Authorization": []string{"Bearer session-token"}}
	source := testSource("cloud-drive")
	session := &stubSession{source: source, resolve: func(context.Context, Reference) (Playback, error) {
		return Playback{Resource: Resource{URL: "https://example.test/video?signature=temporary", Headers: headers}, Subtitle: Resource{URL: "https://example.test/subtitle", Headers: headers}, Seekable: true}, nil
	}}
	ref := Reference{SourceID: source.ID, ItemID: "movie"}
	playback, err := ResolvePlayback(context.Background(), session, ref)
	if err != nil {
		t.Fatal(err)
	}
	headers.Set("Authorization", "replaced")
	if playback.Headers.Get("Authorization") != "Bearer session-token" || playback.Subtitle.Headers.Get("Authorization") != "Bearer session-token" {
		t.Fatal("provider mutated resolved authentication")
	}
	ref.SourceID = "other-account"
	if _, err := ResolvePlayback(context.Background(), session, ref); err == nil {
		t.Fatal("cross-source resolution was allowed")
	}
	session.resolve = func(context.Context, Reference) (Playback, error) {
		return Playback{Resource: Resource{URL: "https://example.test/expired"}, ExpiresAt: time.Now().Add(-time.Second)}, nil
	}
	ref.SourceID = source.ID
	if _, err := ResolvePlayback(context.Background(), session, ref); err == nil {
		t.Fatal("expired resource was accepted")
	}
}

func TestResourceReadsApplyAuthenticationAndLimits(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte("subtitle"))
	}))
	defer server.Close()
	resource := Resource{URL: server.URL, Headers: http.Header{"Authorization": []string{"Bearer test"}}}
	if data, err := ReadResource(context.Background(), resource, 8); err != nil || string(data) != "subtitle" {
		t.Fatal(string(data), err)
	}
	if _, err := ReadResource(context.Background(), resource, 7); err == nil {
		t.Fatal("oversized resource was accepted")
	}
	resource.Headers = nil
	if _, err := ReadResource(context.Background(), resource, 8); err == nil {
		t.Fatal("unauthenticated resource was accepted")
	}
}
