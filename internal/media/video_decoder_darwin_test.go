package media

import (
	"context"
	"io"
	"math"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestHighResolutionH264PlaybackAndSeek(t *testing.T) {
	encoder, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("FFmpeg fixture encoder unavailable")
	}
	path := filepath.Join(t.TempDir(), "8k-avc.mp4")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	args := []string{"-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "color=c=red:s=7680x3840:r=2:d=1", "-c:v", "libx264", "-threads", "1", "-preset", "ultrafast", "-tune", "zerolatency", "-pix_fmt", "yuv420p", "-y", path}
	if out, err := exec.CommandContext(ctx, encoder, args...).CombinedOutput(); err != nil {
		t.Fatalf("8K AVC fixture: %v: %s", err, out)
	}
	// Native session rejection must fall back to software, not repeatedly try
	// the same VideoToolbox setup through libavcodec. No audio device is opened.
	d, err := Open(ctx, Source{URL: path}, true)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	for _, at := range []float64{0, .5, 0} {
		if err := d.Seek(at); err != nil {
			t.Fatal(err)
		}
		frame, err := d.Next()
		if err != nil {
			t.Fatal(err)
		}
		if math.Abs(frame.PTS()-at) > .001 {
			t.Fatal("incorrect fallback seek:", frame.PTS())
		}
		img, err := frame.RGBA(80, 40)
		frame.Close()
		if err != nil || img.RGBAAt(40, 20).R < 200 {
			t.Fatalf("8K AVC thumbnail failed: %v", err)
		}
	}
}

func TestAsyncHEVCReorderingBitDepthAndFrameLifetime(t *testing.T) {
	encoder, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("FFmpeg fixture encoder unavailable")
	}
	for _, pixelFormat := range []string{"yuv420p", "yuv420p10le"} {
		t.Run(pixelFormat, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "hevc.mp4")
			args := []string{"-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "color=c=red:s=320x160:r=60:d=1", "-c:v", "libx265", "-x265-params", "log-level=error:pools=none:frame-threads=1:bframes=8", "-pix_fmt", pixelFormat, "-tag:v", "hvc1", "-y", path}
			if out, err := exec.Command(encoder, args...).CombinedOutput(); err != nil {
				t.Fatalf("HEVC fixture: %v: %s", err, out)
			}
			d, err := Open(context.Background(), Source{URL: path}, true)
			if err != nil {
				t.Fatal(err)
			}
			defer d.Close()
			if name := d.Metadata().Decoder; name != "videotoolbox-async" {
				t.Skipf("asynchronous HEVC hardware decoder unavailable: %s", name)
			}
			var held *Frame
			for i := 0; i < 60; i++ {
				frame, err := d.Next()
				if err != nil {
					t.Fatalf("picture %d: %v", i, err)
				}
				if math.Abs(frame.PTS()-float64(i)/60) > .0001 {
					t.Errorf("picture %d reordered incorrectly: %v", i, frame.PTS())
				}
				if frame.Native() == nil {
					t.Error("hardware picture has no native texture")
				}
				if i == 0 {
					held = frame.Clone()
					defer held.Close()
				}
				frame.Close()
			}
			if frame, err := d.Next(); err != io.EOF {
				frame.Close()
				t.Fatalf("expected EOF, got %v", err)
			}
			if err := d.Seek(.45); err != nil {
				t.Fatal(err)
			}
			frame, err := d.Next()
			if err != nil {
				t.Fatal(err)
			}
			if frame.PTS() < .45 || frame.PTS() > .47 {
				t.Errorf("inaccurate HEVC seek: %v", frame.PTS())
			}
			frame.Close()
			d.Close()
			// Renderer/detector references outlive the decoder and seek session.
			img, err := held.RGBA(80, 40)
			if err != nil {
				t.Fatal(err)
			}
			pixel := img.RGBAAt(40, 20)
			if pixel.R < 200 || pixel.G > 35 || pixel.B > 35 {
				t.Fatalf("wrong retained %s color: %+v", pixelFormat, pixel)
			}
		})
	}
}

func TestAsyncHEVCOpenGOPSeekPreservesHardwareAndPresentationOrder(t *testing.T) {
	encoder, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("FFmpeg fixture encoder unavailable")
	}
	for _, pixelFormat := range []string{"yuv420p", "yuv420p10le"} {
		t.Run(pixelFormat, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "open-gop.mp4")
			args := []string{"-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=s=640x320:r=30:d=6", "-c:v", "libx265", "-x265-params", "log-level=error:pools=none:frame-threads=1:keyint=30:min-keyint=30:scenecut=0:open-gop=1:bframes=4:b-adapt=0", "-pix_fmt", pixelFormat, "-tag:v", "hvc1", "-y", path}
			if out, err := exec.Command(encoder, args...).CombinedOutput(); err != nil {
				t.Fatalf("open-GOP fixture: %v: %s", err, out)
			}
			d, err := Open(context.Background(), Source{URL: path}, true)
			if err != nil {
				t.Fatal(err)
			}
			defer d.Close()
			if name := d.Metadata().Decoder; name != "videotoolbox-async" {
				t.Skipf("native HEVC decoder unavailable: %s", name)
			}
			for _, at := range []float64{0, 1.37, 3.43, .5, 4.73, 1.04, 0} {
				if err := d.Seek(at); err != nil {
					t.Fatal(err)
				}
				previous := -1.0
				for frameIndex := range 24 {
					frame, err := d.Next()
					if err != nil {
						t.Fatalf("seek %.3fs picture %d: %v", at, frameIndex, err)
					}
					pts := frame.PTS()
					if pts+.0001 < at || frameIndex == 0 && pts > at+1.0/30+.001 || pts <= previous || frame.Native() == nil {
						t.Errorf("seek %.3fs picture %d: pts=%v previous=%v native=%v", at, frameIndex, pts, previous, frame.Native() != nil)
					}
					previous = pts
					frame.Close()
				}
			}
		})
	}
}

type nativeWaitContext struct {
	context.Context
	waiting chan struct{}
}

func (c nativeWaitContext) Done() <-chan struct{} {
	select {
	case c.waiting <- struct{}{}:
	default:
	}
	return c.Context.Done()
}

func TestNativeDecoderCancellationWakesPendingPicture(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	waiting := make(chan struct{}, 1)
	p := &nativeVideoPipeline{ctx: nativeWaitContext{ctx, waiting}, changed: make(chan struct{}, 1)}
	// A full submission window must wait for its first asynchronous callback.
	// Cancellation must interrupt that wait even if no callback arrives.
	for range 32 {
		p.pictures = append(p.pictures, &videoPicture{})
	}
	done := make(chan int32, 1)
	go func() { done <- p.receive(&avFrame{}) }()
	select {
	case <-waiting:
	case <-time.After(time.Second):
		t.Fatal("native decoder did not reach its pending callback wait")
	}
	cancel()
	select {
	case code := <-done:
		if code != avExit {
			t.Fatalf("cancellation returned %d", code)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled native decode waited for an output callback")
	}
}
