package tracking

import (
	"github.com/foursmith/player/internal/detection"
	"github.com/foursmith/player/internal/projection"
	"math"
	"sort"
)

type Phase uint8

const (
	Tracking Phase = iota
	ViewportRetry
	PanoramaScan
	RecoveryBackoff
)

type Tile struct{ Yaw, Pitch, FOV float64 }
type Scan struct {
	Phase            Phase
	Misses, Failures int
	Tiles            []Tile
	Index            int
	Refinement       *Tile
	RefinementUsed   bool
	RetryAt          float64
}

func RetryDelay(failed int) float64 {
	if failed <= 0 {
		return 0
	}
	return math.Min(4000, 500*math.Pow(2, float64(failed-1)))
}
func Tiles(mode projection.Mode, yaw, pitch float64, prediction *Tile) []Tile {
	var result []Tile
	if mode.Half() {
		result = []Tile{{0, 0, 100}, {-60, 0, 100}, {60, 0, 100}, {0, 90, 100}, {0, -90, 100}}
	} else if mode == projection.Flat {
		result = []Tile{{yaw, pitch, 100}}
	} else {
		result = []Tile{{projection.Angle(yaw), 0, 100}, {projection.Angle(yaw + 90), 0, 100}, {projection.Angle(yaw + 180), 0, 100}, {projection.Angle(yaw - 90), 0, 100}, {projection.Angle(yaw), 90, 100}, {projection.Angle(yaw), -90, 100}}
	}
	if prediction != nil {
		distance := func(t Tile) float64 {
			a, b := projection.Radians(t.Pitch), projection.Radians(prediction.Pitch)
			return projection.Degrees(math.Acos(projection.Clamp(math.Sin(a)*math.Sin(b)+math.Cos(a)*math.Cos(b)*math.Cos(projection.Radians(projection.Angle(t.Yaw-prediction.Yaw))), -1, 1)))
		}
		sort.SliceStable(result, func(i, j int) bool { return distance(result[i])+0.0001 < distance(result[j]) })
	}
	return result
}
func Reliable(face detection.Face) bool {
	x, y := face.Center()
	return face.Score >= .7 && x >= .18 && x <= .82 && y >= .18 && y <= .82 && face.X >= .08 && face.Y >= .08 && face.X+face.Width <= .92 && face.Y+face.Height <= .92
}
func (s *Scan) viewport(found bool, tiles func() []Tile) bool {
	if found {
		*s = Scan{}
		return false
	}
	s.Misses++
	if s.Phase != ViewportRetry {
		s.Phase = ViewportRetry
		return true
	}
	s.Phase = PanoramaScan
	s.Tiles = tiles()
	s.Index = 0
	s.Refinement = nil
	s.RefinementUsed = false
	return false
}
func (s *Scan) miss(now float64, refinement *Tile) {
	if s.Phase != PanoramaScan {
		return
	}
	s.Misses++
	if refinement != nil && s.Refinement == nil && !s.RefinementUsed {
		s.Refinement = refinement
		s.RefinementUsed = true
		return
	}
	s.Refinement = nil
	if s.Index+1 < len(s.Tiles) {
		s.Index++
		return
	}
	s.Failures++
	s.Phase = RecoveryBackoff
	s.RetryAt = now + RetryDelay(s.Failures)
}
func InferencePeriod(fps, p95 float64, activity string, motion *Motion) float64 {
	hz, headroom := fps, 1.15
	switch activity {
	case "stable":
		hz = 3
	case "active":
		hz, headroom = 6, 1.1
	case "searching":
		hz, headroom = 5, 1.08
	case "recovery":
		hz, headroom = 6, 1.03
	}
	if activity == "stable" || activity == "active" {
		if motion != nil && motion.Size >= .18 && motion.Speed < .08 && motion.Receding < .015 {
			hz = 2
		} else if motion != nil {
			urgency := math.Max(projection.Clamp((motion.Speed-.08)/.42, 0, 1), projection.Clamp(motion.Receding/.15, 0, 1))
			hz += (10 - hz) * urgency
		}
	}
	return math.Max(1000/math.Min(math.Max(1, fps), hz), math.Min(360, p95*headroom))
}
func MovingPeriod(remaining, p95 float64) float64 {
	return math.Max(p95+50, math.Min(800, math.Max(300, remaining/3)))
}

type Request struct {
	View                    projection.View
	Aspect, At, FOV, Period float64
	Panorama, FullRange     bool
	Tile                    Tile
	Activity                string
	Epoch                   uint64
}
type Tracker struct {
	State                 State
	Scan                  Scan
	Busy, Rescan          bool
	NextAt, LastInference float64
	Activity              string
	Epoch                 uint64
	recent                []float64
}

