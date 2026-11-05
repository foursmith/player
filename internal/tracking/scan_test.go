package tracking

import (
	"github.com/foursmith/player/internal/detection"
	"github.com/foursmith/player/internal/projection"
	"math"
	"testing"
)

func TestRecoveryStateMachine(t *testing.T) {
	scan := Scan{}
	factory := func() []Tile { return Tiles(projection.Mono360, 0, 0, nil) }
	if !scan.viewport(false, factory) || scan.Phase != ViewportRetry {
		t.Fatal("first miss must preserve viewport schedule")
	}
	if scan.viewport(false, factory) || scan.Phase != PanoramaScan || len(scan.Tiles) != 6 {
		t.Fatal("full-range miss must begin recovery")
	}
	refinement := Tile{5, 6, 70}
	scan.miss(100, &refinement)
	if scan.Refinement == nil || !scan.RefinementUsed {
		t.Fatal("first weak candidate needs refinement")
	}
	scan.miss(200, &refinement)
	if scan.Refinement != nil || scan.Index != 1 {
		t.Fatal("failed refinement must advance coarse tile")
	}
	for scan.Phase == PanoramaScan {
		scan.miss(500, nil)
	}
	if scan.Phase != RecoveryBackoff || scan.RetryAt != 1000 {
		t.Fatalf("backoff starts at completion: %+v", scan)
	}
}
func TestRecoveryBoundsAndPrediction(t *testing.T) {
	if len(Tiles(projection.Mono180, 80, 0, nil)) != 5 {
		t.Fatal("half sphere must use five tiles")
	}
	tiles := Tiles(projection.Mono360, 0, 0, &Tile{Yaw: 90})
	if tiles[0].Yaw != 90 {
		t.Fatalf("wrong prioritized tile: %+v", tiles[0])
	}
	if RetryDelay(1) != 500 || RetryDelay(5) != 4000 {
		t.Fatal("unexpected backoff")
	}
	state := State{Motion: &Motion{Yaw: 179, Pitch: 80, YawVelocity: 100, PitchVelocity: 100, Seen: 100, Samples: 2}}
	guess := predicted(&state, 300, projection.Mono360)
	if guess == nil || guess.Yaw >= 0 || guess.Pitch != 85 {
		t.Fatalf("prediction must wrap and clamp: %+v", guess)
	}
	if predicted(&state, 1001, projection.Mono360) != nil {
		t.Fatal("old motion must not prioritize recovery")
	}
}
func TestInferenceScheduling(t *testing.T) {
	close := &Motion{Size: .2, Speed: .01, Receding: 0}
	if InferencePeriod(60, 20, "stable", close) != 500 {
		t.Fatal("close slow subject should use 2 Hz")
	}
	fast := &Motion{Size: .1, Speed: .5}
	if math.Abs(InferencePeriod(60, 20, "active", fast)-100) > 1e-8 {
		t.Fatal("urgent movement should reach 10 Hz")
	}
	if MovingPeriod(2000, 20) != 2000.0/3 {
		t.Fatal("long move rescan period")
	}
}
func TestReliabilityAndPerspectiveMapping(t *testing.T) {
	face := detection.Face{X: .4, Y: .4, Width: .2, Height: .2, Score: .9}
	if !Reliable(face) {
		t.Fatal("centered face rejected")
	}
	face.X = 0
	if Reliable(face) {
		t.Fatal("edge face accepted")
	}
	mapped := mapFace(Face{Face: detection.Face{X: .4, Y: .4, Width: .2, Height: .2, Score: .9}}, Tile{90, 0, 100}, projection.Mono360)
	x, y := mapped.Center()
	if math.Abs(x-.25) > 1e-8 || math.Abs(y-.5) > 1e-8 {
		t.Fatalf("wrong world coordinate: %v %v", x, y)
	}
}
func TestResetDiscardsOutstandingResult(t *testing.T) {
	tracker := Tracker{}
	request, ok := tracker.Begin(100, 60, projection.Mono360, projection.DefaultView(), 1)
	if !ok {
		t.Fatal("initial detection not due")
	}
	tracker.Reset()
	tracker.Apply(request, []detection.Face{{X: .4, Y: .3, Width: .2, Height: .2, Score: .9}}, 200, 100, 60, projection.Mono360, projection.DefaultView(), 1)
	if tracker.State.Target != nil || tracker.Busy {
		t.Fatal("outdated result changed new media session")
	}
}

func TestResetStopsOldCenteringWithoutWaitingForInference(t *testing.T) {
	tracker := Tracker{State: State{
		Target:   &Target{Yaw: 50, Pitch: 20, Seen: 100},
		Velocity: projection.View{Yaw: 12, Pitch: 4},
		Moving:   true,
	}}
	old := Request{Epoch: tracker.Epoch}
	tracker.Reset()
	view := projection.DefaultView()
	tracker.Advance(&view, projection.Mono360, 1, 200, 1.0/60)
	if view != projection.DefaultView() || tracker.State.Moving {
		t.Fatal("previous centering kept moving the camera after reset")
	}
	if _, ok := tracker.Begin(200, 60, projection.Mono360, view, 1); !ok {
		t.Fatal("fresh frame must not wait for the old inference or movement")
	}
	tracker.Apply(old, []detection.Face{{X: .4, Y: .3, Width: .2, Height: .2, Score: .9}}, 250, 50, 60, projection.Mono360, view, 1)
	if tracker.State.Target != nil || !tracker.Busy {
		t.Fatal("old inference changed the new request or centering target")
	}
}
