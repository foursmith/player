package video

import (
	"bytes"
	"image"
	"image/color"
	"math"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/foursmith/player/internal/projection"
)

func TestAmbientBarsFollowContent(t *testing.T) {
	for _, vertical := range []bool{false, true} {
		w, h := 160, 80
		if vertical {
			w, h = h, w
		}
		img := image.NewRGBA(image.Rect(0, 0, w, h))
		for y := range h {
			for x := range w {
				c := color.RGBA{R: 240, A: 255}
				if (!vertical && x >= w/2) || (vertical && y >= h/2) {
					c = color.RGBA{B: 240, A: 255}
				}
				img.SetRGBA(x, y, c)
			}
		}
		surface := bitmapSurface{bitmap: ui.NewBitmap(img), aspect: float64(w) / float64(h), params: Parameters{Mode: projection.Flat}}
		tt := ui.NewTester(func(c *ui.Context) { ui.Box(c).Fill().Draw(surface.Paint) }, 160, 160)
		out := tt.Image()
		near, far, blue, sharp := out.RGBAAt(32, 38), out.RGBAAt(32, 0), out.RGBAAt(128, 38), out.RGBAAt(32, 60)
		if vertical {
			near, far, blue, sharp = out.RGBAAt(38, 32), out.RGBAAt(0, 32), out.RGBAAt(38, 128), out.RGBAAt(60, 32)
		}
		if near.R < 50 || near.R > 245 || near.R <= near.B*2 || blue.B <= blue.R*2 {
			t.Fatalf("ambient light lost spatial color: red %v blue %v", near, blue)
		}
		if far.R >= near.R/2 {
			t.Fatalf("glow does not fade toward background: near %v far %v", near, far)
		}
		if sharp != (color.RGBA{R: 240, A: 255}) {
			t.Fatalf("glow changed video pixels: %v", sharp)
		}
		// A new playback resets the light field, including a completely black frame.
		for y := range h {
			for x := range w {
				img.SetRGBA(x, y, color.RGBA{A: 255})
			}
		}
		surface.bitmap.Update(img)
		surface.params.Generation++
		tt.Frame()
		out = tt.Image()
		if p := out.RGBAAt(38, 38); p != (color.RGBA{A: 255}) {
			t.Fatalf("black frame retained light: %v", p)
		}
	}
}

func TestAmbientHistoryRejectsFlickerAndResets(t *testing.T) {
	for _, fps := range []int{24, 30, 60} {
		var history ambientHistory
		value := 0.0
		size := image.Pt(160, 80)
		for i := 0; i <= fps; i++ {
			alpha, _ := history.step(uint64(i+1), float64(i)/float64(fps), 1, size)
			if i > 0 {
				value += (1 - value) * float64(alpha)
			}
		}
		if value < .93 || value > .95 {
			t.Fatalf("%d fps settling: %v", fps, value)
		}
		if alpha, changed := history.step(uint64(fps+1), 1, 1, size); changed || alpha != 0 {
			t.Fatal("paused redraw changed light")
		}
		if alpha, _ := history.step(uint64(fps+1), 1, 2, size); alpha != 1 {
			t.Fatal("new playback generation retained previous color")
		}
		if alpha, _ := history.step(uint64(fps+2), .5, 2, size); alpha != 1 {
			t.Fatal("backward seek retained previous color")
		}
	}
	var history ambientHistory
	value := 0.0
	for i := 0; i < 120; i++ {
		target := float64(i % 2)
		alpha, _ := history.step(uint64(i+1), float64(i)/30, 1, image.Pt(160, 80))
		next := value + (target-value)*float64(alpha)
		if i > 60 && math.Abs(next-value) > .06 {
			t.Fatal("alternating bright/dark frames still flash", value, next)
		}
		value = next
	}
}

