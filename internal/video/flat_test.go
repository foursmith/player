package video

import (
	"math"
	"testing"
)

func TestFlatFitsWholeImageWithoutDistortion(t *testing.T) {
	for _, tc := range []struct{ aspect, source, left, top float64 }{
		{1, 2, 0, .25},
		{4, 2, .25, 0},
		{.5, 2, 0, .375},
		{2, .5, .375, 0},
		{2, 2, 0, 0},
	} {
		v := FlatView{}
		for _, point := range [][4]float64{
			{.5, .5, .5, .5},
			{tc.left, tc.top, 0, 0},
			{1 - tc.left, 1 - tc.top, 1, 1},
		} {
			u, w, visible := v.UV(point[0], point[1], tc.aspect, tc.source)
			if !visible || math.Abs(u-point[2]) > 1e-8 || math.Abs(w-point[3]) > 1e-8 {
				t.Fatalf("%+v point %v: got %v %v %v", tc, point, u, w, visible)
			}
		}
		if tc.left > 0 {
			if _, _, visible := v.UV(0, .5, tc.aspect, tc.source); visible {
				t.Fatal("pillarbox must stay black")
			}
		}
		if tc.top > 0 {
			if _, _, visible := v.UV(.5, 0, tc.aspect, tc.source); visible {
				t.Fatal("letterbox must stay black")
			}
		}
	}
}

func TestFlatPanAndZoomStayWithinImage(t *testing.T) {
	v := FlatView{}.Pan(1, 1, 2, 2)
	if v != (FlatView{Zoom: 1}) {
		t.Fatal("an image that fits must not pan", v)
	}
	v = v.Scale(2, 2, 2).Pan(1, -1, 2, 2)
	if v != (FlatView{X: .25, Y: -.25, Zoom: 2}) {
		t.Fatal("zoomed pan did not stop at the image boundary", v)
	}
	v = v.Scale(.01, 2, 2)
	if v != (FlatView{Zoom: 1}) {
		t.Fatal("zooming out must restore the complete image", v)
	}
	v = v.Scale(1000, 2, 2)
	if v.Zoom != 10 {
		t.Fatal("unbounded zoom", v)
	}
	// A wide image still has vertical bars until zoom fills that axis.
	v = FlatView{}.Scale(2, .5, 2).Pan(0, 1, .5, 2)
	if v.Y != 0 {
		t.Fatal("panning introduced extra empty space", v)
	}
}
