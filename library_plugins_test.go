package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/foursmith/player/internal/media"
	"github.com/foursmith/player/library"
)

type integrationPlugin struct {
	id               string
	revision, closed atomic.Int64
}

func (p *integrationPlugin) Manifest() library.Manifest {
	return library.Manifest{ID: p.id, Name: "Test Cloud", Icon: "library"}
}
func (p *integrationPlugin) Normalize(s library.Source) (library.Source, error) { return s, nil }
func (p *integrationPlugin) Open(ctx context.Context, s library.Source, h library.Host) (library.Session, error) {
	return &integrationSession{source: s, plugin: p}, ctx.Err()
}

type integrationSession struct {
	source library.Source
	plugin *integrationPlugin
}

func (s *integrationSession) Source() library.Source { return s.source }
func (s *integrationSession) Close() error           { s.plugin.closed.Add(1); return nil }
func (s *integrationSession) Browse(ctx context.Context, r library.BrowseRequest) (library.Page, error) {
	if r.ParentID != "" {
		return library.Page{}, fmt.Errorf("parent no longer exists")
	}
	return library.Page{Items: []library.Item{{Entry: library.Entry{Name: "Cloud movie", ItemID: "movie"}, Duration: 600}}}, ctx.Err()
}
func (s *integrationSession) Resolve(ctx context.Context, r library.Reference) (library.Playback, error) {
	return library.Playback{Resource: library.Resource{URL: fmt.Sprintf("https://example.test/video?token=%d", s.plugin.revision.Load()), Headers: http.Header{"Authorization": []string{"Bearer ephemeral"}}}, Subtitle: library.Resource{URL: "https://example.test/subtitle?token=ephemeral"}, Seekable: true}, ctx.Err()
}

func TestNewPluginBrowsesQueuesAndRestoresWithoutProtocolBranches(t *testing.T) {
	plugin := &integrationPlugin{id: t.Name()}
	if err := libraryPlugins.Register(plugin); err != nil {
		t.Fatal(err)
	}
	source := library.Source{ID: "cloud-account", Name: "Cloud", Plugin: plugin.id, Config: library.EncodeConfig(map[string]string{"drive": "videos"}), CredentialRef: "keychain:cloud"}
	a := modeTestApplication(t)
	a.expandedFolders = map[string]bool{}
	a.stored.Libraries = []library.Source{source}
	a.catalog.selected = source.ID
	a.catalog.directoryPath = []catalogFolder{{id: sourceRoot(source)}}
	queued := make(chan func(), 1)
	cancel := readCatalog(a.ctx, source, "", func(f func()) { queued <- f }, func(updated library.Source, items []library.Item, err error) {
		if err != nil {
			t.Error(err)
			return
		}
		a.setCatalogItems(updated, items)
		a.filterCatalog()
	})
	runQueued(t, queued)
	cancel()
	if len(a.catalog.playable) != 1 || a.catalog.playable[0].Path != "" || a.catalog.playable[0].SourceID != source.ID {
		t.Fatal("plugin required a browse-time playback URL", a.catalog.playable)
	}
	a.addRemotePlaylist(source, true)
	roots := a.playlistModel()
	if len(roots) != 2 || roots[1].ID != playlistRemoteFolderID(source.ID, "") || len(roots[1].Children) != 1 {
		t.Fatal("generic empty-root playlist was lost", roots)
	}
	entry := a.entries[1]
	_, first, err := resolveSourceEntry(a.ctx, source, entry, nil)
	if err != nil {
		t.Fatal(err)
	}
	plugin.revision.Add(1)
	_, second, err := resolveSourceEntry(a.ctx, source, entry, nil)
	if err != nil || first.Path == second.Path || first.Key() != second.Key() {
		t.Fatal("fresh playback URLs changed durable identity", first, second, err)
	}
	if !strings.Contains(media.FromEntry(second).Headers, "Bearer ephemeral") {
		t.Fatal("authentication did not reach decoder input")
	}
	a.entries[1] = second
	a.invalidatePlaylist()
	item, valid := a.playbackRecord(second)
	if !valid {
		t.Fatal("plugin row did not enter recent playback")
	}
	// Direct resolution survives a changed parent, without a successful browse.
	item.Playlist.Entries[1].ParentID = "moved-folder"
	a.stored.RecentMedia = []savedPlayback{item}
	path := filepath.Join(t.TempDir(), "state.json")
	if err := saveState(path, a.stored); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	for _, secret := range []string{"token=", "Bearer ephemeral", "subtitle?"} {
		if bytes.Contains(data, []byte(secret)) {
			t.Fatal("session resource leaked into persisted state", secret)
		}
	}
	restored, err := resolveSavedPlayback(a.ctx, loadState(path).RecentMedia[0], nil)
	if err != nil || restored.selected < 0 || restored.entries[restored.selected].Key() != entry.Key() {
		t.Fatal("plugin recent playback did not restore after a parent changed", restored, err)
	}
	if plugin.closed.Load() < 4 {
		t.Fatal("plugin sessions were not released", plugin.closed.Load())
	}
}

