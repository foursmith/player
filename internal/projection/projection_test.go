package projection

import (
	"math"
	"testing"
)

func TestProjectionCenterAndStereoCrop(t *testing.T) {
	for _, tc := range []struct {
		mode Mode
		u, v float64
	}{{SBS180, .25, .5}, {SBSFisheye, .25, .5}, {Mono360, .5, .5}, {Mono180, .5, .5}, {MonoFisheye, .5, .5}, {TB360, .5, .25}} {
		u, v, ok := UV(tc.mode, Vec3{}, Vec3{Z: -1}, 2)
		if !ok || math.Abs(u-tc.u) > 1e-8 || math.Abs(v-tc.v) > 1e-8 {
			t.Errorf("%v: %v %v %v", tc.mode, u, v, ok)
		}
	}
}
func TestForwardUsesSphereIntersection(t *testing.T) {
	o, r := Ray(.8, .4, 1, 80, View{Zoom: 1, Forward: 35})
	u, _, _ := UV(Mono360, o, r, 2)
	u0, _, _ := UV(Mono360, Vec3{}, r, 2)
	if math.Abs(u-u0) < .001 {
		t.Fatal("camera translation must change projection")
	}
}
func TestHalfCoverageStopsAtEdge(t *testing.T) {
	v := DefaultView()
	next := v
	next.Yaw = 100
	got := Constrain(Mono180, v, next, 1)
	if got.Yaw >= 90 || Coverage(Mono180, got, 1) < -0.05 {
		t.Fatalf("uncovered view: %+v", got)
	}
}
func TestSplitLayout(t *testing.T) {
	if got := len(Viewports(1920, 1080, 3, false)); got != 2 {
		t.Fatalf("got %d", got)
	}
	if got := len(Viewports(1920, 1080, 3, true)); got != 3 {
		t.Fatalf("got %d", got)
	}
	if len(Viewports(600, 1000, 3, true)) != 1 {
		t.Fatal("portrait must stay single")
	}
}
