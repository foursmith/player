// Package tracking ports the portrait-centering rules documented in
// doc/PORTRAIT_CENTERING.md. It has no rendering, UI, or inference side effects.
package tracking

import (
	"github.com/foursmith/player/internal/detection"
	"github.com/foursmith/player/internal/projection"
	"math"
)

type Face struct {
	detection.Face
	Seen     float64
	Panorama bool
}
type Target struct {
	X, Y, Size, Yaw, Pitch, Forward, Seen float64
	Panorama, HasForward                  bool
}
type Motion struct {
	X, Y, Size, Speed, Receding, Yaw, Pitch, YawVelocity, PitchVelocity, Seen float64
	Samples                                                                   int
}
type State struct {
	Faces     []Face
	Selected  *Face
	Target    *Target
	Motion    *Motion
	Velocity  projection.View
	Moving    bool
	OffCenter *float64
}

func yawOffset(x, aspect, zoom float64) float64 {
	return projection.Degrees(math.Atan((1 - x*2) * math.Tan(projection.Radians(80)/2) / zoom * aspect))
}
func pitchOffset(y, zoom float64) float64 {
	return projection.Degrees(math.Atan((1 - y*2) * math.Tan(projection.Radians(80)/2) / zoom))
}
func distance(a, b detection.Face, wrap bool) float64 {
	ax, ay := a.Center()
	bx, by := b.Center()
	dx := math.Abs(ax - bx)
	if wrap {
		dx = math.Min(dx, 1-dx)
	}
	return math.Hypot(dx, ay-by)
}
func selectFace(state *State, faces []Face, panorama bool, now float64, anchor *[2]float64, wrap bool) *Face {
	bestScore := math.Inf(-1)
	var best *Face
	for _, face := range faces {
		if face.Score < .6 {
			continue
		}
		score := face.Score*1.2 + face.Width*face.Height*2.4
		if previous := state.Selected; previous != nil && now-previous.Seen < 2400 && previous.Panorama == panorama {
			score += math.Max(0, 1-distance(face.Face, previous.Face, panorama)/.32) * 1.6
		}
		if anchor != nil {
			x, y := face.Center()
			dx := math.Abs(x - anchor[0])
			if wrap {
				dx = math.Min(dx, 1-dx)
			}
			score += math.Max(0, 1-math.Hypot(dx, y-anchor[1])/.42) * 1.35
		}
		if score > bestScore {
			copy := face
			best = &copy
			bestScore = score
		}
	}
	if best != nil {
		if previous := state.Selected; previous != nil && previous.Panorama == panorama {
			elapsed := (now - previous.Seen) / 1000
			if elapsed > 0 && elapsed <= 1.5 && previous.Size() > 0 && best.Size() > 0 && distance(best.Face, previous.Face, panorama)/elapsed >= .8 && math.Abs(math.Log(best.Size()/previous.Size()))/elapsed >= 1.2 {
				state.Target = nil
				state.Motion = nil
				state.Velocity = projection.View{}
				state.Moving = false
				state.OffCenter = nil
			}
		}
		state.Selected = best
	}
	return best
}
func updateMotion(state *State, face Face, view projection.View, aspect, now float64) {
	x, y := face.Center()
	size := face.Size()
	yaw := projection.Angle(view.Yaw + yawOffset(x, aspect, view.Zoom))
	pitch := projection.Clamp(view.Pitch+pitchOffset(y, view.Zoom), -90, 90)
	previous := state.Motion
	next := &Motion{X: x, Y: y, Size: size, Yaw: yaw, Pitch: pitch, Seen: now, Samples: 1}
	if previous != nil {
		elapsed := now - previous.Seen
		if elapsed > 0 && elapsed <= 1500 {
			dt := elapsed / 1000
			blend := 1 - math.Exp(-elapsed/350)
			next.Speed = previous.Speed + (projection.Clamp(math.Hypot(x-previous.X, y-previous.Y)/dt, 0, 4)-previous.Speed)*blend
			next.Receding = previous.Receding + (projection.Clamp((previous.Size-size)/dt, -2, 2)-previous.Receding)*blend
			next.YawVelocity = previous.YawVelocity + (projection.Clamp(projection.Angle(yaw-previous.Yaw)/dt, -180, 180)-previous.YawVelocity)*blend
			next.PitchVelocity = previous.PitchVelocity + (projection.Clamp((pitch-previous.Pitch)/dt, -120, 120)-previous.PitchVelocity)*blend
			next.Samples = previous.Samples + 1
		}
	}
	state.Motion = next
}
func predicted(state *State, now float64, mode projection.Mode) *Tile {
	m := state.Motion
	if m == nil || m.Samples < 2 || now-m.Seen < 0 || now-m.Seen > 900 {
		return nil
	}
	seconds := math.Min(600, now-m.Seen+160) / 1000
	yaw := projection.Angle(m.Yaw + projection.Clamp(m.YawVelocity*seconds, -45, 45))
	if mode.Half() {
		yaw = projection.Clamp(yaw, -86, 86)
	}
	return &Tile{Yaw: yaw, Pitch: projection.Clamp(m.Pitch+projection.Clamp(m.PitchVelocity*seconds, -30, 30), -85, 85)}
}
func forwardTarget(face detection.Face, current float64, mode projection.Mode) float64 {
	size := face.Size()
	if size == 0 || math.Abs(size-.18) < .02-1e-9 {
		return current
	}
	surface := 100.0
	if mode == projection.Flat {
		surface = 65
	}
	return math.Min(surface-math.Max(1, surface-current)*size/.18, 35)
}
func smoothTarget(state *State, next Target) {
	old := state.Target
	if old == nil || old.Panorama != next.Panorama || next.Seen-old.Seen > 1800 {
		state.Target = &next
		return
	}
	blend := 1 - math.Exp(-math.Max(0, next.Seen-old.Seen)/480)
	next.X = old.X + (next.X-old.X)*blend
	next.Y = old.Y + (next.Y-old.Y)*blend
	next.Yaw = old.Yaw + projection.Angle(next.Yaw-old.Yaw)*blend
	next.Pitch = old.Pitch + (next.Pitch-old.Pitch)*blend
	if old.HasForward && next.HasForward && math.Abs(next.Size-.18) >= .02-1e-9 {
		next.Forward = old.Forward + (next.Forward-old.Forward)*blend
	}
	state.Target = &next
}
func viewportTarget(state *State, face Face, view projection.View, aspect, now float64, mode projection.Mode) {
	x, y := face.Center()
	smoothTarget(state, Target{X: x - .5, Y: y - 1.0/3, Size: face.Size(), Yaw: view.Yaw + yawOffset(x, aspect, view.Zoom), Pitch: projection.Clamp(view.Pitch+pitchOffset(y, view.Zoom)-pitchOffset(1.0/3, view.Zoom), -85, 85), Forward: forwardTarget(face.Face, view.Forward, mode), HasForward: true, Seen: now})
}
func panoramaTarget(state *State, face Face, mode projection.Mode, zoom, now float64) {
	x, y := face.Center()
	span := 360.0
	if mode.Half() {
		span = 180
	}
	yaw := (.5 - x) * span
	if mode.Half() {
		yaw = projection.Clamp(yaw, -86, 86)
	}
	smoothTarget(state, Target{X: x - .5, Y: y - 1.0/3, Yaw: yaw, Pitch: projection.Clamp((.5-y)*180-pitchOffset(1.0/3, zoom), -75, 75), Panorama: true, Seen: now})
}
func mapFace(face Face, tile Tile, mode projection.Mode) Face {
	x, y := face.Center()
	tan := math.Tan(projection.Radians(tile.FOV) / 2)
	direction := projection.Rotate(projection.Vec3{X: (2*x - 1) * tan, Y: (1 - 2*y) * tan, Z: -1}.Unit(), tile.Yaw, tile.Pitch)
	yaw := projection.Degrees(math.Atan2(-direction.X, -direction.Z))
	pitch := projection.Degrees(math.Asin(projection.Clamp(direction.Y, -1, 1)))
	span := 360.0
	if mode.Half() {
		span = 180
	}
	width := projection.Clamp(projection.Degrees(2*math.Atan(face.Width*tan))/span, 0, 1)
	height := projection.Clamp(projection.Degrees(2*math.Atan(face.Height*tan))/180, 0, 1)
	cx := .5 - yaw/span
	if mode.Half() {
		cx = projection.Clamp(cx, 0, 1)
	} else {
		cx = math.Mod(math.Mod(cx, 1)+1, 1)
	}
	cy := projection.Clamp(.5-pitch/180, 0, 1)
	face.X = cx - width/2
	if mode.Half() {
		face.X = projection.Clamp(face.X, 0, 1-width)
	}
	face.Y = projection.Clamp(cy-height/2, 0, 1-height)
	face.Width = width
	face.Height = height
	return face
}
