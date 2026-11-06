package media

import (
	"bytes"
	"image/color"
	"testing"
)

func conversionFrame(t testing.TB, width, height int) *Frame {
	t.Helper()
	if err := loadMedia(); err != nil {
		t.Fatal(err)
	}
	// Padding permits swscale's SIMD readers to read beyond the last pixel.
	y := bytes.Repeat([]byte{100}, width*height+64)
	u := bytes.Repeat([]byte{90}, width*height+64)
	v := bytes.Repeat([]byte{200}, width*height+64)
	return &Frame{ptr: &avFrame{
		Width: int32(width), Height: int32(height), Format: av.util.PixelFormat("yuv444p"),
		Data: [8]*byte{&y[0], &u[0], &v[0]}, Linesize: [8]int32{int32(width), int32(width), int32(width)},
		Colorspace: 1, ColorRange: 1,
	}}
}

func TestConverterReusesBuffersWithoutChangingDisplayedImage(t *testing.T) {
	frame := conversionFrame(t, 80, 40)
	var converter RGBAConverter
	defer converter.Close()
	first, err := converter.Convert(frame, 80, 40)
	if err != nil {
		t.Fatal(err)
	}
	saved := bytes.Clone(first.Pix)
	// A second frame with a different matrix must leave the displayed one
	// intact until its replacement is handed to the bitmap.
	frame.ptr.Colorspace = 5
	second, err := converter.Convert(frame, 80, 40)
	if err != nil || first == second || !bytes.Equal(first.Pix, saved) || bytes.Equal(first.Pix, second.Pix) {
		t.Fatal("conversion overwrote the displayed frame or ignored its matrix", err)
	}
	third, err := converter.Convert(frame, 80, 40)
	if err != nil || third != first || !bytes.Equal(second.Pix, third.Pix) {
		t.Fatal("steady playback did not reuse the retired buffer", err)
	}
	resized, err := converter.Convert(frame, 32, 16)
	if err != nil || resized.Bounds().Dx() != 32 || resized.Bounds().Dy() != 16 {
		t.Fatal("changing video size failed", err)
	}
	if _, err := converter.Convert(conversionFrame(t, 64, 48), 32, 16); err != nil {
		t.Fatal("changing source dimensions with the same output size failed", err)
	}
	converter.Close()
	converter.Close()
	if _, err := converter.Convert(frame, 80, 40); err != nil {
		t.Fatal("closed converter could not be reused", err)
	}
	for _, size := range [][2]int{{0, 10}, {10, -1}} {
		if _, err := converter.Convert(frame, size[0], size[1]); err == nil {
			t.Fatal("invalid size was accepted", size)
		}
	}
	if _, err := converter.Convert(&Frame{}, 10, 10); err == nil {
		t.Fatal("closed frame was accepted")
	}
}

func TestConverterHonorsVideoColorMatrixAndRange(t *testing.T) {
	frame := conversionFrame(t, 80, 40)
	var converter RGBAConverter
	defer converter.Close()
	for _, tc := range []struct {
		name         string
		matrix, full int32
		want         color.RGBA
	}{
		{"limited BT709", 1, 1, color.RGBA{227, 68, 18, 255}},
		{"limited BT601", 5, 1, color.RGBA{213, 54, 21, 255}},
		{"full BT709", 1, avColorFull, color.RGBA{213, 73, 29, 255}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			frame.ptr.Colorspace, frame.ptr.ColorRange = tc.matrix, tc.full
			img, err := converter.Convert(frame, 80, 40)
			if err != nil {
				t.Fatal(err)
			}
			got := img.RGBAAt(40, 20)
			for _, pair := range [][2]uint8{{got.R, tc.want.R}, {got.G, tc.want.G}, {got.B, tc.want.B}, {got.A, tc.want.A}} {
				if difference := int(pair[0]) - int(pair[1]); difference < -3 || difference > 3 {
					t.Fatalf("got %v, want approximately %v", got, tc.want)
				}
			}
		})
	}
}

func BenchmarkPlaybackConversion(b *testing.B) {
	frame := conversionFrame(b, 3840, 2160)
	for _, reuse := range []bool{false, true} {
		name := "new buffers"
		if reuse {
			name = "reused buffers"
		}
		b.Run(name, func(b *testing.B) {
			var converter RGBAConverter
			defer converter.Close()
			// Warm up both presentation buffers outside the measurement.
			for range 2 {
				if _, err := converter.Convert(frame, 3840, 2160); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				if !reuse {
					converter.Close()
				}
				if _, err := converter.Convert(frame, 3840, 2160); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