func TestPortableAmbientTransition(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 16, 8))
	fill := func(c color.RGBA) {
		for y := range 8 {
			for x := range 16 {
				img.SetRGBA(x, y, c)
			}
		}
	}
	fill(color.RGBA{R: 255, A: 255})
	surface := bitmapSurface{bitmap: ui.NewBitmap(img), aspect: 2, serial: 1, params: Parameters{Mode: projection.Flat, Generation: 1}}
	tt := ui.NewTester(func(c *ui.Context) { ui.Box(c).Fill().Draw(surface.Paint) }, 32, 32)
	first := tt.Image().RGBAAt(16, 5)
	fill(color.RGBA{B: 255, A: 255})
	surface.bitmap.Update(img)
	surface.serial, surface.pts = 2, 1.0/30
	tt.Frame()
	out := tt.Image()
	light := out.RGBAAt(16, 5)
	if float64(light.R) < float64(first.R)*.8 || light.B > light.R/3 {
		t.Fatalf("light jumped at scene change: %v -> %v", first, light)
	}
	if p := out.RGBAAt(16, 16); p != (color.RGBA{B: 255, A: 255}) {
		t.Fatal("temporal filtering delayed video", p)
	}
	tt.Frame()
	if p := tt.Image().RGBAAt(16, 5); p != light {
		t.Fatal("paused redraw continued blending", light, p)
	}
	previous := light
	for serial := uint64(3); serial <= 6; serial++ {
		surface.serial, surface.pts = serial, float64(serial-1)/30
		tt.Frame()
		next := tt.Image().RGBAAt(16, 5)
		if next.R > previous.R || next.B <= previous.B {
			t.Fatal("history did not converge smoothly", previous, next)
		}
		previous = next
	}
	surface.params.Generation++
	tt.Frame()
	if p := tt.Image().RGBAAt(16, 5); p.B < 50 || p.R > 3 {
		t.Fatal("seek retained prior light", p)
	}
	surface.params.DisableAmbientLight = true
	tt.Frame()
	if p := tt.Image().RGBAAt(16, 5); p != (color.RGBA{A: 255}) || surface.ambientHistory.valid {
		t.Fatal("disabled light retained presentation history", p)
	}
	if p := tt.Image().RGBAAt(16, 16); p != (color.RGBA{B: 255, A: 255}) {
		t.Fatal("switch changed video", p)
	}
	surface.params.DisableAmbientLight = false
	tt.Frame()
	if p := tt.Image().RGBAAt(16, 5); p.B < 50 || p.R > 3 {
		t.Fatal("reenabling light retained old colors", p)
	}
	surface.Clear()
	if surface.ambientCanvases[0] != nil || surface.ambientHistory.valid {
		t.Fatal("clear retained history")
	}
}

// Thin colored edges around a neutral center must light the adjacent bars.
// Sampling a compressed copy of the whole frame used to dilute these colors.
func TestAmbientUsesAdjacentEdgeColors(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 128, 72))
	for y := range 72 {
		for x := range 128 {
			c := color.RGBA{R: 160, G: 160, B: 160, A: 255}
			if y < 9 {
				c = color.RGBA{R: 30, G: 90, B: 190, A: 255}
			}
			if y >= 63 {
				c = color.RGBA{R: 190, G: 40, B: 140, A: 255}
			}
			img.SetRGBA(x, y, c)
		}
	}
	surface := bitmapSurface{bitmap: ui.NewBitmap(img), aspect: 128.0 / 72, params: Parameters{Mode: projection.Flat}, serial: 1}
	tt := ui.NewTester(func(c *ui.Context) { ui.Box(c).Fill().Draw(surface.Paint) }, 64, 96)
	out := tt.Image()
	top, bottom := out.RGBAAt(32, 29), out.RGBAAt(32, 66)
	if int(top.B) < int(top.R)*2 || top.B < 100 {
		t.Fatal("blue edge lost in neutral center", top)
	}
	if int(bottom.R) < int(bottom.G)*2 || bottom.R < 100 {
		t.Fatal("pink edge lost in neutral center", bottom)
	}
	if p := out.RGBAAt(32, 0); p.B < 8 || p.B <= p.R {
		t.Fatal("window edge lost the adjacent blue light", p)
	}
}

func TestAmbientDitherKeepsDarkGradientsNeutralAndStill(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 64, 32))
	for y := range 32 {
		for x := range 64 {
			img.SetRGBA(x, y, color.RGBA{R: 15, G: 15, B: 15, A: 255})
		}
	}
	surface := bitmapSurface{bitmap: ui.NewBitmap(img), aspect: 2, params: Parameters{Mode: projection.Flat}}
	tt := ui.NewTester(func(c *ui.Context) { ui.Box(c).Fill().Draw(surface.Paint) }, 64, 96)
	out := tt.Image()
	low, high := uint8(255), uint8(0)
	total := 0
	for x := range 64 {
		p := out.RGBAAt(x, 31)
		if p.R != p.G || p.G != p.B || p.A != 255 {
			t.Fatal("dither tinted neutral light", p)
		}
		low, high = min(low, p.R), max(high, p.R)
		total += int(p.R)
	}
	if high-low != 1 {
		t.Fatalf("flat gradient was not distributed across adjacent levels: %d..%d", low, high)
	}
	if mean := float64(total) / 64; mean < 15 || mean > 15.3 {
		t.Fatal("dither changed average light strength", mean)
	}
	if p := out.RGBAAt(32, 0); p.R < 2 || p.R >= low || p.R != p.G || p.G != p.B {
		t.Fatal("outer light lost its dim neutral color", p)
	}
	if p := out.RGBAAt(32, 48); p != (color.RGBA{R: 15, G: 15, B: 15, A: 255}) {
		t.Fatal("dither modified the video", p)
	}
	tt.Frame()
	if !bytes.Equal(out.Pix, tt.Image().Pix) {
		t.Fatal("static gradient dither flickered")
	}
}

