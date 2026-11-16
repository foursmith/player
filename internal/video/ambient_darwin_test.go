package video

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/foursmith/player/internal/media"
	"github.com/foursmith/player/internal/projection"
)

// Exercise the history pass with real native YUV textures, without a window.
func TestMetalAmbientTransition(t *testing.T) {
	input := filepath.Join(t.TempDir(), "transition.y4m")
	data := []byte("YUV4MPEG2 W160 H80 F30:1 Ip A1:1 C420jpeg\n")
	for _, planes := range [][3]byte{{81, 90, 240}, {41, 240, 110}} {
		data = append(data, []byte("FRAME\n")...)
		for i, value := range planes {
			size := 160 * 80
			if i > 0 {
				size /= 4
			}
			data = append(data, bytes.Repeat([]byte{value}, size)...)
		}
	}
	if err := os.WriteFile(input, data, 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "transition.mp4")
	if out, err := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-i", input, "-c:v", "libx264", "-pix_fmt", "yuv420p", "-y", path).CombinedOutput(); err != nil {
		t.Fatalf("fixture: %v %s", err, out)
	}
	d, err := media.Open(context.Background(), media.Source{URL: path}, true)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	red, err := d.Next()
	if err != nil {
		t.Fatal(err)
	}
	defer red.Close()
	blue, err := d.Next()
	if err != nil {
		t.Fatal(err)
	}
	defer blue.Close()
	if red.Native() == nil {
		t.Skip("native decoder unavailable")
	}
	r, err := newMetal(0)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer metalPool()()
	output := send(r.device, "newTextureWithDescriptor:", textureDescriptor(80, 32, 32, 4))
	defer send(output, "release")
	params := Parameters{Mode: projection.Flat, Generation: 1}
	render := func(frame *media.Frame) *image.RGBA {
		t.Helper()
		img := image.NewRGBA(image.Rect(0, 0, 32, 32))
		if err := r.render(frame, params, output, 0, img, true); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < len(img.Pix); i += 4 {
			img.Pix[i], img.Pix[i+2] = img.Pix[i+2], img.Pix[i]
		}
		return img
	}
	first := render(red).RGBAAt(16, 5)
	out := render(blue)
	light := out.RGBAAt(16, 5)
	if float64(light.R) < float64(first.R)*.8 || light.B > light.R/3 {
		t.Fatalf("light jumped at scene cut: %v -> %v", first, light)
	}
	if p := out.RGBAAt(16, 16); p.B < 200 || p.R > 40 {
		t.Fatal("history delayed picture", p)
	}
	// Analysis/screenshot captures must not affect presentation history.
	if _, err := r.Capture(red, params, 32, 32); err != nil {
		t.Fatal(err)
	}
	if p := render(blue).RGBAAt(16, 5); p != light {
		t.Fatal("capture or paused redraw changed light", light, p)
	}
	params.Generation++
	if p := render(blue).RGBAAt(16, 5); p.B < 50 || p.R > 10 {
		t.Fatal("seek retained prior light", p)
	}
	params.DisableAmbientLight = true
	if p := render(blue).RGBAAt(16, 5); p != (color.RGBA{A: 255}) || r.ambientHistory.valid {
		t.Fatal("disabled native light retained history", p)
	}
	disabled, err := r.Capture(blue, params, 32, 32)
	if err != nil {
		t.Fatal(err)
	}
	if p := disabled.RGBAAt(16, 5); p != (color.RGBA{A: 255}) {
		t.Fatal("disabled native capture contains glow", p)
	}
	if p := disabled.RGBAAt(16, 16); p.B < 200 || p.R > 40 {
		t.Fatal("switch changed captured video", p)
	}
	params.DisableAmbientLight = false
	if p := render(blue).RGBAAt(16, 5); p.B < 50 || p.R > 10 {
		t.Fatal("reenabling native light retained old colors", p)
	}
	r.Clear()
	if r.ambientHistory.valid {
		t.Fatal("clear retained history")
	}
	if p := render(red).RGBAAt(16, 5); p.R < 50 || p.B > 10 {
		t.Fatal("clear failed to reset light", p)
	}
}

func TestMetalAmbientDitherIsSubtleAndStable(t *testing.T) {
	frame := portableYUVFrame(t, 160, 80)
	r, err := newMetal(0)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	params := Parameters{Mode: projection.Flat}
	first, err := r.Capture(frame, params, 64, 96)
	if err != nil {
		t.Fatal(err)
	}
	low, high := uint8(255), uint8(0)
	for x := range 64 {
		p := first.RGBAAt(x, 31)
		low, high = min(low, p.R), max(high, p.R)
	}
	if high-low != 1 {
		t.Fatalf("native gradient was not distributed across adjacent levels: %d..%d", low, high)
	}
	second, err := r.Capture(frame, params, 64, 96)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.Pix, second.Pix) {
		t.Fatal("static native gradient dither flickered")
	}
}

func TestMetalAmbientJoinsVideoEdge(t *testing.T) {
	frame := portableYUVFrame(t, 160, 80)
	r, err := newMetal(0)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	out, err := r.Capture(frame, Parameters{Mode: projection.Flat}, 128, 160)
	if err != nil {
		t.Fatal(err)
	}
	video, glow := out.RGBAAt(64, 48), out.RGBAAt(64, 47)
	for _, pair := range [][2]uint8{{video.R, glow.R}, {video.G, glow.G}, {video.B, glow.B}} {
		if math.Abs(float64(pair[0])-float64(pair[1])) > 2 {
			t.Fatal("native video/ambient edge has an intensity seam", video, glow)
		}
	}
}