func (t *Tracker) Reset() { epoch := t.Epoch + 1; *t = Tracker{Epoch: epoch} }
func (t *Tracker) P95() float64 {
	if len(t.recent) == 0 {
		return 0
	}
	values := append([]float64(nil), t.recent...)
	sort.Float64s(values)
	return values[max(0, int(math.Ceil(float64(len(values))*.95))-1)]
}
func (t *Tracker) schedule(now, completed, fps float64, mode projection.Mode, view projection.View, aspect float64, override *Request) {
	plan := Plan{}
	target := t.State.Target
	if target != nil {
		plan = CenteringPlan(*target, view, mode, aspect, t.State.Moving)
	}
	needs := plan.Offset.Yaw != 0 || plan.Offset.Pitch != 0 || plan.Offset.Forward != 0
	blocked := plan.Blocked && !needs
	if override != nil && !blocked {
		t.Activity = override.Activity
	} else if t.Scan.Phase == PanoramaScan {
		t.Activity = "recovery"
	} else if t.Scan.Phase != Tracking || target == nil {
		t.Activity = "searching"
	} else if t.State.Moving || !blocked && (t.State.OffCenter != nil || needs) {
		t.Activity = "active"
	} else {
		t.Activity = "stable"
	}
	motion := t.State.Motion
	if motion != nil && now-motion.Seen >= 1500 {
		motion = nil
	}
	period := InferencePeriod(fps, t.P95(), t.Activity, motion)
	if override != nil && !blocked {
		period = override.Period
	} else if t.State.Moving && t.Rescan && target != nil {
		period = MovingPeriod(EstimateDuration(plan.Offset, t.State.Velocity, target.Panorama), t.P95())
	}
	t.NextAt = math.Max(now+math.Max(0, period-completed), t.Scan.RetryAt)
}
func (t *Tracker) Begin(now, fps float64, mode projection.Mode, view projection.View, aspect float64) (Request, bool) {
	if t.Busy || now < t.NextAt || t.State.Moving && !t.Rescan {
		return Request{}, false
	}
	if t.Scan.Phase == RecoveryBackoff {
		if now < t.Scan.RetryAt {
			return Request{}, false
		}
		t.Scan.Phase = Tracking
		t.Scan.RetryAt = 0
	}
	request := Request{View: view, Aspect: aspect, At: now, FOV: 80, FullRange: t.Scan.Phase == ViewportRetry || t.Scan.Phase == PanoramaScan, Epoch: t.Epoch}
	if t.Scan.Phase == PanoramaScan {
		request.Panorama = true
		tile := t.Scan.Tiles[t.Scan.Index]
		if t.Scan.Refinement != nil {
			tile = *t.Scan.Refinement
		}
		request.Tile = tile
		request.FOV = tile.FOV
		request.Aspect = 1
		request.View = projection.View{Yaw: tile.Yaw, Pitch: tile.Pitch, Zoom: 1}
	}
	t.schedule(now, 0, fps, mode, view, aspect, nil)
	request.Period = t.NextAt - now
	request.Activity = t.Activity
	t.Busy = true
	return request, true
}
func (t *Tracker) Apply(request Request, faces []detection.Face, completed, latency, fps float64, mode projection.Mode, view projection.View, aspect float64) {
	if request.Epoch != t.Epoch {
		return
	}
	t.Busy = false
	t.LastInference = latency
	t.recent = append(t.recent, latency)
	if len(t.recent) > 20 {
		t.recent = t.recent[1:]
	}
	raw := make([]Face, len(faces))
	mapped := make([]Face, len(faces))
	for i, f := range faces {
		raw[i] = Face{f, request.At, request.Panorama}
		mapped[i] = raw[i]
		if request.Panorama {
			mapped[i] = mapFace(raw[i], request.Tile, mode)
		}
	}
	t.State.Faces = raw
	var anchor *[2]float64
	if request.Panorama {
		span := 360.0
		if mode.Half() {
			span = 180
		}
		x := .5 - request.Tile.Yaw/span
		if mode.Half() {
			x = projection.Clamp(x, 0, 1)
		} else {
			x = math.Mod(math.Mod(x, 1)+1, 1)
		}
		anchor = &[2]float64{x, projection.Clamp(.5-request.Tile.Pitch/180, 0, 1)}
	}
	face := selectFace(&t.State, mapped, request.Panorama, request.At, anchor, !mode.Half())
	preserve := false
	found := false
	if request.Panorama {
		var sample *Face
		if face != nil {
			for i, f := range mapped {
				if f == *face {
					sample = &raw[i]
					break
				}
			}
		}
		if face != nil && sample != nil && Reliable(sample.Face) {
			panoramaTarget(&t.State, *face, mode, view.Zoom, request.At)
			t.Scan = Scan{}
			found = true
		} else {
			var refinement *Tile
			if face != nil {
				x, y := face.Center()
				span := 360.0
				if mode.Half() {
					span = 180
				}
				yaw := projection.Angle((.5 - x) * span)
				if mode.Half() {
					yaw = projection.Clamp(yaw, -86, 86)
				}
				refinement = &Tile{yaw, projection.Clamp((.5-y)*180, -85, 85), 70}
			}
			t.Scan.miss(completed, refinement)
		}
	} else {
		if face != nil {
			updateMotion(&t.State, *face, request.View, request.Aspect, request.At)
			viewportTarget(&t.State, *face, request.View, request.Aspect, request.At, mode)
			found = true
		}
		preserve = t.Scan.viewport(found, func() []Tile {
			return Tiles(mode, request.View.Yaw, request.View.Pitch, predicted(&t.State, request.At, mode))
		})
	}
	if !found {
		if t.State.Target != nil && request.At-t.State.Target.Seen > 900 {
			t.State.Target = nil
		}
		if t.Scan.Misses >= 3 && t.State.Target == nil {
			t.State.Selected = nil
		}
	}
	var override *Request
	if preserve && !t.Rescan {
		override = &request
	}
	t.schedule(completed, latency, fps, mode, view, aspect, override)
}
func (t *Tracker) Advance(view *projection.View, mode projection.Mode, aspect, now, delta float64) Movement {
	result := Advance(&t.State, view, mode, aspect, now, delta)
	if result.Started {
		t.Rescan = result.Duration > 3500
		if t.Rescan {
			t.NextAt = now + MovingPeriod(result.Duration, t.LastInference)
		}
	}
	if result.Stopped {
		t.Rescan = false
		t.NextAt = 0
	}
	return result
}
