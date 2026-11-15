package video

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/foursmith/player/internal/media"
	"github.com/foursmith/player/internal/projection"
)

// Exercise the actual portable drawing shader, including its uniforms and
// texture sampling, against the projection geometry used by native playback.
func TestPortableProjectionDrawing(t *testing.T) {
	const sw, sh = 256, 128
	source := image.NewRGBA(image.Rect(0, 0, sw, sh))
	for y := range sh {
		for x := range sw {
			source.SetRGBA(x, y, color.RGBA{uint8(x), uint8(2 * y), uint8(x / 2), 255})
		}
	}
	surface := &bitmapSurface{bitmap: ui.NewBitmap(source), aspect: 2}
	for mode := projection.SBS180; mode <= projection.Flat; mode++ {
		t.Run(fmt.Sprint(mode), func(t *testing.T) {
			surface.params = Parameters{Mode: mode, View: projection.View{Yaw: 23, Pitch: -11, Forward: 14, Zoom: 1.2}, SplitCount: 2, Portrait: true}
			const width, height = 80, 40
			tt := ui.NewTester(func(c *ui.Context) {
				ui.Box(c).Fill().Draw(surface.Paint)
			}, width, height)
			out := tt.Image()
			count := len(projection.Viewports(width, height, 2, true))
			if mode == projection.Flat {
				count = 1
			}
			for y := 4; y < height; y += 7 {
				for x := 4; x < width; x += 7 {
					lx := (float64(x) + .5) * float64(count) / width
					lx -= math.Floor(lx)
					ly, aspect := (float64(y)+.5)/height, float64(width)/float64(count)/height
					var u, v float64
					var visible bool
					if mode == projection.Flat {
						u, v, visible = surface.params.FlatView.UV(lx, ly, aspect, 2)
					} else {
						origin, ray := projection.Ray(lx, ly, aspect, 80, surface.params.View)
						u, v, visible = projection.UV(mode, origin, ray)
					}
					if !visible {
						continue
					}
					want := source.RGBAAt(int(projection.Clamp(u*sw, 0, sw-1)), int(projection.Clamp(v*sh, 0, sh-1)))
					got := out.RGBAAt(x, y)
					for _, channel := range [][2]uint8{{got.R, want.R}, {got.G, want.G}, {got.B, want.B}, {got.A, want.A}} {
						if math.Abs(float64(channel[0])-float64(channel[1])) > 3 {
							t.Fatalf("pixel %d,%d source %.3f,%.3f: got %v, want %v", x, y, u, v, got, want)
						}
					}
				}
			}
		})
	}
}

// Use a decoded, reference-counted software frame: the surface must keep its
// borrowed planes alive after the caller releases its reference.
func TestPortableYUVFrameLifetimeAndModeSwitch(t *testing.T) {
	frame := portableYUVFrame(t, 16, 8)
	defer frame.Close()
	want, err := frame.RGBA(16, 8)
	if err != nil {
		t.Fatal(err)
	}
	var surface bitmapSurface
	defer surface.Close()
	params := Parameters{Mode: projection.Flat, FlatView: FlatView{Zoom: 1}}
	if err := surface.Present(frame, params, 32, 32, 1); err != nil {
		t.Fatal(err)
	}
	if surface.frame == nil {
		t.Fatal("software YUV did not use direct presentation")
	}
	frame.Close()
	view := ui.NewTester(func(c *ui.Context) { ui.Box(c).Fill().Draw(surface.Paint) }, 32, 32)
	for range 2 {
		runtime.GC()
		view.Frame()
		got, expected := view.Image().RGBAAt(16, 16), want.RGBAAt(8, 4)
		for _, pair := range [][2]uint8{{got.R, expected.R}, {got.G, expected.G}, {got.B, expected.B}} {
			if math.Abs(float64(pair[0])-float64(pair[1])) > 3 {
				t.Fatalf("YUV colors differ from swscale: got %v, want %v", got, expected)
			}
		}
		if got := view.Image().RGBAAt(16, 0); got != (color.RGBA{A: 255}) {
			t.Fatal("letterbox is not black", got)
		}
	}
	owned := surface.frame.Clone()
	if owned == nil {
		t.Fatal("cannot clone fixture")
	}
	defer owned.Close()
	params.Mode = projection.Mono360
	if err := surface.Present(owned, params, 32, 32, 1); err != nil || surface.frame != nil {
		t.Fatal("same frame did not switch to VR conversion", err)
	}
	params.Mode = projection.Flat
	if err := surface.Present(owned, params, 32, 32, 1); err != nil || surface.frame == nil {
		t.Fatal("same frame did not return to direct YUV", err)
	}
	surface.Clear()
	if surface.frame != nil || surface.bitmap != nil {
		t.Fatal("clear retained presentation resources")
	}
}

