package media

import (
	"context"
	"errors"
	"image"
	"math"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestSampleTimesCoverClipAndVaryRandomly(t *testing.T) {
	a := sampleTimes(100, func() float64 { return .1 })
	b := sampleTimes(100, func() float64 { return .9 })
	for i, at := range a {
		if at <= 0 || at >= 100 || at == b[i] {
			t.Fatal("sampling is not bounded and randomized", a, b)
		}
		if i > 0 && i%3 != 0 && at-a[i-1] < 20 {
			t.Fatal("frames clustered in one part of the clip", a)
		}
	}
}

func TestSampleFramesCanSkipBlackIntro(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("FFmpeg toolchain unavailable")
	}
	source := filepath.Join(t.TempDir(), "intro.mp4")
	filter := "color=c=black:s=320x160:r=10:d=3.5[black];testsrc2=s=320x160:r=10:d=5.5[content];[black][content]concat=n=2:v=1:a=0"
	if out, err := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", filter, "-c:v", "libx264", "-pix_fmt", "yuv420p", source).CombinedOutput(); err != nil {
		t.Fatalf("fixture: %v: %s", err, out)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	black, usable := 0, 0
	err := SampleFrames(ctx, source, func(img *image.RGBA) bool {
		var total int
		for i := 0; i < len(img.Pix); i += 4 {
			total += int(img.Pix[i]) + int(img.Pix[i+1]) + int(img.Pix[i+2])
		}
		if total < img.Rect.Dx()*img.Rect.Dy()*10 {
			black++
		} else {
			usable++
		}
		return usable == 3
	}, nil)
	if err != nil || black == 0 || usable != 3 {
		t.Fatalf("black=%d usable=%d err=%v", black, usable, err)
	}
}

func TestSampleFramesCancelsBlockedSource(t *testing.T) {
	entered := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(entered); <-r.Context().Done() }))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- SampleFrames(ctx, server.URL+"/video.mp4", func(*image.RGBA) bool { return false }, nil)
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("sampling did not open the source")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("sampling ignored cancellation")
	}
}

func TestSampleLongGOPReplacesRepeatedBlackKeyframes(t *testing.T) {
	source := filepath.Join(t.TempDir(), "long-gop.mp4")
	filter := "color=c=black:s=80x40:r=5:d=4[black];color=c=red:s=80x40:r=5:d=36[content];[black][content]concat=n=2:v=1:a=0"
	if out, err := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", filter, "-c:v", "libx264", "-g", "1000", "-keyint_min", "1000", "-sc_threshold", "0", "-pix_fmt", "yuv420p", source).CombinedOutput(); err != nil {
		t.Fatalf("fixture: %v: %s", err, out)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	black, usable := 0, 0
	var traces []SampleTrace
	err := SampleFrames(ctx, source, func(img *image.RGBA) bool {
		if img.RGBAAt(img.Rect.Dx()/2, img.Rect.Dy()/2).R < 30 {
			black++
		} else {
			usable++
		}
		return usable == 3
	}, func(info SampleTrace) { traces = append(traces, info) })
	if err != nil || black == 0 || usable != 3 {
		t.Fatalf("repeated keyframes blocked sampling: black=%d usable=%d err=%v", black, usable, err)
	}
	if len(traces) < 4 || traces[0].Stage != "open" || traces[0].Decoder != "h264" {
		t.Fatalf("missing sampling setup trace: %+v", traces)
	}
	replaced := false
	for _, info := range traces[1:] {
		if info.Stage != "frame" || info.Err != nil || info.Attempt < 1 || math.IsNaN(info.PTS) || info.Elapsed <= 0 {
			t.Fatalf("invalid frame trace: %+v", info)
		}
		if info.ReplacedDuplicate {
			replaced = true
			if info.Strategy != "exact_duplicate_replacement" || info.DuplicatePTS != 0 || info.PTS+.0001 < info.Requested || info.PTS > info.Requested+.21 {
				t.Fatalf("duplicate replacement lost its requested/actual timestamps: %+v", info)
			}
		}
	}
	if !replaced {
		t.Fatal("duplicate-keyframe replacement was missing from diagnostics")
	}
	d, err := OpenContext(ctx, Source{URL: source}, false)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := d.Seek(20.1); err != nil {
		t.Fatal(err)
	}
	frame, err := d.Next()
	if err != nil {
		t.Fatal(err)
	}
	defer frame.Close()
	if frame.PTS() < 20.1 || frame.PTS() > 20.4 {
		t.Fatal("normal seeking lost its exact timestamp", frame.PTS())
	}
}

func TestSamplingTraceIncludesOpenFailure(t *testing.T) {
	var traces []SampleTrace
	err := SampleFrames(context.Background(), filepath.Join(t.TempDir(), "missing.mp4"), func(*image.RGBA) bool {
		t.Fatal("an unavailable source produced a frame")
		return false
	}, func(info SampleTrace) { traces = append(traces, info) })
	if err == nil || len(traces) != 1 || traces[0].Stage != "open" || traces[0].Err == nil {
		t.Fatalf("open failure was not traced: %v %+v", err, traces)
	}
}
