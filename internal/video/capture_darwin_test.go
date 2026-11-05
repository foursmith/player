package video

import (
	"github.com/foursmith/player/internal/media"
	"github.com/foursmith/player/internal/projection"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// This checks projection math and native texture readback, without a window or
// UI renderer. Spatial samples catch flipped axes and the wrong stereo eye.
func TestProjectionReadbackMatchesCameraCoordinates(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "pattern.png")
	f, err := os.Create(input)
	if err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, 320, 160))
	colors := [4]color.RGBA{{255, 0, 0, 255}, {0, 255, 0, 255}, {0, 0, 255, 255}, {255, 255, 0, 255}}
	for y := range 160 {
		for x := range 320 {
			index := x/160 + 2*(y/80)
			img.SetRGBA(x, y, colors[index])
		}
	}
	if err = png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	f.Close()
	clip := filepath.Join(dir, "pattern.mp4")
	if out, err := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-loop", "1", "-i", input, "-t", "0.1", "-pix_fmt", "yuv420p", "-c:v", "libx264", clip).CombinedOutput(); err != nil {
		t.Fatalf("fixture: %v: %s", err, out)
	}
	decoder, err := media.Open(media.Source{URL: clip}, false)
	if err != nil {
		t.Fatal(err)
	}
	defer decoder.Close()
	frame, err := decoder.Next()
	if err != nil {
		t.Fatal(err)
	}
	defer frame.Close()
	rgba, err := frame.RGBA(320, 160)
	if err != nil {
		t.Fatal(err)
	}
	surface, err := New(0)
	if err != nil {
		t.Fatal(err)
	}
	defer surface.Close()
	for mode := projection.Mode(0); mode <= projection.Flat; mode++ {
		p := Parameters{Mode: mode, View: projection.View{Yaw: 23, Pitch: -11, Forward: 14, Zoom: 1}, SplitCount: 1}
		out, err := surface.Capture(frame, p, 41, 31)
		if err != nil {
			t.Fatal(err)
		}
		for _, y := range []int{5, 15, 25} {
			for _, x := range []int{8, 20, 32} {
				origin, ray := projection.Ray((float64(x)+.5)/41, (float64(y)+.5)/31, 41.0/31, 80, p.View)
				u, v, visible := projection.UV(mode, origin, ray, 2)
				expected := color.RGBA{20, 18, 15, 255}
				if visible {
					if math.Abs(u-.5) < .03 || math.Abs(v-.5) < .03 {
						continue
					}
					expected = rgba.RGBAAt(int(projection.Clamp(u*320, 0, 319)), int(projection.Clamp(v*160, 0, 159)))
				}
				got := out.RGBAAt(x, y)
				for _, channel := range [][2]uint8{{expected.R, got.R}, {expected.G, got.G}, {expected.B, got.B}} {
					if math.Abs(float64(channel[0])-float64(channel[1])) > 32 {
						t.Fatalf("mode %d, %d,%d source %.2f,%.2f: got %v expected %v", mode, x, y, u, v, got, expected)
					}
				}
			}
		}
	}
}
