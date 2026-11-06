package main

import (
	"context"
	"errors"
	"image"
	"math"
	"slices"
	"sync/atomic"

	"github.com/egoist/mygo/ui"
	"github.com/foursmith/player/internal/media"
)

type seekPreviewJob struct {
	sequence uint64
	keys     []int64
	precise  bool
}

type seekPreviewResult struct {
	key     int64
	image   *image.RGBA
	err     error
	precise bool
}

type seekPreviewImage struct {
	bitmap  *ui.Bitmap
	used    uint64
	precise bool
}

// The main thread owns the bounded image cache; one worker owns the decoder.
// Jobs replace queued pointer positions and interrupt obsolete long prerolls.
type seekPreviews struct {
	source           string
	cancel           context.CancelFunc
	jobs             chan seekPreviewJob
	results          chan seekPreviewResult
	sequence         atomic.Uint64
	focus            atomic.Int64
	requested        []int64
	requestedPrecise bool
	images           map[int64]seekPreviewImage
	clock            uint64
}

func (a *application) stopSeekPreviews() {
	if a.seekPreviews != nil {
		a.seekPreviews.cancel()
		a.seekPreviews = nil
	}
}

func newSeekPreviews(ctx context.Context, source string, chapters []media.Chapter, invalidate func()) *seekPreviews {
	ctx, cancel := context.WithCancel(ctx)
	chapters = slices.Clone(chapters)
	t := &seekPreviews{source: source, cancel: cancel, jobs: make(chan seekPreviewJob, 1), results: make(chan seekPreviewResult, 16), images: map[int64]seekPreviewImage{}}
	go func() {
		var decoder *media.SeekPreview
		var artwork *media.ChapterArtwork
		var artworkErr error
		defer func() {
			if decoder != nil {
				decoder.Close()
			}
			if artwork != nil {
				artwork.Close()
			}
		}()
		for {
			select {
			case <-ctx.Done():
				return
			case job := <-t.jobs:
				for _, key := range job.keys {
					if ctx.Err() != nil || t.sequence.Load() != job.sequence {
						break
					}
					obsolete := func() bool {
						if ctx.Err() != nil {
							return true
						}
						if t.sequence.Load() == job.sequence {
							return false
						}
						focus := t.focus.Load()
						// Finish a nearby frame instead of repeatedly flushing a
						// GOP as the pointer moves; distant jumps still interrupt.
						return key < 0 || focus < 0 || key-focus > 1e6 || focus-key > 1e6
					}
					var img *image.RGBA
					var err error
					if key < 0 {
						index := -key - 1
						if index >= int64(len(chapters)) {
							err = errors.New("invalid chapter artwork index")
						} else {
							if artwork == nil && artworkErr == nil {
								artwork, artworkErr = media.OpenChapterArtwork(ctx, source)
							}
							if artworkErr != nil {
								err = artworkErr
							} else {
								img, err = artwork.Image(chapters[index].Start)
							}
						}
					} else {
						if decoder == nil {
							decoder, err = media.OpenSeekPreview(ctx, source)
						}
						if err == nil {
							img, err = decoder.Image(float64(key)/1e6, job.precise, obsolete)
						}
					}
					if ctx.Err() != nil {
						return
					}
					if errors.Is(err, context.Canceled) || img == nil && obsolete() {
						break
					}
					select {
					case t.results <- seekPreviewResult{key: key, image: img, err: err, precise: job.precise || key < 0}:
						invalidate()
					case <-ctx.Done():
						return
					}
				}
			}
		}
	}()
	return t
}

func (t *seekPreviews) collect() {
	for {
		select {
		case result := <-t.results:
			// Keep an already displayed frame for repeated results at the same
			// quality. Only an explicit precise request may upgrade a quick image.
			if existing, ok := t.images[result.key]; ok && existing.bitmap != nil && (existing.precise || !result.precise) {
				continue
			}
			t.clock++
			preview := seekPreviewImage{used: t.clock, precise: result.precise}
			if result.err == nil && result.image != nil {
				preview.bitmap = ui.NewBitmap(result.image)
			}
			t.images[result.key] = preview
		default:
			t.trim(t.requested)
			return
		}
	}
}

func (t *seekPreviews) request(keys []int64, precise bool) {
	if slices.Equal(t.requested, keys) && t.requestedPrecise == precise {
		return
	}
	t.requested = slices.Clone(keys)
	t.requestedPrecise = precise
	focus := int64(-1)
	if len(keys) > 0 {
		focus = keys[0]
	}
	t.focus.Store(focus)
	sequence := t.sequence.Add(1)
	var missing []int64
	for _, key := range keys {
		if preview, ok := t.images[key]; ok {
			t.clock++
			preview.used = t.clock
			t.images[key] = preview
			if precise && !preview.precise && !slices.Contains(missing, key) {
				missing = append(missing, key)
			}
		} else if !slices.Contains(missing, key) {
			missing = append(missing, key)
		}
	}
	t.trim(keys)
	select {
	case <-t.jobs:
	default:
	}
	if len(missing) > 0 {
		t.jobs <- seekPreviewJob{sequence: sequence, keys: missing, precise: precise}
	}
}

func (t *seekPreviews) trim(keys []int64) {
	// Evict only images outside the visible strip, including failed frames.
	for len(t.images) > 96 {
		oldest, used, found := int64(0), ^uint64(0), false
		for key, img := range t.images {
			if img.used < used && !slices.Contains(keys, key) {
				oldest, used, found = key, img.used, true
			}
		}
		if !found {
			break
		}
		delete(t.images, oldest)
	}
}

func previewKey(at float64, metadata media.Metadata, precise bool) int64 {
	fps := max(1, metadata.FrameRate)
	if !precise {
		at = math.Floor(at*2) / 2
	}
	at = math.Round(at*fps) / fps
	at = max(0, min(at, max(0, metadata.Duration-1/fps)))
	return int64(math.Round(at * 1e6))
}

func (a *application) currentSeekPreviews(c *ui.Context, state media.State) *seekPreviews {
	if state.URL == "" {
		return nil
	}
	if a.seekPreviews == nil || a.seekPreviews.source != state.URL {
		a.stopSeekPreviews()
		a.seekPreviews = newSeekPreviews(a.ctx, state.URL, state.Metadata.Chapters, c.Services().Invalidate)
	}
	a.seekPreviews.collect()
	return a.seekPreviews
}

func chapterArtworkKey(index int) int64 { return -int64(index) - 1 }

func (a *application) seekPreviewImage(key int64) (*ui.Bitmap, bool) {
	if a.seekPreviews == nil {
		return nil, false
	}
	preview, ready := a.seekPreviews.images[key]
	return preview.bitmap, ready
}
