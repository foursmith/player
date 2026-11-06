package main

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/foursmith/player/internal/media"
)

func timelineTestView(t *testing.T, a *application, state media.State, width, height int) *ui.Tester {
	t.Helper()
	if a.ctx == nil && state.URL != "" {
		a.ctx = t.Context()
		t.Cleanup(a.stopSeekPreviews)
	}
	return ui.NewTester(func(c *ui.Context) {
		a.width, a.height = c.Size()
		c.SetTheme(darkPlayerTheme)
		ui.Box(c).Fill().Background(ui.Hex("#28333e")).Children(func() {
			ui.Column(c).Absolute().Left(24).Right(24).Bottom(24).Children(func() { a.timeline(c, state) })
			a.timelinePreview(c, state)
		})
	}, width, height)
}

func TestTimelineHoverFineDragCancelAndChapterNavigation(t *testing.T) {
	if err := loadAssets(); err != nil {
		t.Fatal(err)
	}
	file := playbackRenderFixture(t)
	for _, test := range []struct {
		name string
		http bool
	}{{"local", false}, {"dlna-http", true}} {
		t.Run(test.name, func(t *testing.T) {
			source := file
			if test.http {
				server := httptest.NewServer(http.FileServer(http.Dir(filepath.Dir(file))))
				t.Cleanup(server.Close)
				source = server.URL + "/" + filepath.Base(file)
			}
			player := media.NewPlayer(nil)
			defer player.Close()
			player.Open(media.Source{URL: source})
			waitRenderState(t, player, func(s media.State) bool { return !s.Loading && s.FrameID != 0 })
			player.Pause()
			state := waitRenderState(t, player, func(s media.State) bool { return !s.Playing })
			state.Metadata.Chapters = []media.Chapter{{Start: 0, End: 1, Title: "Opening"}, {Start: 1, End: 2, Title: "Second scene"}}
			a := &application{player: player, preferences: defaultPreferences()}
			view := timelineTestView(t, a, state, 900, 560)
			bounds, ok := view.Find(a.t("timeline.playbackPosition"))
			if !ok {
				t.Fatal("timeline not accessible")
			}
			x, y := bounds.X+bounds.W*.3, bounds.Y+bounds.H/2
			view.Move(x, y)
			if !view.HasText(a.t("timeline.pullUp")) || !view.HasText("Opening") {
				t.Fatal("hover preview or chapter missing")
			}
			if player.Snapshot().Generation != state.Generation {
				t.Fatal("hover sought the playing video")
			}
			view.Press(x, y)
			view.Move(x+40, y)
			coarse := a.scrubTime
			view.Move(x+40, y-55)
			if !a.seekUI.fine || math.Abs(a.scrubTime-coarse) > .5/state.Metadata.FrameRate || !view.HasText(a.t("timeline.releaseToSeek")) {
				t.Fatal("pull-up failed or moved the target")
			}
			view.Move(x+80, y-55)
			if a.scrubTime <= coarse || player.Snapshot().Generation != state.Generation {
				t.Fatal("fine drag did not preview without seeking")
			}
			view.CancelPress()
			if a.scrubbing || a.seekUI.fine || player.Snapshot().Generation != state.Generation {
				t.Fatal("capture cancellation committed a seek")
			}
			view.Press(x, y)
			view.Move(x+60, y-60)
			view.Key(0, ui.KeyEscape)
			view.Release(x+60, y-60)
			if a.scrubbing || player.Snapshot().Generation != state.Generation {
				t.Fatal("Escape committed the canceled seek")
			}
			view.Press(x, y)
			view.Move(x+30, y-60)
			target := a.scrubTime
			view.Release(x+30, y-60)
			ready := waitRenderState(t, player, func(s media.State) bool { return !s.Loading && s.Generation > state.Generation })
			if frame := player.Frame(); frame == nil {
				t.Fatal("released seek did not publish a frame")
			} else {
				defer frame.Close()
				if frame.PTS() < target-.0001 || frame.PTS() > target+1/state.Metadata.FrameRate+.0001 {
					t.Fatalf("released seek presented %.6f, want %.6f within one source frame", frame.PTS(), target)
				}
			}
			if math.Abs(ready.Position-target) > .1 {
				t.Fatalf("released at wrong target: %.4f want %.4f", ready.Position, target)
			}
			if err := view.Click(a.t("timeline.chapters")); err != nil {
				t.Fatal(err)
			}
			if !view.HasText("Second scene") {
				t.Fatal("chapter list missing")
			}
			writeUISnapshot(t, "timeline-chapters-navigation-"+test.name+".png", view)
			if err := view.Click("Second scene"); err != nil {
				t.Fatal(err)
			}
			chapter := waitRenderState(t, player, func(s media.State) bool { return !s.Loading && s.Generation > ready.Generation })
			if chapter.Position < 1 || chapter.Position > 1.15 || a.seekUI.chaptersOpen {
				t.Fatal("chapter selection did not seek and dismiss")
			}
		})
	}
}