func TestAmbientAdaptsToTallAndWideBars(t *testing.T) {
	for _, vertical := range []bool{true, false} {
		sw, sh, w, h := 128, 72, 32, 96
		if !vertical {
			sw, sh, w, h = 72, 128, 96, 32
		}
		img := image.NewRGBA(image.Rect(0, 0, sw, sh))
		fill := color.RGBA{R: 120, G: 80, B: 60, A: 255}
		for y := range sh {
			for x := range sw {
				img.SetRGBA(x, y, fill)
			}
		}
		surface := bitmapSurface{bitmap: ui.NewBitmap(img), aspect: float64(sw) / float64(sh), params: Parameters{Mode: projection.Flat}}
		tt := ui.NewTester(func(c *ui.Context) { ui.Box(c).Fill().Draw(surface.Paint) }, w, h)
		out := tt.Image()
		mid, near, edge := out.RGBAAt(16, 16), out.RGBAAt(16, 38), out.RGBAAt(16, 0)
		if !vertical {
			mid, near, edge = out.RGBAAt(16, 16), out.RGBAAt(38, 16), out.RGBAAt(0, 16)
		}
		if mid.R < 12 || mid.R >= near.R {
			t.Fatalf("light does not diffuse through large bar (vertical=%v): midway %v near %v", vertical, mid, near)
		}
		if edge.R < 8 || edge.R >= mid.R {
			t.Fatal("far window edge must retain dimmed color", edge)
		}
		if p := out.RGBAAt(w/2, h/2); p != fill {
			t.Fatal("adaptive spread changed video", p)
		}
	}
}

func TestAmbientJoinsUniformVideoEdges(t *testing.T) {
	for _, vertical := range []bool{true, false} {
		sw, sh, w, h := 128, 72, 128, 160
		if !vertical {
			sw, sh, w, h = 72, 128, 160, 128
		}
		pink := color.RGBA{R: 180, G: 145, B: 160, A: 255}
		img := image.NewRGBA(image.Rect(0, 0, sw, sh))
		for y := range sh {
			for x := range sw {
				img.SetRGBA(x, y, pink)
			}
		}
		surface := bitmapSurface{bitmap: ui.NewBitmap(img), aspect: float64(sw) / float64(sh), params: Parameters{Mode: projection.Flat}, serial: 1}
		tt := ui.NewTester(func(c *ui.Context) { ui.Box(c).Fill().Draw(surface.Paint) }, w, h)
		out := tt.Image()
		edge := out.RGBAAt(64, 43)
		if !vertical {
			edge = out.RGBAAt(43, 64)
		}
		for _, pair := range [][2]uint8{{edge.R, pink.R}, {edge.G, pink.G}, {edge.B, pink.B}} {
			if math.Abs(float64(pair[0])-float64(pair[1])) > 2 {
				t.Fatalf("visible intensity seam (vertical=%v): video %v glow %v", vertical, pink, edge)
			}
		}
	}
}

func TestAmbientDoesNotExtrudeSourceBlackBorders(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 72, 128))
	for y := range 128 {
		for x := range 72 {
			c := color.RGBA{R: 150, G: 150, B: 150, A: 255}
			if x < 3 || x >= 69 {
				c = color.RGBA{A: 255}
			}
			img.SetRGBA(x, y, c)
		}
	}
	surface := bitmapSurface{bitmap: ui.NewBitmap(img), aspect: 72.0 / 128, params: Parameters{Mode: projection.Flat}, serial: 1}
	tt := ui.NewTester(func(c *ui.Context) { ui.Box(c).Fill().Draw(surface.Paint) }, 128, 96)
	out := tt.Image()
	for _, x := range []int{36, 91} {
		if p := out.RGBAAt(x, 48); p.R < 60 {
			t.Fatal("source border was stretched into the ambient field", x, p)
		}
	}
	if p := out.RGBAAt(37, 48); p != (color.RGBA{A: 255}) {
		t.Fatal("ambient effect changed original video border", p)
	}
}

func TestPortableAmbientDisabledCapture(t *testing.T) {
	frame := portableYUVFrame(t, 16, 8)
	var surface bitmapSurface
	params := Parameters{Mode: projection.Flat}
	enabled, err := surface.Capture(frame, params, 32, 32)
	if err != nil {
		t.Fatal(err)
	}
	params.DisableAmbientLight = true
	disabled, err := surface.Capture(frame, params, 32, 32)
	if err != nil {
		t.Fatal(err)
	}
	if p := disabled.RGBAAt(16, 5); p != (color.RGBA{A: 255}) {
		t.Fatal("disabled capture contains ambient light", p)
	}
	if enabled.RGBAAt(16, 5).R == 0 {
		t.Fatal("fixture did not show enabled light")
	}
	if disabled.RGBAAt(16, 16) != enabled.RGBAAt(16, 16) {
		t.Fatal("switch changed video pixels")
	}
}