func portableYUVFrame(t testing.TB, width, height int) *media.Frame {
	t.Helper()
	path := filepath.Join(t.TempDir(), "color.y4m")
	data := []byte(fmt.Sprintf("YUV4MPEG2 W%d H%d F25:1 Ip A1:1 C420jpeg\nFRAME\n", width, height))
	data = append(data, bytes.Repeat([]byte{100}, width*height)...)
	data = append(data, bytes.Repeat([]byte{90}, ((width+1)/2)*((height+1)/2))...)
	data = append(data, bytes.Repeat([]byte{200}, ((width+1)/2)*((height+1)/2))...)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	decoder, err := media.Open(context.Background(), media.Source{URL: path}, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(decoder.Close)
	frame, err := decoder.Next()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(frame.Close)
	return frame
}

// Compare presentation preparation only; decoding and GPU submission are outside
// this benchmark. Reset serial so each iteration presents a new decoded picture.
func BenchmarkPortableFlatPreparation(b *testing.B) {
	frame := portableYUVFrame(b, 1920, 1080)
	params := Parameters{Mode: projection.Flat}
	b.Run("RGBA", func(b *testing.B) {
		var converter media.RGBAConverter
		defer converter.Close()
		bitmap := ui.NewBitmap(image.NewRGBA(image.Rect(0, 0, 1, 1)))
		for range 2 {
			img, err := converter.Convert(frame, 1920, 1080)
			if err != nil {
				b.Fatal(err)
			}
			bitmap.Update(img)
		}
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			img, err := converter.Convert(frame, 1920, 1080)
			if err != nil {
				b.Fatal(err)
			}
			bitmap.Update(img)
		}
	})
	b.Run("YUV", func(b *testing.B) {
		var surface bitmapSurface
		defer surface.Close()
		if err := surface.Present(frame, params, 1920, 1080, 1); err != nil {
			b.Fatal(err)
		}
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			surface.serial = 0
			if err := surface.Present(frame, params, 1920, 1080, 1); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func TestPortableFlatZoomPanAndLetterbox(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 80, 40))
	for y := range 40 {
		for x := range 80 {
			img.SetRGBA(x, y, color.RGBA{uint8(2 * x), uint8(4 * y), 60, 255})
		}
	}
	for _, tc := range []struct {
		name string
		view FlatView
	}{
		{"fit", FlatView{Zoom: 1}},
		{"zoom and pan", FlatView{Zoom: 2, X: .125}},
		{"vertical pan", FlatView{Zoom: 4, X: -.125, Y: .125}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			surface := bitmapSurface{bitmap: ui.NewBitmap(img), aspect: 2,
				params: Parameters{Mode: projection.Flat, FlatView: tc.view}}
			view := ui.NewTester(func(c *ui.Context) {
				ui.Box(c).Fill().Draw(func(p *ui.Painter, _ ui.Rect) {
					surface.Paint(p, ui.Rect{X: 4, Y: 8, W: 80, H: 80})
				})
			}, 88, 96)
			out := view.Image()
			for y := 3; y < 80; y += 7 {
				for x := 3; x < 80; x += 7 {
					u, v, visible := tc.view.UV((float64(x)+.5)/80, (float64(y)+.5)/80, 1, 2)
					want := color.RGBA{A: 255}
					if visible {
						want = img.RGBAAt(int(u*80), int(v*40))
					}
					got := out.RGBAAt(4+x, 8+y)
					for _, pair := range [][2]uint8{{got.R, want.R}, {got.G, want.G}, {got.B, want.B}, {got.A, want.A}} {
						if math.Abs(float64(pair[0])-float64(pair[1])) > 4 {
							t.Fatalf("pixel %d,%d: got %v, want %v", x, y, got, want)
						}
					}
				}
			}
		})
	}
}