func TestFineDragUsesSourceFramesAndClampsAtEdges(t *testing.T) {
	for _, fps := range []float64{24, 29.97, 60} {
		t.Run(fmt.Sprint(fps), func(t *testing.T) {
			a := &application{}
			state := media.State{Metadata: media.Metadata{Duration: 7200, FrameRate: fps}}
			a.timelineInput(ui.InputEvent{Kind: ui.InputPointerDown, X: 500, Y: 12}, 1000, state)
			a.timelineInput(ui.InputEvent{Kind: ui.InputPointerMove, X: 500, Y: -50}, 1000, state)
			a.timelineInput(ui.InputEvent{Kind: ui.InputPointerMove, X: 510, Y: -50}, 1000, state)
			if math.Abs(a.scrubTime-3600-10/fps) > 1/fps {
				t.Fatal("fine drag retained full-duration sensitivity", a.scrubTime)
			}
			if math.Abs(a.scrubTime*fps-math.Round(a.scrubTime*fps)) > .001 {
				t.Fatal("target is not frame aligned")
			}
			a.timelineInput(ui.InputEvent{Kind: ui.InputPointerMove, X: -1e8, Y: -50}, 1000, state)
			if a.scrubTime != 0 {
				t.Fatal("fine drag went before the source")
			}
			a.timelineInput(ui.InputEvent{Kind: ui.InputPointerMove, X: 1e8, Y: -50}, 1000, state)
			if a.scrubTime != 7200 {
				t.Fatal("fine drag went past the source")
			}
		})
	}
}

func TestTimelineEmphasizesOnlyHoveredOrPlayingChapter(t *testing.T) {
	metadata := media.Metadata{Duration: 300, Chapters: []media.Chapter{
		{Start: 0, End: 100}, {Start: 100, End: 200}, {Start: 200, End: 300},
	}}
	for _, test := range []struct {
		name            string
		position, hover float64
		hovered         bool
		selected        int
	}{
		{"playing", 150, 0, false, 1},
		{"hover overrides playback", 150, 250, true, 2},
		{"hover same chapter", 150, 130, true, 1},
		{"hover earlier chapter", 250, 50, true, 0},
		{"pointer leaves", 250, 50, false, 2},
		{"chapter boundary", 100, 0, false, 1},
		{"end of movie", 300, 0, false, 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			view := ui.NewTester(func(c *ui.Context) {
				c.SetTheme(darkPlayerTheme)
				ui.Box(c).Fill().Background(ui.RGB(0, 0, 0)).Draw(func(p *ui.Painter, r ui.Rect) {
					paintTimelineTrack(p, r, test.position, metadata, test.hovered, test.hover)
				})
			}, 600, 40)
			img := view.Image()
			for i, x := range []int{50, 250, 450} {
				// This row sits above the ordinary track, away from the thumb.
				thick := img.RGBAAt(x, 16).R > 20
				if thick != (i == test.selected) {
					t.Fatalf("chapter %d thick=%v, only chapter %d should be emphasized", i, thick, test.selected)
				}
				for y := 0; y < 20; y++ {
					upper, lower := img.RGBAAt(x, y), img.RGBAAt(x, 39-y)
					if upper != lower {
						t.Fatalf("chapter %d is not centered on the thumb: y=%d upper=%v lower=%v", i, y, upper, lower)
					}
				}
			}
			writeUISnapshot(t, fmt.Sprintf("timeline-emphasis-%d-%t-%d.png", int(test.position), test.hovered, int(test.hover)), view)
		})
	}
}

