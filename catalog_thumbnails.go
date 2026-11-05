package main

import (
	"context"
	"path/filepath"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/foursmith/player/internal/library"
	"github.com/foursmith/player/internal/thumbnail"
)

type catalogPreview struct {
	bitmap   *ui.Bitmap
	duration float64
	used     uint64
}

// UI state stays on the main thread; one worker owns disk IO and decoding.
type catalogThumbnails struct {
	images  map[string]catalogPreview
	pending map[string]bool
	failed  map[string]bool
	jobs    chan library.Entry
	cancel  context.CancelFunc
	clock   uint64
}

func (a *application) stopCatalogThumbnails() {
	if t := a.catalog.thumbnails; t != nil {
		if t.cancel != nil {
			t.cancel()
			t.cancel = nil
		}
		t.jobs = nil
		t.pending = nil
	}
}

func (a *application) startCatalogThumbnails() {
	a.stopCatalogThumbnails()
	if a.catalogWindow == nil {
		return
	}
	if a.catalog.thumbnails == nil {
		a.catalog.thumbnails = &catalogThumbnails{images: map[string]catalogPreview{}}
	}
	t := a.catalog.thumbnails
	t.failed, t.pending = map[string]bool{}, map[string]bool{}
	t.jobs = make(chan library.Entry, 32)
	ctx, cancel := context.WithCancel(a.ctx)
	t.cancel = cancel
	jobs, window := t.jobs, a.window
	// Cache failure only disables disk reuse; previews can still be displayed.
	dir, _ := mygo.App.Path(mygo.PathUserData)
	cache := &thumbnail.Cache{}
	if dir != "" {
		cache.Dir = filepath.Join(dir, "thumbnails")
	}
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case entry := <-jobs:
				loadCtx, stop := context.WithTimeout(ctx, 6*time.Second)
				preview, err := cache.Load(loadCtx, entry)
				stop()
				window.Update(func() {
					if ctx.Err() != nil || a.shutdown {
						return
					}
					key := library.Identity(entry)
					delete(t.pending, key)
					if err != nil {
						t.failed[key] = true
					}
					if preview.Image != nil || preview.Duration > 0 {
						t.clock++
						value := catalogPreview{duration: preview.Duration, used: t.clock}
						if preview.Image != nil {
							value.bitmap = ui.NewBitmap(preview.Image)
						}
						t.images[key] = value
						t.trim()
					}
					if a.catalogWindow != nil {
						a.catalogWindow.Invalidate()
					}
				})
			}
		}
	}()
}

func (t *catalogThumbnails) trim() {
	for len(t.images) > 128 {
		var oldest string
		var used uint64 = ^uint64(0)
		for key, preview := range t.images {
			if preview.used < used {
				oldest, used = key, preview.used
			}
		}
		delete(t.images, oldest)
	}
}

func (a *application) catalogThumbnail(item library.SavedItem) *ui.Bitmap {
	t := a.catalog.thumbnails
	if t == nil || item.Error != nil || item.Folder {
		return nil
	}
	key := library.Identity(item.Entry)
	if preview, ok := t.images[key]; ok {
		t.clock++
		preview.used = t.clock
		t.images[key] = preview
		if preview.bitmap != nil {
			return preview.bitmap
		}
	}
	if t.jobs != nil && !t.pending[key] && !t.failed[key] {
		select {
		case t.jobs <- item.Entry:
			t.pending[key] = true
		default:
		}
	}
	return nil
}
