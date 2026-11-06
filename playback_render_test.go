package main

import (
	"context"
	"image"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/foursmith/player/internal/media"
	"github.com/foursmith/player/internal/projection"
	"github.com/foursmith/player/internal/subtitle"
	"github.com/foursmith/player/internal/tracking"
	"github.com/foursmith/player/internal/video"
)

type countedVideoSurface struct {
	presents, duplicates int
	frame                uint64
	params               video.Parameters
	scale                float64
}

func (s *countedVideoSurface) Present(frame *media.Frame, p video.Parameters, _, _, scale float64) error {
	if s.presents > 0 && s.frame == frame.ID() && s.params == p && s.scale == scale {
		s.duplicates++
	}
	s.presents++
	s.frame, s.params, s.scale = frame.ID(), p, scale
	return nil
}
func (*countedVideoSurface) Capture(*media.Frame, video.Parameters, int, int) (*image.RGBA, error) {
	return nil, nil
}
func (*countedVideoSurface) Close() {}

func playbackRenderFixture(t *testing.T) string {
	t.Helper()
	encoder, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("FFmpeg fixture encoder unavailable")
	}
	file := filepath.Join(t.TempDir(), "render.mp4")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	args := []string{"-v", "error", "-f", "lavfi", "-i", "color=c=red:s=64x32:r=24:d=2", "-c:v", "libx264", "-threads", "1", "-g", "24", file}
	if output, err := exec.CommandContext(ctx, encoder, args...).CombinedOutput(); err != nil {
		t.Fatalf("fixture: %v: %s", err, output)
	}
	return file
}

