package media

import (
	"context"
	"errors"
	"image"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestThumbnailUsesSmallIndependentVideoFrame(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	preview, err := Thumbnail(ctx, fixture(t, false))
	if err != nil {
		t.Fatal(err)
	}
	img := preview.Image.(*image.RGBA)
	if preview.Duration < .9 || preview.Duration > 1.1 {
		t.Fatal("preview did not include the video duration", preview.Duration)
	}
	if img.Bounds().Dx() != 240 || img.Bounds().Dy() != 120 || img.RGBAAt(100, 60).R < 150 {
		t.Fatal("preview has the wrong dimensions or pixels", img.Bounds())
	}
	canceled, stop := context.WithCancel(context.Background())
	stop()
	if _, err := Thumbnail(canceled, "/missing.mp4"); !errors.Is(err, context.Canceled) {
		t.Fatal("preview ignored cancellation", err)
	}
}

func TestThumbnailSkipsBlackIntroWithSparseKeyframes(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("FFmpeg fixture encoder unavailable")
	}
	source := filepath.Join(t.TempDir(), "intro.mp4")
	filter := "color=c=black:s=80x40:r=5:d=3[black];color=c=red:s=80x40:r=5:d=3[content];[black][content]concat=n=2:v=1:a=0"
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", filter, "-c:v", "libx264", "-g", "1000", "-sc_threshold", "0", "-pix_fmt", "yuv420p", source).CombinedOutput(); err != nil {
		t.Fatalf("fixture: %v: %s", err, out)
	}
	preview, err := Thumbnail(ctx, source)
	if err != nil || preview.Image.(*image.RGBA).RGBAAt(50, 30).R < 150 || preview.Duration < 5.9 {
		t.Fatal("sparse keyframes left the preview on a black intro", err)
	}
}