func TestPluginPlaybackCancellationDiscardsQueuedResolution(t *testing.T) {
	plugin := &integrationPlugin{id: t.Name()}
	if err := libraryPlugins.Register(plugin); err != nil {
		t.Fatal(err)
	}
	source := library.Source{ID: "iptv", Name: "IPTV", Plugin: plugin.id, Config: library.EncodeConfig(map[string]string{})}
	for _, canceled := range []bool{false, true} {
		queued := make(chan func(), 1)
		completed := false
		cancel := readPlayback(context.Background(), source, library.Entry{SourceID: source.ID, ItemID: "channel"}, 0, func(f func()) { queued <- f }, func(library.Source, library.Entry, error) { completed = true })
		select {
		case update := <-queued:
			if canceled {
				cancel()
			}
			update()
		case <-time.After(3 * time.Second):
			t.Fatal("playback resolution never queued")
		}
		cancel()
		if completed == canceled {
			t.Fatal("queued playback resolution ignored cancellation", canceled, completed)
		}
	}
}

type preparedPlugin struct {
	integrationPlugin
	expired bool
	reports chan library.Playback
}

func (p *preparedPlugin) Open(ctx context.Context, source library.Source, host library.Host) (library.Session, error) {
	return &preparedSession{integrationSession{source: source, plugin: &p.integrationPlugin}, p}, ctx.Err()
}
func (p *preparedPlugin) Report(ctx context.Context, source library.Source, ref library.Reference, playback library.Playback, event library.PlaybackEvent, host library.Host) error {
	if event.Type != "stop" || !event.Failed {
		return errors.New("discarded session must report a failed stop")
	}
	p.reports <- playback
	return nil
}

type preparedSession struct {
	integrationSession
	prepared *preparedPlugin
}

func (s *preparedSession) Prepare(ctx context.Context, request library.PlayRequest) (library.Playback, error) {
	playback := library.Playback{Resource: library.Resource{URL: "https://example.test/video"}, SessionData: []byte(`{"id":"new-session"}`), Seekable: true}
	if s.prepared.expired {
		playback.ExpiresAt = time.Now().Add(-time.Second)
	}
	return playback, ctx.Err()
}

func TestPreparedPlaybackReleasesRejectedAndUndeliveredResults(t *testing.T) {
	for _, expired := range []bool{false, true} {
		t.Run(fmt.Sprint("expired-", expired), func(t *testing.T) {
			plugin := &preparedPlugin{integrationPlugin: integrationPlugin{id: t.Name()}, expired: expired, reports: make(chan library.Playback, 2)}
			if err := libraryPlugins.Register(plugin); err != nil {
				t.Fatal(err)
			}
			source := library.Source{ID: "account", Name: "Account", Plugin: plugin.id, Config: library.EncodeConfig(map[string]string{})}
			entry := library.Entry{SourceID: source.ID, ItemID: "video", Playback: &library.Playback{SessionData: []byte(`{"id":"old-session"}`)}}
			queued := make(chan func(), 1)
			completed := false
			cancel := readPlayback(context.Background(), source, entry, 42, func(f func()) { queued <- f }, func(_ library.Source, _ library.Entry, err error) {
				completed = true
				if err == nil {
					t.Error("expired resource was accepted")
				}
			})
			defer cancel()
			select {
			case update := <-queued:
				if expired {
					update()
				} else {
					cancel() // Drop the queued callback, as window shutdown can do.
				}
			case <-time.After(3 * time.Second):
				t.Fatal("prepared result never queued")
			}
			select {
			case playback := <-plugin.reports:
				if string(playback.SessionData) != `{"id":"new-session"}` {
					t.Fatal("cleanup stopped an existing playback", playback)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("prepared session was not released")
			}
			if completed != expired {
				t.Fatal("canceled result was delivered or rejection was lost")
			}
		})
	}
}
func runQueued(t *testing.T, queued <-chan func()) {
	t.Helper()
	select {
	case update := <-queued:
		update()
	case <-time.After(3 * time.Second):
		t.Fatal("plugin did not queue a result")
	}
}
