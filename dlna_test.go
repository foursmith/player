package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/foursmith/player/internal/dlna"
	"github.com/foursmith/player/internal/library"
	"github.com/foursmith/player/internal/media"
)

func dlnaCatalogFixture(t *testing.T) (dlna.Device, *atomic.Int64) {
	t.Helper()
	var revision atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			fmt.Fprint(w, `<root><device><friendlyName>Test NAS</friendlyName><UDN>uuid:library-test</UDN><serviceList><service><serviceType>urn:schemas-upnp-org:service:ContentDirectory:1</serviceType><controlURL>/control</controlURL></service></serviceList></device></root>`)
			return
		}
		var request struct {
			ID string `xml:"Body>Browse>ObjectID"`
		}
		if err := xml.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		if revision.Load() < 0 {
			http.Error(w, "offline", http.StatusServiceUnavailable)
			return
		}
		var body string
		switch request.ID {
		case "0":
			body = fmt.Sprintf(`<DIDL-Lite><container id="season"><title>Season 2</title></container><item id="movie"><title>Episode 2.mp4</title><class>object.item.videoItem</class><date>2026-11-05T00:00:00Z</date><res protocolInfo="http-get:*:image/jpeg:*">/cover.jpg</res><res protocolInfo="http-get:*:video/mp4:*" duration="00:10:00" size="12345">/stream?id=movie&amp;token=%d</res></item><item id="subtitle"><title>Episode 2.srt</title><res protocolInfo="http-get:*:text/plain:*">/captions.srt</res></item></DIDL-Lite>`, revision.Load())
		case "season":
			body = `<DIDL-Lite><item id="episode10"><title>Episode 10.mp4</title><class>object.item.videoItem</class><res protocolInfo="http-get:*:video/mp4:*" duration="00:20:00">/episode10</res></item></DIDL-Lite>`
		default:
			http.Error(w, "no such object", http.StatusNotFound)
			return
		}
		var escaped bytes.Buffer
		xml.EscapeText(&escaped, []byte(body))
		fmt.Fprintf(w, `<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body><u:BrowseResponse xmlns:u="urn:schemas-upnp-org:service:ContentDirectory:1"><Result>%s</Result><NumberReturned>3</NumberReturned><TotalMatches>3</TotalMatches></u:BrowseResponse></s:Body></s:Envelope>`, escaped.String())
	}))
	t.Cleanup(server.Close)
	return dlna.Device{ID: fmt.Sprintf("dlna-%x", sha256.Sum256([]byte("uuid:library-test"))), Name: "Test NAS", Location: server.URL + "/device.xml", ControlURL: server.URL + "/control", ServiceType: "urn:schemas-upnp-org:service:ContentDirectory:1"}, &revision
}

func TestDLNALibraryBrowsingMetadataStableHistoryAndSavedSources(t *testing.T) {
	device, revision := dlnaCatalogFixture(t)
	items, err := library.BrowseDLNA(context.Background(), device, "0")
	if err != nil || len(items) != 2 || !items[0].Folder || items[0].Name() != "Season 2" {
		t.Fatal("DLNA root did not expose its folders and videos", items, err)
	}
	movie := items[1]
	if movie.Name() != "Episode 2.mp4" || movie.Duration != 600 || movie.Entry.Size != 12345 || movie.Entry.Modified == 0 || !strings.HasSuffix(movie.Entry.Subtitle, "/captions.srt") {
		t.Fatal("DLNA video metadata or subtitles were lost", movie)
	}
	children, err := library.BrowseDLNA(context.Background(), device, "season")
	if err != nil || len(children) != 1 || children[0].Entry.ObjectID != "episode10" || children[0].Duration != 1200 {
		t.Fatal("nested DLNA folder could not be browsed", children, err)
	}
	a := modeTestApplication(t)
	a.entries = []library.Entry{movie.Entry}
	a.recordPlaybackHistory(movie.Entry, media.State{URL: movie.Entry.Path, Position: 120, Metadata: media.Metadata{Duration: 600}})
	a.stored.Libraries, _ = rememberDLNALibraries(nil, []dlna.Device{device})
	a.stored.Libraries[0].Name = "My collection of NAS videos"
	revision.Add(1)
	fresh, err := library.BrowseDLNA(context.Background(), device, "0")
	if err != nil || fresh[1].Path == movie.Path || fresh[1].Key() != movie.Key() {
		t.Fatal("renewed stream URL changed the stable video identity", fresh, err)
	}
	if !a.catalogMatchesPlayback(fresh[1].Entry, media.State{URL: movie.Entry.Path}) || a.prepareVideoMode(fresh[1].Entry) != 120 {
		t.Fatal("renewed DLNA URL lost live progress or saved resume position")
	}
	a.catalog.items = fresh
	a.filterCatalog()
	if !reflect.DeepEqual(a.catalog.visible, []int{0, 1}) {
		t.Fatal("DLNA folders did not sort before videos", a.catalog.visible)
	}
	a.preferences.LibraryFilter = catalogUnwatched
	a.filterCatalog()
	if !reflect.DeepEqual(a.catalog.visible, []int{0}) {
		t.Fatal("watch filtering hid navigation folders or retained a watched video", a.catalog.visible)
	}
	device.Location += "?new-address"
	a.stored.Libraries, _ = rememberDLNALibraries(a.stored.Libraries, []dlna.Device{device})
	if len(a.stored.Libraries) != 1 || a.stored.Libraries[0].Name != "My collection of NAS videos" || a.stored.Libraries[0].Location != device.Location {
		t.Fatal("rediscovery duplicated or renamed a saved DLNA source", a.stored.Libraries)
	}
	path := filepath.Join(t.TempDir(), "state.json")
	if err := saveState(path, a.stored); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if bytes.Contains(data, []byte("token=")) || bytes.Contains(data, []byte("captions.srt")) {
		t.Fatal("temporary stream or subtitle URLs were persisted")
	}
	reloaded := loadState(path)
	if !reflect.DeepEqual(reloaded.Libraries, a.stored.Libraries) || reloaded.History[library.Identity(fresh[1].Entry)].Position != 120 {
		t.Fatal("DLNA source or progress did not survive a restart", reloaded.Libraries)
	}
	queue, selected, play := importedPlaylist([]library.Entry{movie.Entry}, []library.Entry{fresh[1].Entry}, 0, false)
	if len(queue) != 1 || !play || selected != 0 || queue[0].Path != fresh[1].Path {
		t.Fatal("opening a DLNA library retained a stale stream URL", queue)
	}
	if queue, _, _ := importedPlaylist([]library.Entry{movie.Entry}, []library.Entry{fresh[1].Entry}, 0, true); len(queue) != 1 || queue[0].Path != movie.Path {
		t.Fatal("appending duplicated a playing DLNA object or replaced its active URL", queue)
	}
}

