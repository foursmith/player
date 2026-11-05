package dlnaserver

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/foursmith/player/internal/dlna"
	"github.com/foursmith/player/internal/library"
)

func TestUnsharingOneLibraryPreservesOtherLibrariesAndTransfers(t *testing.T) {
	dir := t.TempDir()
	first, second := filepath.Join(dir, "first.mp4"), filepath.Join(dir, "second.mp4")
	const size = 16 << 20
	for _, path := range []string{first, second} {
		file, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		err = file.Truncate(size)
		file.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	sources := []library.Saved{
		{ID: "first", Name: "First", Paths: []string{first}, ShareDLNA: true},
		{ID: "second", Name: "Second", Paths: []string{second}, ShareDLNA: true},
		{ID: "private", Name: "Private", Paths: []string{second}},
		{ID: "remote", Name: "Remote", DeviceID: "nas", ShareDLNA: true},
	}
	s, base := fixture(t, sources)
	device, err := dlna.Connect(context.Background(), fmt.Sprintf("dlna-%x", sha256.Sum256([]byte(s.udn))), base+"/device.xml")
	if err != nil {
		t.Fatal(err)
	}
	roots, err := device.Browse(context.Background(), "0")
	if err != nil || len(roots) != 2 {
		t.Fatal("unshared or remote libraries were published", roots, err)
	}
	s.mu.RLock()
	firstItem, secondItem := s.objects[objectID("first", "file", first)], s.objects[objectID("second", "file", second)]
	s.mu.RUnlock()
	firstURL, secondURL := resourceURL(base, "media", firstItem), resourceURL(base, "media", secondItem)
	client := &http.Client{Timeout: 5 * time.Second}
	play := func(address string) *http.Response {
		t.Helper()
		response, err := client.Get(address)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { response.Body.Close() })
		if response.StatusCode != 200 {
			t.Fatal("video stream did not start", response.StatusCode)
		}
		return response
	}
	firstStream, secondStream := play(firstURL), play(secondURL)
	sources[0].ShareDLNA = false
	s.SetSources(sources)
	fetch(t, "GET", firstURL, "", 404)
	fetch(t, "HEAD", secondURL, "", 200)
	if count, err := io.Copy(io.Discard, firstStream.Body); err == nil || count == size {
		t.Fatal("unsharing did not stop that library's active transfer", count, err)
	}
	if count, err := io.Copy(io.Discard, secondStream.Body); err != nil || count != size {
		t.Fatal("unsharing interrupted another library's transfer", count, err)
	}
	roots, err = device.Browse(context.Background(), "0")
	if err != nil || len(roots) != 1 || roots[0].ID != objectID("second") {
		t.Fatal("unsharing changed the wrong libraries", roots, err)
	}
	awaitCatalog(t, s, func(c catalog) bool {
		_, firstPublished := c[firstItem.ID]
		return !firstPublished && c[secondItem.ID].File != ""
	})
}
