package projection

import (
	"image"
	"image/color"
	"math"
	"math/rand/v2"
	"testing"
)

func observeImage(detector *FrameDetector, img image.Image, aspect float64) bool {
	detector.Observe(AnalyzeFrame(img, aspect))
	return detector.SamplingComplete(nil)
}

func textured(width, height int, seed uint64) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	random := rand.New(rand.NewPCG(seed, seed+1))
	for y := range height {
		for x := range width {
			v := uint8(40 + random.IntN(170))
			img.SetRGBA(x, y, color.RGBA{v, v, v, 255})
		}
	}
	return img
}

func stereoFrame(tb, fisheye bool) *image.RGBA {
	w, h := 160, 160
	if tb {
		w = 320
	}
	eye := textured(w, h, 123)
	width, height := w*2, h
	if tb {
		width, height = w, h*2
	}
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := range h {
		for x := range w {
			left := eye.RGBAAt(x, y)
			// Different scene depths, exposure and a small vertical disparity.
			dx := 3 + (y/40)%3*3
			right := eye.RGBAAt(min(w-1, x+dx), min(h-1, y+1))
			right.R, right.G, right.B = uint8(float64(right.R)*.85+12), uint8(float64(right.G)*.85+12), uint8(float64(right.B)*.85+12)
			if fisheye && math.Hypot((float64(x)+.5)/float64(w)-.5, (float64(y)+.5)/float64(h)-.5) > .5 {
				left, right = color.RGBA{A: 255}, color.RGBA{A: 255}
			}
			img.SetRGBA(x, y, left)
			if tb {
				img.SetRGBA(x, y+h, right)
			} else {
				img.SetRGBA(x+w, y, right)
			}
		}
	}
	return img
}

func TestAnalyzeStereoFramesWithParallaxAndExposureChanges(t *testing.T) {
	for _, tc := range []struct {
		tb, fisheye bool
		aspect      float64
		want        Mode
	}{{false, false, 2, SBS180}, {false, true, 2, SBSFisheye}, {true, false, 1, TB360}} {
		got := AnalyzeFrame(stereoFrame(tc.tb, tc.fisheye), tc.aspect)
		if !got.Usable || got.Mode != tc.want {
			t.Fatalf("%+v: %+v", tc, got)
		}
	}
}

func TestCloseStereoSubjectsUseWiderDisparityWithoutRelaxingThresholds(t *testing.T) {
	for _, tb := range []bool{false, true} {
		w, h, aspect, want := 160, 160, 2.0, SBS180
		if tb {
			w, aspect, want = 320, 1, TB360
		}
		eye := textured(w, h, 867)
		width, height := 2*w, h
		if tb {
			width, height = w, 2*h
		}
		img := image.NewRGBA(image.Rect(0, 0, width, height))
		for y := range h {
			for x := range w {
				shift := w/6 + (y/40)*w/50
				img.SetRGBA(x, y, eye.RGBAAt(x, y))
				other := eye.RGBAAt(min(w-1, x+shift), min(h-1, y+1))
				if tb {
					img.SetRGBA(x, y+h, other)
				} else {
					img.SetRGBA(x+w, y, other)
				}
			}
		}
		evidence := AnalyzeFrame(img, aspect)
		if evidence.Mode != want || !evidence.WideDisparity || evidence.StereoAgreement < .4 || evidence.Similarity < .75 {
			t.Fatalf("close stereo subject tb=%t: %+v", tb, evidence)
		}
	}
}

func TestStereoSearchCannotMatchBackIntoTheSameEye(t *testing.T) {
	img := stereoFrame(false, false)
	g := grayscale(img)
	left, right := image.Rect(16, 16, 48, 48), image.Rect(176, 16, 208, 48)
	if score := g.correlation(left, right, -160, 0, g.luma, image.Rect(160, 0, 320, 160)); score != 0 {
		t.Fatal("a candidate outside the target eye matched the source eye:", score)
	}
}

