package dlnaserver

import (
	"context"
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/foursmith/player/internal/library"
)

func TestMetadataSchedulerQueuesOnlyMissingUniqueFiles(t *testing.T) {
	s := &Server{objects: catalog{}, metadata: &metadataIndex{byKey: map[string][]string{}, known: map[string]library.MediaInfo{}, active: map[string]bool{}, failed: map[string]time.Time{}}}
	for _, id := range []string{"cached", "active", "failed", "retry", "missing"} {
		item := object{ID: id, MetadataPath: id + ".mp4", Size: 100, Modified: 1}
		key := library.Identity(item.metadataEntry())
		s.objects[id] = item
		s.metadata.byKey[key] = []string{id}
		switch id {
		case "cached":
			s.metadata.known[key] = library.MediaInfo{Duration: 10, Width: 320, Height: 160}
		case "active":
			s.metadata.active[key] = true
		case "failed":
			s.metadata.failed[key] = time.Now().Add(time.Hour)
		case "retry":
			s.metadata.failed[key] = time.Now().Add(-time.Second)
		case "missing":
			other := item
			other.ID = "same-file-in-another-library"
			s.objects[other.ID] = other
			s.metadata.byKey[key] = append(s.metadata.byKey[key], other.ID)
		}
	}
	items := s.pendingMetadata()
	if len(items) != 2 || items[0].ID == items[1].ID {
		t.Fatal("scheduler repeated cached, active, failed, or duplicate work", items)
	}
	for _, item := range items {
		if item.ID != "retry" && item.ID != "missing" {
			t.Fatal("scheduler queued a file that did not need probing", item)
		}
		s.metadata.known[library.Identity(item.metadataEntry())] = library.MediaInfo{Duration: 10}
	}
	if items := s.pendingMetadata(); len(items) != 0 {
		t.Fatal("cached refresh enqueued metadata work", items)
	}
}

func TestMetadataIndexDoesNotBlockBrowsingAndSharesCachedResults(t *testing.T) {
	file := filepath.Join(t.TempDir(), "clip.mp4")
	writeFile(t, file, "video")
	s, base := fixture(t, []library.Saved{
		{ID: "first", Name: "First", Paths: []string{file}, ShareDLNA: true},
		{ID: "second", Name: "Second", Paths: []string{file}, ShareDLNA: true},
	})
	started, release := make(chan struct{}, 2), make(chan struct{})
	var calls atomic.Int32
	s.startMetadata(func(ctx context.Context, entry library.Entry) (library.MediaInfo, error) {
		call := calls.Add(1)
		started <- struct{}{}
		select {
		case <-release:
			return library.MediaInfo{Duration: 125.5 + float64(call), Width: 8192, Height: 4096, BitRate: 80000000}, nil
		case <-ctx.Done():
			return library.MediaInfo{}, ctx.Err()
		}
	})
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("metadata worker did not start")
	}
	id := objectID("first", "file", file)
	// A blocked probe must not hold the catalog lock or delay directory requests.
	done := make(chan soapResult, 1)
	go func() {
		done <- soap(t, base, contentType, "Browse", browseArgs(id, "BrowseMetadata", "res@duration,res@resolution", "0", "0", ""), 200)
	}()
	select {
	case result := <-done:
		if !strings.Contains(result.Result, "<res ") {
			t.Fatal("attribute-only filtering dropped the media resource", result.Result)
		}
	case <-time.After(time.Second):
		t.Fatal("directory request waited for a metadata probe")
	}
	close(release)
	awaitCatalog(t, s, func(c catalog) bool {
		return c[id].Info.Duration == 126.5 && c[objectID("second", "file", file)].Info.Duration == 126.5
	})
	if calls.Load() != 1 {
		t.Fatal("the same video was probed for each shared library", calls.Load())
	}
	for _, filter := range []string{"*", "res@duration,res@resolution,res@bitrate", "res@*"} {
		result := soap(t, base, contentType, "Browse", browseArgs(id, "BrowseMetadata", filter, "0", "0", ""), 200)
		var didl struct {
			Item struct {
				Resource struct {
					Duration   string `xml:"duration,attr"`
					Resolution string `xml:"resolution,attr"`
					Bitrate    int64  `xml:"bitrate,attr"`
					URL        string `xml:",chardata"`
				} `xml:"res"`
			} `xml:"item"`
		}
		if err := xml.Unmarshal([]byte(result.Result), &didl); err != nil {
			t.Fatal(err)
		}
		resource := didl.Item.Resource
		if resource.Duration != "0:02:06.500" || resource.Resolution != "8192x4096" || resource.Bitrate != 10000000 || resource.URL == "" {
			t.Fatalf("metadata/filter %q: %+v", filter, resource)
		}
	}
	if err := os.WriteFile(file, []byte("replacement video"), 0600); err != nil {
		t.Fatal(err)
	}
	s.Refresh()
	awaitCatalog(t, s, func(c catalog) bool { return c[id].Info.Duration == 127.5 })
	if calls.Load() != 2 {
		t.Fatal("file replacement did not invalidate only its old metadata", calls.Load())
	}
}

func TestLateMetadataCannotRestoreUnsharedObjects(t *testing.T) {
	file := filepath.Join(t.TempDir(), "clip.mp4")
	writeFile(t, file, "video")
	s, _ := fixture(t, []library.Saved{{ID: "first", Name: "First", Paths: []string{file}, ShareDLNA: true}})
	started, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	s.startMetadata(func(context.Context, library.Entry) (library.MediaInfo, error) {
		close(started)
		<-release
		defer close(finished)
		return library.MediaInfo{Duration: 10, Width: 320, Height: 160}, nil
	})
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("metadata worker did not start")
	}
	s.SetSources(nil)
	close(release)
	select {
	case <-finished:
	case <-time.After(3 * time.Second):
		t.Fatal("metadata worker did not finish")
	}
	awaitCatalog(t, s, func(c catalog) bool { return len(s.metadata.active) == 0 })
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.objects) != 1 || len(s.metadata.known) != 0 {
		t.Fatal("an abandoned metadata result restored unshared content", s.objects)
	}
}
