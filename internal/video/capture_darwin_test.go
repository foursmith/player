package video

import (
	"bytes"
	"context"
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
func patternFrame(t *testing.T, width, height int) (*media.Frame, *image.RGBA) {
	t.Helper()
	dir := t.TempDir()
	input := filepath.Join(dir, "pattern.png")
	f, err := os.Create(input)
	if err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	colors := [4]color.RGBA{{255, 0, 0, 255}, {0, 255, 0, 255}, {0, 0, 255, 255}, {255, 255, 0, 255}}
	for y := range height {
		for x := range width {
			index := x/(width/2) + 2*(y/(height/2))
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
	decoder, err := media.Open(context.Background(), media.Source{URL: clip}, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(decoder.Close)
	frame, err := decoder.Next()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(frame.Close)
	rgba, err := frame.RGBA(width, height)
	if err != nil {
		t.Fatal(err)
	}
	return frame, rgba
}

func TestProjectionReadbackMatchesCameraCoordinates(t *testing.T) {
	frame, rgba := patternFrame(t, 320, 160)
	surface, err := New(0)
	if err != nil {
		t.Fatal(err)
	}
	defer surface.Close()
	for mode := projection.Mode(0); mode < projection.Flat; mode++ {
		p := Parameters{Mode: mode, View: projection.View{Yaw: 23, Pitch: -11, Forward: 14, Zoom: 1}, SplitCount: 1}
		out, err := surface.Capture(frame, p, 41, 31)
		if err != nil {
			t.Fatal(err)
		}
		for _, y := range []int{5, 15, 25} {
			for _, x := range []int{8, 20, 32} {
				origin, ray := projection.Ray((float64(x)+.5)/41, (float64(y)+.5)/31, 41.0/31, 80, p.View)
				u, v, visible := projection.UV(mode, origin, ray)
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

func TestFlatReadbackFitsAndIgnoresVRCameraAndSplit(t *testing.T) {
	surface, err := New(0)
	if err != nil {
		t.Fatal(err)
	}
	defer surface.Close()
	for _, size := range [][2]int{{320, 160}, {160, 320}} {
		frame, _ := patternFrame(t, size[0], size[1])
		for _, output := range [][2]int{{80, 80}, {160, 40}, {40, 160}} {
			for _, view := range []FlatView{{}, {Zoom: 2, X: .2, Y: -.1}} {
				p := Parameters{Mode: projection.Flat, FlatView: view, View: projection.View{Yaw: 123, Pitch: 80, Forward: 150, Zoom: 3}, SplitCount: 3, Portrait: true}
				got, err := surface.Capture(frame, p, output[0], output[1])
				if err != nil {
					t.Fatal(err)
				}
				cpu, err := (&rgbaSurface{}).Capture(frame, p, output[0], output[1])
				if err != nil {
					t.Fatal(err)
				}
				for y := 2; y < output[1]; y += 7 {
					for x := 2; x < output[0]; x += 7 {
						u, v, visible := view.UV((float64(x)+.5)/float64(output[0]), (float64(y)+.5)/float64(output[1]), float64(output[0])/float64(output[1]), float64(size[0])/float64(size[1]))
						if visible && (math.Abs(u-.5) < .03 || math.Abs(v-.5) < .03) {
							continue
						}
						a, b := got.RGBAAt(x, y), cpu.RGBAAt(x, y)
						for _, channel := range [][2]uint8{{a.R, b.R}, {a.G, b.G}, {a.B, b.B}, {a.A, b.A}} {
							// Geometry is compared across native YUV and software RGBA
							// conversion, whose unspecified matrix defaults can differ.
							if math.Abs(float64(channel[0])-float64(channel[1])) > 45 {
								t.Fatalf("source %v, output %v, view %+v, pixel %d,%d: Metal %v CPU %v", size, output, view, x, y, a, b)
							}
						}
					}
				}
				p.View, p.SplitCount, p.Portrait = projection.DefaultView(), 1, false
				single, err := surface.Capture(frame, p, output[0], output[1])
				if err != nil || !bytes.Equal(single.Pix, got.Pix) {
					t.Fatal("VR settings affected flat rendering", err)
				}
			}
		}
	}
}
