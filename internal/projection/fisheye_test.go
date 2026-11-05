package projection

import (
	"image"
	"image/color"
	"math"
	"testing"
)

func lensFrame(stereo bool, size int, centerX, centerY, radius, scaleY float64, watermark, darkSubject bool) *image.RGBA {
	width := size
	if stereo {
		width *= 2
	}
	img := image.NewRGBA(image.Rect(0, 0, width, size))
	for eye := range width / size {
		// Deliberately unrelated texture: lens identification must not depend
		// on matching a subject between the left and right camera.
		texture := textured(size, size, uint64(100+eye*91))
		for y := range size {
			for x := range size {
				value := color.RGBA{R: uint8((x + y) % 5), A: 255}
				u, v := (float64(x)+.5)/float64(size), (float64(y)+.5)/float64(size)
				if math.Hypot(u-centerX, (v-centerY)*scaleY) <= radius {
					value = texture.RGBAAt(x, y)
					if darkSubject && u < .4 && v < .4 {
						value = color.RGBA{A: 255}
					}
				}
				if watermark && (u < .28 && v < .075 || u > .72 && v > .925) {
					value = color.RGBA{R: 230, G: 230, B: 230, A: 255}
				}
				img.SetRGBA(eye*size+x, y, value)
			}
		}
	}
	return img
}

func TestFisheyeFitsVisibleRimsWithWatermarksCropAndOffset(t *testing.T) {
	for _, tc := range []struct {
		name        string
		x, y, r, sy float64
		watermark   bool
		darkSubject bool
	}{
		{"inscribed", .5, .5, .49, 1, false, false},
		{"corner watermarks", .5, .5, .49, 1, true, false},
		{"cropped rim", .5, .5, .61, 1, false, false},
		{"off-center lens", .43, .56, .47, 1, false, false},
		{"dark subject and watermarks", .5, .5, .49, 1, true, true},
		{"wide ellipse and watermarks", .5, .5, .49, 1.07, true, false},
		{"tall ellipse and watermarks", .5, .5, .49, .96, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, size := range []int{160, 320} {
				for _, stereo := range []bool{false, true} {
					aspect, want := 1.0, MonoFisheye
					if stereo {
						aspect, want = 2, SBSFisheye
					}
					e := AnalyzeFrame(lensFrame(stereo, size, tc.x, tc.y, tc.r, tc.sy, tc.watermark, tc.darkSubject), aspect)
					if !e.Usable || e.Mode != want {
						t.Fatalf("size %d stereo %v: got %+v want %v", size, stereo, e, want)
					}
				}
			}
		})
	}
}

func TestDarkRectangularAndEquirectangularBoundariesAreNotFisheye(t *testing.T) {
	for _, shape := range []string{"letterbox", "pillarbox", "rounded rectangle", "equirectangular lens limit"} {
		t.Run(shape, func(t *testing.T) {
			for _, size := range []int{160, 320} {
				img := textured(size, size, 17)
				for y := range size {
					for x := range size {
						u, v := (float64(x)+.5)/float64(size)-.5, (float64(y)+.5)/float64(size)-.5
						dark := false
						switch shape {
						case "letterbox":
							dark = math.Abs(v) > .35
						case "pillarbox":
							dark = math.Abs(u) > .35
						case "rounded rectangle":
							dark = math.Hypot(max(0, math.Abs(u)-.28), max(0, math.Abs(v)-.28)) > .22
						case "equirectangular lens limit":
							// A limited camera field projected into 180 EQR has
							// curved black corners, but no circular image-plane rim.
							dark = math.Cos(u*math.Pi)*math.Cos(v*math.Pi) < math.Cos(85*math.Pi/180)
						}
						if dark {
							img.SetRGBA(x, y, color.RGBA{A: 255})
						}
					}
				}
				if e := AnalyzeFrame(img, 1); e.Mode.Fisheye() {
					t.Fatalf("size %d: %+v", size, e)
				}
			}
		})
	}
}

func TestVisibleFisheyeConsensusOverridesContradictingAI(t *testing.T) {
	detector := &FrameDetector{}
	model := &ModelScores{}
	for range 3 {
		observeImage(detector, lensFrame(true, 160, .5, .5, .49, 1, true, false), 2)
		model.Add(prediction(SBS180))
	}
	if decision := detector.Resolve(SBS180, model); decision.Mode != SBSFisheye {
		t.Fatalf("a visible circular rim was erased by AI scores: %+v", decision)
	}
	// An unambiguous prediction may still refine frames without a lens rim.
	model = &ModelScores{}
	model.Add(prediction(Mono180))
	model.Add(prediction(Mono180))
	detector = &FrameDetector{}
	observeImage(detector, textured(320, 160, 89), 2)
	observeImage(detector, textured(320, 160, 90), 2)
	if decision := detector.Resolve(Flat, model); decision.Mode != Mono180 || decision.Reason != "model_confident" {
		t.Fatalf("AI stopped refining ambiguous frames: %+v", decision)
	}
}
