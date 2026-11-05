package thumbnail

import (
	"context"
	"errors"
	"image"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/foursmith/player/internal/library"
	"github.com/foursmith/player/internal/media"
)

func TestPreviewCacheReusesOpaquePrivateFilesAndInvalidatesReplacedVideos(t *testing.T) {
	dir := t.TempDir()
	calls := 0
	cache := &Cache{Dir: dir, render: func(context.Context, string) (media.Preview, error) {
		calls++
		return media.Preview{Image: image.NewRGBA(image.Rect(0, 0, 16, 8)), Duration: 123.5}, nil
	}}
	entry := library.Entry{Path: filepath.Join(t.TempDir(), "private-title.mp4"), Size: 100, Modified: 1}
	for range 2 {
		preview, err := cache.Load(context.Background(), entry)
		if err != nil || preview.Image.Bounds().Dx() != 16 || preview.Duration != 123.5 {
			t.Fatalf("preview load: %v", err)
		}
	}
	if calls != 1 {
		t.Fatal("a cached preview was decoded again", calls)
	}
	path := filepath.Join(dir, library.Identity(entry)+".jpg")
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm()&0077 != 0 {
		t.Fatal("preview is missing or accessible to other users", err)
	}
	if err := os.WriteFile(path, []byte("corrupt preview"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.Load(context.Background(), entry); err != nil || calls != 2 {
		t.Fatal("corrupt preview did not regenerate", err, calls)
	}
	entry.Modified++
	if _, err := cache.Load(context.Background(), entry); err != nil || calls != 3 {
		t.Fatal("a replaced file reused a stale preview", err, calls)
	}
	files, _ := os.ReadDir(dir)
	if len(files) != 4 {
		t.Fatal("unexpected preview files", len(files))
	}
	for _, file := range files {
		if len(file.Name()) != 68 && len(file.Name()) != 69 {
			t.Fatal("preview filename is not an opaque hash", file.Name())
		}
	}
}

func TestPreviewCachePrunesOnlyItsOldestImages(t *testing.T) {
	dir := t.TempDir()
	for i := range 4 {
		key := library.Identity(library.Entry{Path: string(rune('a' + i))})
		path := filepath.Join(dir, key+".jpg")
		if err := os.WriteFile(path, nil, 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, key+".json"), []byte(`{"Duration":100}`), 0600); err != nil {
			t.Fatal(err)
		}
		at := time.Unix(int64(i+1), 0)
		os.Chtimes(path, at, at)
	}
	other := filepath.Join(dir, "unrelated.jpg")
	if err := os.WriteFile(other, nil, 0600); err != nil {
		t.Fatal(err)
	}
	cache := &Cache{Dir: dir}
	cache.prune(2)
	for i := range 4 {
		_, err := os.Stat(filepath.Join(dir, library.Identity(library.Entry{Path: string(rune('a' + i))})+".jpg"))
		if (err == nil) != (i >= 2) {
			t.Fatal("preview eviction did not retain the newest entries", i, err)
		}
		_, err = os.Stat(filepath.Join(dir, library.Identity(library.Entry{Path: string(rune('a' + i))})+".json"))
		if (err == nil) != (i >= 2) {
			t.Fatal("preview eviction left orphaned duration metadata", i, err)
		}
	}
	if _, err := os.Stat(other); err != nil {
		t.Fatal("cache pruning touched an unrelated file", err)
	}
}

func TestPreviewCancellationDoesNotWriteAnAbandonedResult(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	dir := t.TempDir()
	cache := &Cache{Dir: dir, render: func(context.Context, string) (media.Preview, error) {
		cancel()
		return media.Preview{Image: image.NewRGBA(image.Rect(0, 0, 16, 8)), Duration: 100}, nil
	}}
	_, err := cache.Load(ctx, library.Entry{Path: "/media/clip.mp4"})
	if !errors.Is(err, context.Canceled) {
		t.Fatal("an abandoned preview was published", err)
	}
	if files, _ := os.ReadDir(dir); len(files) != 0 {
		t.Fatal("an abandoned preview was cached")
	}
}

func TestDurationSurvivesThumbnailDecodeFailure(t *testing.T) {
	dir := t.TempDir()
	failed := errors.New("thumbnail decoding failed")
	cache := &Cache{Dir: dir, render: func(context.Context, string) (media.Preview, error) {
		return media.Preview{Duration: 90}, failed
	}}
	preview, err := cache.Load(context.Background(), library.Entry{Path: "/Media/video.mp4"})
	if !errors.Is(err, failed) || preview.Duration != 90 || preview.Image != nil {
		t.Fatal("usable duration was lost with a failed thumbnail", preview, err)
	}
	if files, _ := os.ReadDir(dir); len(files) != 0 {
		t.Fatal("an incomplete thumbnail was stored as a cache hit")
	}
}