func waitRenderState(t *testing.T, player *media.Player, ready func(media.State) bool) media.State {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		state := player.Snapshot()
		if state.Error != "" {
			t.Fatal(state.Error)
		}
		if ready(state) {
			return state
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("player did not reach the expected state: %+v", player.Snapshot())
	return media.State{}
}

func TestVideoPaintReusesViewAndStopsWhilePaused(t *testing.T) {
	if err := loadAssets(); err != nil {
		t.Fatal(err)
	}
	file := playbackRenderFixture(t)
	for _, pip := range []bool{false, true} {
		name := "main"
		if pip {
			name = "pip"
		}
		t.Run(name, func(t *testing.T) {
			var services atomic.Pointer[ui.Services]
			player := media.NewPlayer(func() {
				if window := services.Load(); window != nil {
					window.Invalidate()
				}
			})
			defer player.Close()
			player.Open(media.Source{URL: file})
			waitRenderState(t, player, func(s media.State) bool { return s.Playing && !s.Loading && s.FrameID != 0 })
			surface := &countedVideoSurface{}
			a := &application{player: player, surface: surface, pipSurface: surface, mode: projection.Flat, preferences: defaultPreferences()}
			builds, staticPaints := 0, 0
			view := ui.NewTester(func(c *ui.Context) {
				builds++
				window := c.Services()
				services.Store(&window)
				a.renderStage(c, pip)
				ui.Box(c).Size(8, 8).Draw(func(p *ui.Painter, r ui.Rect) { staticPaints++; p.Fill(r, ui.RGB(1, 0, 0), 0) })
			}, 64, 48)
			before, staticBefore := builds, staticPaints
			view.Advance(50 * time.Millisecond)
			if builds != before || staticPaints != staticBefore {
				t.Fatalf("video repaint rebuilt static UI: builds=%d paints=%d", builds-before, staticPaints-staticBefore)
			}
			if surface.duplicates != 0 {
				t.Fatal("display refreshes repeatedly presented the same video frame")
			}
			a.flatView.Zoom = 2
			view.Advance(20 * time.Millisecond)
			if builds != before || surface.params.FlatView.Zoom != 2 {
				t.Fatal("camera changes did not reach paint-only frames")
			}
			view.SetScale(2)
			if surface.scale != 2 {
				t.Fatal("presentation did not follow the painter's display scale")
			}
			player.Pause()
			waitRenderState(t, player, func(s media.State) bool { return !s.Playing })
			view.Advance(50 * time.Millisecond)
			if view.FramePending() {
				t.Fatal("paused video keeps requesting display frames")
			}
			before = builds
			presented := surface.presents
			view.Advance(time.Second)
			if builds != before || surface.presents != presented {
				t.Fatal("idle video kept rebuilding or presenting")
			}
			player.Seek(.5)
			waitRenderState(t, player, func(s media.State) bool { return !s.Loading && s.Position >= .5 })
			view.Advance(50 * time.Millisecond)
			if !view.FramePending() || surface.presents <= presented {
				t.Fatal("seek did not restart video painting after an idle pause")
			}
		})
	}
}

func TestABLoopWorksWithoutControlUpdatesAndAtEndOfFile(t *testing.T) {
	player := media.NewPlayer(nil)
	defer player.Close()
	player.Open(media.Source{URL: playbackRenderFixture(t)})
	waitRenderState(t, player, func(s media.State) bool { return !s.Loading && s.FrameID != 0 })
	player.Seek(.6)
	before := waitRenderState(t, player, func(s media.State) bool { return !s.Loading && s.Position >= .6 })
	start, end := .1, .4
	a := &application{player: player, surface: &countedVideoSurface{}, mode: projection.Flat, loopA: &start, loopB: &end}
	ui.NewTester(func(c *ui.Context) { a.renderStage(c, false) }, 64, 48)
	waitRenderState(t, player, func(s media.State) bool { return !s.Loading && s.Generation > before.Generation })
	player.Pause()
	paused := waitRenderState(t, player, func(s media.State) bool { return !s.Playing })
	if paused.Generation != before.Generation+1 || paused.Position < start || paused.Position >= end {
		t.Fatalf("paint frames did not coalesce the loop seek: %+v", paused)
	}
	end = paused.Metadata.Duration
	player.Seek(end - .1)
	ended := waitRenderState(t, player, func(s media.State) bool { return s.Ended })
	if !a.loopPlayback(ended) || !a.loopPlayback(ended) {
		t.Fatal("end-of-file loop did not take priority over playlist advance")
	}
	ready := waitRenderState(t, player, func(s media.State) bool { return !s.Loading && s.Generation > ended.Generation })
	if ready.Generation != ended.Generation+1 || ready.Position < start || ready.Position >= .4 {
		t.Fatalf("end-of-file loop did not return to A once: %+v", ready)
	}
}

func TestPausedPipOnlyRebuildsForItsOwnChanges(t *testing.T) {
	a := &application{mode: projection.Flat, preferences: defaultPreferences()}
	a.cues = subtitle.Parse("1\n00:00:00,000 --> 00:00:02,000\ncaption", "test.srt")
	state := media.State{Position: .5}
	a.presentation[1].params = a.params(true)
	a.presentation[1].subtitle = a.subtitleAt(state.Position)
	state.Position = .6
	if a.pipNeedsView(state) {
		t.Fatal("unchanged PiP follows unrelated main-window refreshes")
	}
	a.flatView.Zoom = 2
	if !a.pipNeedsView(state) {
		t.Fatal("paused PiP did not notice a camera change")
	}
	a.presentation[1].params = a.params(true)
	a.preferences.SubtitlesEnabled = false
	if !a.pipNeedsView(state) {
		t.Fatal("paused PiP did not notice a subtitle change")
	}
	state.Playing = true
	if a.pipNeedsView(state) {
		t.Fatal("playing PiP rebuilds instead of sampling paint inputs")
	}
}

func TestTrackingMovesOnPaintFramesAndSubtitlesRebuildAtTheirBoundary(t *testing.T) {
	if err := loadAssets(); err != nil {
		t.Fatal(err)
	}
	player := media.NewPlayer(nil)
	defer player.Close()
	player.Open(media.Source{URL: playbackRenderFixture(t)})
	waitRenderState(t, player, func(s media.State) bool { return !s.Loading && s.FrameID != 0 })
	worker := &inferenceWorker{jobs: make(chan inferenceJob, 1)}
	defer func() {
		select {
		case job := <-worker.jobs:
			job.frame.Close()
		default:
		}
	}()
	a := &application{player: player, surface: &countedVideoSurface{}, preferences: defaultPreferences(), mode: projection.Mono360, view: projection.DefaultView(), inference: worker}
	a.tracker.State.Target = &tracking.Target{Panorama: true, Yaw: 50, Seen: trackingNow()}
	a.cues = subtitle.Parse("1\n00:00:00,000 --> 00:00:00,250\nfirst\n\n2\n00:00:00,250 --> 00:00:02,000\nsecond", "test.srt")
	builds := 0
	view := ui.NewTester(func(c *ui.Context) { builds++; a.renderStage(c, false) }, 96, 64)
	before, yaw := builds, a.view.Yaw
	view.Advance(100 * time.Millisecond)
	if builds != before || a.view.Yaw == yaw {
		t.Fatal("VR motion required rebuilding the view")
	}
	waitRenderState(t, player, func(s media.State) bool { return s.Position > .3 })
	view.Advance(50 * time.Millisecond)
	if !view.HasText("second") || builds <= before {
		t.Fatal("subtitle boundary did not rebuild its visible text")
	}
	a.preferences.SubtitlesEnabled = false
	view.Advance(50 * time.Millisecond)
	if view.HasText("second") {
		t.Fatal("paint-only video retained subtitles after they were disabled")
	}
}
