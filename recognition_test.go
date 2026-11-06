package main

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"log"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/foursmith/player/internal/media"
	"github.com/foursmith/player/internal/projection"
	"github.com/foursmith/player/library"
)

func TestRecognitionTestBypassesManualAndAutomaticHistoryEveryOpen(t *testing.T) {
	t.Setenv("FSVR_PROJECTION_MODEL", "off")
	a := modeTestApplication(t)
	a.recognitionTest = true
	entry := a.entries[0]
	for _, manual := range []bool{false, true} {
		a.stored.History[library.Identity(entry)] = videoHistory{Projection: projection.Mono360, ProjectionManual: manual, AutoProjection: "sbs_180_fe", Position: 12.5}
		for range 2 {
			a.mode = projection.SBSFisheye
			a.modeManual, a.detectMode = true, false
			start := a.prepareVideoMode(entry)
			if start != 12.5 || a.mode != projection.Flat || a.modeManual || !a.detectMode {
				t.Fatalf("test mode reused saved types: start=%v mode=%v manual=%v detect=%v", start, a.mode, a.modeManual, a.detectMode)
			}
		}
	}
	a.recognitionTest = false
	a.prepareVideoMode(entry)
	if a.mode != projection.Mono360 || !a.modeManual || a.detectMode {
		t.Fatal("normal mode stopped honoring a saved manual type")
	}
}

func TestRecognitionTestPreservesHistoryTypesWhileSavingPosition(t *testing.T) {
	a := modeTestApplication(t)
	a.recognitionTest = true
	entry := a.entries[0]
	key := library.Identity(entry)
	want := videoHistory{Projection: projection.Mono360, ProjectionManual: true, AutoProjection: "sbs_180_eqr", Position: 12}
	a.stored.History[key] = want
	a.mode, a.modeManual = projection.SBSFisheye, false
	a.recordPlaybackHistory(entry, media.State{URL: entry.Path, Position: 67.5})
	got := a.stored.History[key]
	if got.Projection != want.Projection || got.ProjectionManual != want.ProjectionManual || got.AutoProjection != want.AutoProjection || got.Position != 67.5 {
		t.Fatalf("test results overwrote a stored video type: %+v", got)
	}
	delete(a.stored.History, key)
	a.recordPlaybackHistory(entry, media.State{URL: entry.Path, Position: 68})
	if got := a.stored.History[key]; got.AutoProjection != "" || got.ProjectionManual || got.Position != 68 {
		t.Fatalf("a new test result was cached: %+v", got)
	}
	a.recognitionTest = false
	a.detectMode = false
	a.recordPlaybackHistory(entry, media.State{URL: entry.Path, Position: 69})
	if got := a.stored.History[key]; got.AutoProjection != "sbs_180_fe" || got.ProjectionManual || got.Position != 69 {
		t.Fatalf("normal history stopped saving types: %+v", got)
	}
}

func TestRecognitionTestSamplesTrustedMetadataInsteadOfShortCircuiting(t *testing.T) {
	t.Setenv("FSVR_PROJECTION_MODEL", "off")
	a := modeTestApplication(t)
	a.recognitionTest = true
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	a.ctx = ctx
	a.resolveVideoMode(media.State{URL: "current.mp4", Metadata: media.Metadata{Width: 320, Height: 160, Spherical: "equirectangular", Stereo: "mono"}})
	if a.modeDetection == nil || !a.detectMode {
		t.Fatal("recognition test skipped frames for trusted metadata")
	}
	a.setMode(projection.Flat)
	if a.detectMode || !a.modeManual || a.modeDetection != nil {
		t.Fatal("a manual choice made during testing did not cancel detection")
	}
}

func recognitionFixture(t *testing.T) string {
	t.Helper()
	encoder, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("FFmpeg fixture encoder unavailable")
	}
	dir := t.TempDir()
	img := image.NewRGBA(image.Rect(0, 0, 320, 160))
	for y := range 160 {
		for x := range 320 {
			u, v := (float64(x%160)+.5)/160-.5, (float64(y)+.5)/160-.5
			value := uint8(0)
			if math.Hypot(u, v) < .49 {
				value = uint8(50 + ((x%160)*13+y*17)%170)
			}
			img.SetRGBA(x, y, color.RGBA{value, value, value, 255})
		}
	}
	file, err := os.Create(filepath.Join(dir, "source.png"))
	if err != nil {
		t.Fatal(err)
	}
	err = png.Encode(file, img)
	file.Close()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "clip.mp4")
	// Include exact-seek frame rounding: the latest first-segment request
	// can decode the frame just after 2.1s. Keep that entire segment black.
	filter := "color=c=black:s=320x160:r=10:d=2.3[black];[0:v]trim=duration=3.7,setpts=PTS-STARTPTS[fish];[black][fish]concat=n=2:v=1:a=0"
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, encoder, "-hide_banner", "-loglevel", "error", "-loop", "1", "-framerate", "10", "-i", file.Name(), "-filter_complex", filter, "-t", "6", "-c:v", "libx264", "-g", "10", "-pix_fmt", "yuv420p", path).CombinedOutput(); err != nil {
		t.Fatalf("fixture: %v %s", err, out)
	}
	return path
}

func TestRecognitionTraceIncludesSkippedFramesModelScoresAndDecision(t *testing.T) {
	path := recognitionFixture(t)
	var output bytes.Buffer
	trace := &recognitionTrace{id: 7, started: time.Now(), logger: log.New(&output, "", 0)}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	entry := library.Entry{Name: "clip.mp4", Path: path}
	metadata := media.Metadata{Width: 320, Height: 160, Duration: 6, FrameRate: 10}
	decision := analyzeVideoMode(ctx, entry, metadata, "", trace)
	if decision.Mode != projection.SBSFisheye || decision.Reason != "fisheye_boundary" {
		t.Fatalf("unexpected test result: %+v\n%s", decision, output.String())
	}
	for _, field := range []string{"[recognition-test #7]", "metadata size=320x160", "model source=\"embedded:", "sha256=", "sample stage=open", "strategy=exact", "rejection=\"black_frame\"", "decoder=h264", "model attempt=", "consensus frames=3", "model aggregate samples=3", "final mode=sbs_180_fe reason=fisheye_boundary"} {
		if !strings.Contains(output.String(), field) {
			t.Fatalf("missing trace field %q\n%s", field, output.String())
		}
	}
	for _, id := range projection.IDs {
		if !strings.Contains(output.String(), id+"=") {
			t.Fatal("trace omitted model class:", id)
		}
	}
	output.Reset()
	metadata.Spherical, metadata.Stereo = "equirectangular", "mono"
	decision = analyzeVideoMode(ctx, entry, metadata, "", trace)
	if decision.Mode != projection.Mono360 || decision.Reason != "spatial_metadata" || !strings.Contains(output.String(), "model aggregate samples=3") {
		t.Fatalf("testing changed metadata priority or skipped inference: %+v\n%s", decision, output.String())
	}
}