func TestTimelinePreviewLayoutsAcrossLocales(t *testing.T) {
	if err := loadAssets(); err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, 240, 135))
	for y := 0; y < 135; y++ {
		for x := 0; x < 240; x++ {
			img.SetRGBA(x, y, color.RGBA{R: uint8(x), G: uint8(y + 60), B: 120, A: 255})
		}
	}
	bitmap := ui.NewBitmap(img)
	for _, locale := range []string{"en", "zh-CN", "ja"} {
		for _, size := range [][2]int{{400, 420}, {1000, 640}} {
			t.Run(fmt.Sprintf("%s-%d", locale, size[0]), func(t *testing.T) {
				player := media.NewPlayer(nil)
				defer player.Close()
				a := &application{player: player, preferences: defaultPreferences()}
				a.preferences.Locale = locale
				a.seekPreviews = &seekPreviews{images: map[int64]seekPreviewImage{}, requestedPrecise: true}
				state := media.State{Position: 150, Metadata: media.Metadata{Duration: 600, FrameRate: 25, Chapters: []media.Chapter{{Start: 0, End: 120, Title: "Opening"}, {Start: 120, End: 600, Title: "A chapter with a longer title / 第二章 / チャプター"}}}}
				for at := 0.0; at < 600; at += .04 {
					a.seekPreviews.images[int64(math.Round(at*1e6))] = seekPreviewImage{bitmap: bitmap}
				}
				view := timelineTestView(t, a, state, size[0], size[1])
				bounds, _ := view.Find(a.t("timeline.playbackPosition"))
				for _, fraction := range []float32{0, .5, .999} {
					view.Move(bounds.X+bounds.W*fraction, bounds.Y+12)
					if !view.HasText(a.t("timeline.pullUp")) {
						t.Fatal("hover preview absent")
					}
					// The visible help must stay inside the window at both edges.
					hint, ok := view.Find(a.t("timeline.pullUp"))
					if !ok || hint.X < 0 || hint.Y < 0 || hint.X+hint.W > float32(size[0]) || hint.Y+hint.H > float32(size[1]) {
						t.Fatalf("preview clipped: %+v", hint)
					}
				}
				writeUISnapshot(t, fmt.Sprintf("timeline-%s-%d-hover.png", locale, size[0]), view)
				view.Press(bounds.X+bounds.W/2, bounds.Y+12)
				view.Move(bounds.X+bounds.W/2, bounds.Y-50)
				if !view.HasText(a.t("timeline.releaseToSeek")) {
					t.Fatal("precise strip absent")
				}
				track, _ := view.Find(a.t("timeline.playbackPosition"))
				caption, ok := view.Find(formatPreciseTime(a.scrubTime))
				if !ok || caption.Y+caption.H > track.Y {
					t.Fatal("floating preview overlapped the expanded timeline", caption, track)
				}
				writeUISnapshot(t, fmt.Sprintf("timeline-%s-%d-fine.png", locale, size[0]), view)
				view.CancelPress()
				if err := view.Click(a.t("timeline.chapters")); err != nil {
					t.Fatal(err)
				}
				writeUISnapshot(t, fmt.Sprintf("timeline-%s-%d-chapters.png", locale, size[0]), view)
			})
		}
	}
}