func TestAnalyzeFlatAndBlackFrames(t *testing.T) {
	gradient := image.NewRGBA(image.Rect(0, 0, 320, 160))
	letterbox := textured(320, 160, 17)
	black := image.NewRGBA(image.Rect(0, 0, 320, 160))
	dark := textured(320, 160, 18)
	for y := range 160 {
		for x := range 320 {
			v := uint8(30 + x/3 + y/4)
			gradient.SetRGBA(x, y, color.RGBA{v, v, v, 255})
			dark.SetRGBA(x, y, color.RGBA{uint8(x % 5), uint8(y % 5), 0, 255})
			if y < 24 || y >= 136 {
				letterbox.SetRGBA(x, y, color.RGBA{A: 255})
			}
		}
	}
	for _, tc := range []struct {
		name   string
		img    image.Image
		aspect float64
		usable bool
	}{
		{"ordinary wide frame", textured(320, 160, 77), 2, true},
		{"unrelated eyes", textured(320, 160, 78), 2, true},
		{"smooth gradient", gradient, 2, true},
		{"letterboxed movie", letterbox, 2, true},
		{"square movie", textured(320, 320, 79), 1, true},
		{"flat stereo movie", stereoFrame(false, false), 32.0 / 9, true},
		{"black intro", black, 2, false},
		{"nearly black frame", dark, 2, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := AnalyzeFrame(tc.img, tc.aspect)
			if got.Mode != Flat || got.Usable != tc.usable {
				t.Fatal(got)
			}
		})
	}
}

func TestAnalyzeMonoFisheyeAndPanorama(t *testing.T) {
	fisheye := textured(160, 160, 456)
	for y := range 160 {
		for x := range 160 {
			if math.Hypot((float64(x)+.5)/160-.5, (float64(y)+.5)/160-.5) > .5 {
				fisheye.SetRGBA(x, y, color.RGBA{A: 255})
			}
		}
	}
	panorama := image.NewRGBA(image.Rect(0, 0, 320, 160))
	for y := range 160 {
		for x := range 320 {
			longitude, latitude := float64(x)*2*math.Pi/320, math.Sin(float64(y)*math.Pi/160)
			v := uint8(128 + latitude*(45*math.Cos(longitude*3)+30*math.Cos(longitude*5)))
			panorama.SetRGBA(x, y, color.RGBA{v, v, v, 255})
		}
	}
	if got := AnalyzeFrame(fisheye, 1); got.Mode != MonoFisheye || !got.Usable {
		t.Fatal("mono fisheye", got)
	}
	if got := AnalyzeFrame(panorama, 2); got.Mode != Mono360 || !got.Usable {
		t.Fatal("wrapped panorama", got)
	}
}

func TestFrameConsensusSkipsBlackAndRequiresAgreement(t *testing.T) {
	d := &FrameDetector{}
	black := image.NewRGBA(image.Rect(0, 0, 320, 160))
	for range 4 {
		if observeImage(d, black, 2) {
			t.Fatal("black frames completed detection")
		}
	}
	if d.Samples != 0 {
		t.Fatal("black frames entered the vote")
	}
	observeImage(d, stereoFrame(false, false), 2)
	if _, ok := d.Result(); ok {
		t.Fatal("one frame was enough to classify VR")
	}
	observeImage(d, textured(320, 160, 900), 2)
	if _, ok := d.Result(); ok {
		t.Fatal("disagreement was classified")
	}
	if !observeImage(d, stereoFrame(false, false), 2) {
		t.Fatal("three usable frames did not complete detection")
	}
	if mode, ok := d.Result(); !ok || mode != SBS180 {
		t.Fatal(mode, ok)
	}
}

func TestRepeatedBinocularMatchesAreNotOutvotedByUnmatchedViews(t *testing.T) {
	for _, layout := range []Mode{SBS180, TB360} {
		for _, tc := range []struct {
			name  string
			modes []Mode
			want  Mode
		}{
			{"three matches among unmatched views", []Mode{Flat, Flat, layout, Flat, layout, Flat, layout, Flat, Flat}, layout},
			{"four matches from the real-video failure", []Mode{layout, Flat, Flat, layout, Flat, Flat, layout, Flat, layout}, layout},
			{"only two matches", []Mode{Flat, Flat, layout, Flat, layout, Flat, Flat, Flat, Flat}, Flat},
			{"conflicting VR view", []Mode{Mono360, Flat, layout, Flat, layout, Flat, layout, Flat, Flat}, Flat},
		} {
			t.Run(IDs[layout]+"/"+tc.name, func(t *testing.T) {
				detector := &FrameDetector{}
				for _, mode := range tc.modes {
					detector.Observe(FrameEvidence{Mode: mode, Usable: true})
				}
				if mode, _ := detector.Result(); mode != tc.want {
					t.Fatal("unmatched or conflicting views were fused incorrectly:", mode)
				}
			})
		}
	}
}
