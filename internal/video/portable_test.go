package video

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"testing"

	"github.com/egoist/mygo/ui"
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
	surface := &rgbaSurface{bitmap: ui.NewBitmap(source), aspect: 2}
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
