package dlnaserver

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/foursmith/player/internal/library"
)

func TestCloseDisconnectsActivePlaybackAndClosesListeners(t *testing.T) {
	path := filepath.Join(t.TempDir(), "large.mp4")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(64 << 20); err != nil {
		file.Close()
		t.Fatal(err)
	}
	file.Close()
	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{ctx: ctx, cancel: cancel, name: "Test", sources: []library.Saved{{ID: "files", Name: "Files", Paths: []string{path}, ShareDLNA: true}}}
	s.objects, err = readCatalog(ctx, s.name, s.sources)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	s.ssdp = &discovery{server: s, listener: listener}
	server := httptest.NewServer(s)
	s.http = server.Config
	t.Cleanup(func() { s.Close(); server.Close() })
	client := &http.Client{Timeout: 3 * time.Second}
	response, err := client.Get(resourceURL(server.URL, "media", s.objects[objectID("files", "file", path)]))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal("stream did not start", response.StatusCode)
	}
	s.Close()
	if count, err := io.Copy(io.Discard, response.Body); err == nil || count == 64<<20 {
		t.Fatal("closing sharing did not interrupt the active transfer", count, err)
	}
	if next, err := client.Get(server.URL + "/device.xml"); err == nil {
		next.Body.Close()
		t.Fatal("HTTP listener remained open")
	}
}
