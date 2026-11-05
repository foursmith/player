package media

import (
	"io"
	"math"
	"os/exec"
	"path/filepath"
	"testing"
)

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
			d, err := Open(Source{URL: path}, true)
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
