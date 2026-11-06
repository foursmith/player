package providers_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/foursmith/player/internal/providers"
	"github.com/foursmith/player/library"
)

func TestLocalPluginResolvesItsBrowsedReferences(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "video.mp4")
	if err := os.WriteFile(path, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	source := library.Source{ID: "folder", Name: "Folder", Plugin: library.LocalID, Config: library.EncodeConfig(library.LocalConfig{Folder: dir})}
	session, err := providers.NewRegistry().Open(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	items, err := library.ReadDirectory(context.Background(), session, "")
	if err != nil || len(items) != 1 {
		t.Fatal(items, err)
	}
	playback, err := library.ResolvePlayback(context.Background(), session, items[0].Reference())
	if err != nil || playback.URL != path || !playback.Seekable {
		t.Fatal("local plugin could not resolve a browsed file", playback, err)
	}
}