func TestDLNALibraryReadDeliversOfflineErrorsWithoutDeletingSource(t *testing.T) {
	device, revision := dlnaCatalogFixture(t)
	saved := library.Saved{ID: device.ID, Name: device.Name, DeviceID: device.ID, Location: device.Location}
	for _, offline := range []bool{false, true} {
		if offline {
			revision.Store(-1)
		}
		queued := make(chan func(), 1)
		completed := false
		cancel := readDLNACatalog(context.Background(), saved, "0", func(update func()) { queued <- update }, func(d dlna.Device, items []library.SavedItem, err error) {
			completed = true
			if (err != nil) != offline || !offline && (d.ID != device.ID || len(items) != 2) {
				t.Error("wrong DLNA read result", items, err)
			}
		})
		select {
		case update := <-queued:
			update()
		case <-time.After(3 * time.Second):
			t.Fatal("DLNA read did not deliver its result")
		}
		cancel()
		if !completed || len(library.NormalizeSaved([]library.Saved{saved})) != 1 {
			t.Fatal("offline DLNA source was discarded or its error was swallowed")
		}
	}
}

func TestDLNALibraryLayoutAcrossLocales(t *testing.T) {
	if err := loadAssets(); err != nil {
		t.Fatal(err)
	}
	device, _ := dlnaCatalogFixture(t)
	items, err := library.BrowseDLNA(context.Background(), device, "0")
	if err != nil {
		t.Fatal(err)
	}
	for _, locale := range []string{"zh-CN", "en", "ja"} {
		t.Run(locale, func(t *testing.T) {
			a := modeTestApplication(t)
			a.preferences.Locale = locale
			a.stored.Libraries, _ = rememberDLNALibraries(nil, []dlna.Device{device})
			a.catalog.selected, a.catalog.name = device.ID, device.Name
			a.catalog.directoryPath = []catalogFolder{{id: "0"}}
			a.catalog.items = items
			a.filterCatalog()
			view := ui.NewTester(a.catalogView, 800, 460)
			for _, label := range []string{a.t("library.openFolder", "Season 2"), a.t("library.playItem", "Episode 2.mp4"), "0:00 / 10:00 · 0%", a.t("library.parent"), a.t("library.root"), a.t("playlist.scanDlna")} {
				if r, ok := view.Find(label); !ok || r.X < 0 || r.X+r.W > 800 || r.Y+r.H > 460 {
					t.Fatalf("DLNA control or metadata missing from minimum window: %q %+v", label, r)
				}
			}
			if _, ok := view.Find(a.t("media.addFiles")); ok {
				t.Fatal("DLNA source exposed local collection editing")
			}
			writeUISnapshot(t, "dlna-"+locale+".png", view)
		})
	}
}

func TestDLNACatalogCancellationDiscardsQueuedResult(t *testing.T) {
	device, _ := dlnaCatalogFixture(t)
	queued := make(chan func(), 1)
	completed := false
	cancel := readDLNACatalog(context.Background(), library.Saved{DeviceID: device.ID, Location: device.Location}, "0", func(update func()) { queued <- update }, func(dlna.Device, []library.SavedItem, error) { completed = true })
	select {
	case update := <-queued:
		cancel()
		update()
	case <-time.After(3 * time.Second):
		cancel()
		t.Fatal("DLNA scan failed to queue its result")
	}
	if completed {
		t.Fatal("a canceled folder selection applied an old DLNA result")
	}
}