func TestSeekPreviewWorkerReplacesQueuedTargetsAndStops(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	previews := newSeekPreviews(ctx, playbackRenderFixture(t), nil, func() {})
	defer previews.cancel()
	for i := int64(0); i < 30; i++ {
		previews.request([]int64{i * 40000}, true)
	}
	previews.request([]int64{500000}, true)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		previews.collect()
		if img, ready := previews.images[500000]; ready {
			if img.bitmap == nil {
				t.Fatal("latest preview failed")
			}
			previews.request(nil, true)
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("latest pointer target did not complete")
}

func TestHoverPreviewRemainsStableUntilPreciseScrubbing(t *testing.T) {
	if err := loadAssets(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	player := media.NewPlayer(nil)
	defer player.Close()
	a := &application{ctx: ctx, player: player, preferences: defaultPreferences()}
	defer a.stopSeekPreviews()
	state := media.State{URL: playbackRenderFixture(t), Metadata: media.Metadata{Duration: 2, FrameRate: 24}}
	view := timelineTestView(t, a, state, 900, 560)
	bounds, _ := view.Find(a.t("timeline.playbackPosition"))
	view.Move(bounds.X+bounds.W*.3, bounds.Y+12)
	waitFor := func(ready func() bool) {
		t.Helper()
		deadline := time.Now().Add(4 * time.Second)
		for time.Now().Before(deadline) {
			view.Frame()
			if ready() {
				return
			}
			time.Sleep(time.Millisecond)
		}
		t.Fatal("preview did not reach its expected loading phase")
	}
	waitFor(func() bool {
		return a.seekPreviews.images[500000].bitmap != nil && a.seekPreviews.images[1000000].bitmap != nil
	})
	if a.seekPreviews.images[500000].precise || !view.HasText(a.t("timeline.pullUp")) {
		t.Fatal("hover waited for precise decoding before displaying its quick picture")
	}
	view.Move(bounds.X+bounds.W*.55, bounds.Y+12)
	if !view.HasText(a.t("timeline.pullUp")) {
		t.Fatal("moving to a prefetched preview flashed a loading placeholder")
	}
	waitFor(func() bool { return a.seekPreviews.images[1500000].bitmap != nil })
	stable := a.seekPreviews.images[1000000].bitmap
	a.seekPreviews.results <- seekPreviewResult{key: 1000000, image: image.NewRGBA(image.Rect(0, 0, 64, 32))}
	view.Advance(time.Second)
	view.Frame()
	if preview := a.seekPreviews.images[1000000]; preview.precise || preview.bitmap != stable || a.seekPreviews.requestedPrecise {
		t.Fatal("stationary hover silently replaced its preview with a different video frame")
	}
	view.Press(bounds.X+bounds.W*.55, bounds.Y+12)
	view.Move(bounds.X+bounds.W*.55, bounds.Y-50)
	key := previewKey(a.scrubTime, state.Metadata, true)
	waitFor(func() bool { return a.seekPreviews.images[key].precise })
	if !a.seekUI.fine || !a.seekPreviews.requestedPrecise {
		t.Fatal("precise scrubbing did not explicitly request its target frame")
	}
	view.CancelPress()
}

func TestPreviewFadesInAndChangesFramesWithoutLoadingIndicator(t *testing.T) {
	a := &application{preferences: defaultPreferences(), seekPreviews: &seekPreviews{images: map[int64]seekPreviewImage{}}}
	key := int64(500000)
	builds := 0
	view := ui.NewTester(func(c *ui.Context) {
		builds++
		c.SetTheme(darkPlayerTheme)
		ui.Box(c).Fill().Background(ui.Hex("#28333e")).Children(func() { a.previewPicture(c, key) })
	}, 120, 80)
	before, count := bytes.Clone(view.Image().Pix), builds
	view.Advance(200 * time.Millisecond)
	if len(view.Texts()) != 0 || builds != count || !bytes.Equal(before, view.Image().Pix) {
		t.Fatal("waiting preview displayed a loading indicator or animated an empty image")
	}
	solid := func(pixel color.RGBA) *ui.Bitmap {
		img := image.NewRGBA(image.Rect(0, 0, 64, 36))
		for y := 0; y < 36; y++ {
			for x := 0; x < 64; x++ {
				img.SetRGBA(x, y, pixel)
			}
		}
		return ui.NewBitmap(img)
	}
	a.seekPreviews.images[500000] = seekPreviewImage{bitmap: solid(color.RGBA{R: 255, G: 20, B: 10, A: 255})}
	view.Frame()
	initial := view.Image().RGBAAt(60, 40)
	view.Advance(50 * time.Millisecond)
	middle := view.Image().RGBAAt(60, 40)
	if initial.R >= middle.R || middle.R >= 255 {
		t.Fatal("preview did not gradually appear", initial, middle)
	}
	writeUISnapshot(t, "preview-fade-in.png", view)
	view.Advance(100 * time.Millisecond)
	if pixel := view.Image().RGBAAt(60, 40); pixel.R != 255 {
		t.Fatal("preview fade did not finish", pixel)
	}
	a.seekPreviews.images[500000] = seekPreviewImage{bitmap: solid(color.RGBA{R: 10, G: 20, B: 255, A: 255}), precise: true}
	view.Frame()
	view.Advance(50 * time.Millisecond)
	refined := view.Image().RGBAAt(60, 40)
	if refined.R <= 10 || refined.B <= 10 || refined.B >= 255 {
		t.Fatal("precise picture did not blend with its initial preview", refined)
	}
	writeUISnapshot(t, "preview-refine-blend.png", view)
	view.Advance(200 * time.Millisecond)
	if pixel := view.Image().RGBAAt(60, 40); pixel.B != 255 {
		t.Fatal("precise preview did not finish appearing", pixel)
	}
	key = 1000000
	view.Frame()
	view.Advance(200 * time.Millisecond)
	if pixel := view.Image().RGBAAt(60, 40); pixel != (color.RGBA{R: 40, G: 51, B: 62, A: 255}) {
		t.Fatal("an unloaded timestamp displayed a nearby frame that would later change", pixel)
	}
}
