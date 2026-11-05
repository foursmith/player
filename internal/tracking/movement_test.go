package tracking

import (
	"github.com/foursmith/player/internal/detection"
	"github.com/foursmith/player/internal/projection"
	"math"
	"testing"
)

func TestViewportTargetUsesCapturePose(t *testing.T) {
	state := State{}
	capture := projection.View{Yaw: 30, Pitch: 10, Forward: 5, Zoom: 1}
	face := Face{Face: detection.Face{X: .6, Y: .3, Width: .2, Height: .2, Score: .9}, Seen: 100}
	viewportTarget(&state, face, capture, 1, 100, projection.Mono360)
	wantYaw := 30 + yawOffset(.7, 1, 1)
	if math.Abs(state.Target.Yaw-wantYaw) > 1e-8 {
		t.Fatalf("target must use captured camera: %+v", state.Target)
	}
	if state.Target.Forward >= capture.Forward {
		t.Fatal("a face larger than .18 must move camera backward")
	}
}
func TestDeadZoneClearsResidualVelocity(t *testing.T) {
	view := projection.DefaultView()
	state := State{Target: &Target{Yaw: 0, Pitch: 0, Size: .18, Forward: 0, HasForward: true, Seen: 100}, Velocity: projection.View{Yaw: 2, Pitch: 1, Forward: 3}, Moving: true}
	Advance(&state, &view, projection.Mono360, 1, 150, 1.0/60)
	if state.Moving || state.Velocity.Yaw != 0 || state.Velocity.Pitch != 0 || state.Velocity.Forward != 0 {
		t.Fatalf("residual drift: %+v", state)
	}
}
func TestSizeDeadZoneAndForwardLimit(t *testing.T) {
	for _, size := range []float64{.161, .18, .199} {
		if target := forwardTarget(detection.Face{Width: size, Height: size}, 10, projection.Mono360); target != 10 {
			t.Fatalf("size %v moved camera to %v", size, target)
		}
	}
	if v := forwardTarget(detection.Face{Width: .05, Height: .05}, 0, projection.Mono360); v != 35 {
		t.Fatalf("forward cap: %v", v)
	}
	if v := forwardTarget(detection.Face{Width: .8, Height: .8}, 0, projection.Mono360); v >= 0 {
		t.Fatal("backward movement must remain possible")
	}
}
func TestTargetSmoothingWrapsSeam(t *testing.T) {
	state := State{Target: &Target{Yaw: 179, Seen: 100, Panorama: true}}
	smoothTarget(&state, Target{Yaw: -179, Seen: 580, Panorama: true})
	if state.Target.Yaw < 179 || state.Target.Yaw > 181 {
		t.Fatalf("long turn around seam: %v", state.Target.Yaw)
	}
}
func TestMovementTimeIndependent(t *testing.T) {
	run := func(step float64) projection.View {
		view := projection.DefaultView()
		state := State{Target: &Target{Yaw: 25, Pitch: 8, Seen: 0}}
		for now := 0.0; now < 1000; now += step * 1000 {
			Advance(&state, &view, projection.Mono360, 1, now, step)
		}
		return view
	}
	a, b := run(1.0/30), run(1.0/120)
	if math.Abs(a.Yaw-b.Yaw) > 1 || math.Abs(a.Pitch-b.Pitch) > .5 {
		t.Fatalf("cadence changed motion: %+v %+v", a, b)
	}
}
