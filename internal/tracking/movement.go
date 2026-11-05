package tracking

import (
	"github.com/foursmith/vr/app/internal/projection"
	"math"
)

type Plan struct {
	Offset               projection.View
	Desired, Constrained projection.View
	Blocked              bool
}

func subtractZone(value, zone float64) float64 {
	return math.Copysign(math.Max(0, math.Abs(value)-zone), value)
}
func CenteringPlan(target Target, view projection.View, mode projection.Mode, aspect float64, moving bool) Plan {
	viewportThreshold, panoramaThreshold, forwardThreshold := .08, 10.0, 3.0
	if moving {
		viewportThreshold, panoramaThreshold, forwardThreshold = .05, 7, 1.5
	}
	yawZone, pitchZone := panoramaThreshold, panoramaThreshold
	if !target.Panorama {
		yawZone = math.Abs(yawOffset(.5+viewportThreshold, aspect, view.Zoom) - yawOffset(.5, aspect, view.Zoom))
		pitchZone = math.Abs(pitchOffset(1.0/3+viewportThreshold, view.Zoom) - pitchOffset(1.0/3, view.Zoom))
	}
	dy := subtractZone(projection.Angle(target.Yaw-view.Yaw), yawZone)
	dp := subtractZone(target.Pitch-view.Pitch, pitchZone)
	df := 0.0
	if target.HasForward && math.Abs(target.Size-.18) >= .02-1e-9 {
		df = subtractZone(target.Forward-view.Forward, forwardThreshold)
	}
	desired := view
	desired.Yaw += dy
	desired.Pitch = projection.Clamp(desired.Pitch+dp, -85, 85)
	desired.Forward += df
	if target.Panorama && (dy != 0 || dp != 0) && df == 0 && projection.Coverage(mode, desired, aspect) < 0 {
		candidate := desired
		candidate.Forward = 35
		if projection.Coverage(mode, candidate, aspect) < 0 {
			desired.Forward = 35
		} else {
			blocked, safe := view.Forward, 35.0
			for range 12 {
				candidate.Forward = (blocked + safe) / 2
				if projection.Coverage(mode, candidate, aspect) >= 0 {
					safe = candidate.Forward
				} else {
					blocked = candidate.Forward
				}
			}
			desired.Forward = safe
		}
	}
	constrained := projection.Constrain(mode, view, desired, aspect)
	offset := projection.View{Yaw: projection.Angle(constrained.Yaw - view.Yaw), Pitch: constrained.Pitch - view.Pitch, Forward: constrained.Forward - view.Forward}
	if math.Abs(offset.Yaw) < .001 {
		offset.Yaw = 0
	}
	if math.Abs(offset.Pitch) < .001 {
		offset.Pitch = 0
	}
	if math.Abs(offset.Forward) < .001 {
		offset.Forward = 0
	}
	return Plan{offset, desired, constrained, math.Abs(projection.Angle(desired.Yaw-constrained.Yaw)) > .001 || math.Abs(desired.Pitch-constrained.Pitch) > .001 || math.Abs(desired.Forward-constrained.Forward) > .001}
}
func velocity(offset float64, panorama, forward bool) float64 {
	speed, scale := 18.0, 22.0
	if panorama {
		speed, scale = 32, 45
	}
	if forward {
		speed, scale = 16, 18
	}
	return math.Copysign(speed*(1-math.Exp(-math.Abs(offset)/scale)), offset)
}
func smoothVelocity(current, desired, elapsed float64) float64 {
	next := current + (desired-current)*(1-math.Exp(-elapsed/260))
	if math.Abs(next) < .025 && math.Abs(desired) < .025 {
		return 0
	}
	return next
}
func EstimateDuration(offset, vel projection.View, panorama bool) float64 {
	o := [3]float64{offset.Yaw, offset.Pitch, offset.Forward}
	v := [3]float64{vel.Yaw, vel.Pitch, vel.Forward}
	settle := [3]float64{}
	for i := range o {
		settle[i] = math.Max(.01, math.Abs(o[i])*.05)
	}
	for elapsed := 0.0; elapsed <= 15000; elapsed += 16 {
		if o[0] == 0 && o[1] == 0 && o[2] == 0 {
			return elapsed
		}
		for i := range o {
			if math.Abs(o[i]) <= settle[i] {
				o[i], v[i] = 0, 0
				continue
			}
			v[i] = smoothVelocity(v[i], velocity(o[i], panorama, i == 2), 16)
			next := o[i] - v[i]*.016
			if o[i]*next <= 0 {
				o[i], v[i] = 0, 0
			} else {
				o[i] = next
			}
		}
	}
	return math.Inf(1)
}

type Movement struct {
	Started, Stopped bool
	Duration         float64
	Plan             Plan
}

func Advance(state *State, view *projection.View, mode projection.Mode, aspect, now, delta float64) Movement {
	wasMoving := state.Moving
	dt := projection.Clamp(delta, 1.0/240, .05)
	target := state.Target
	maxAge := 1100.0
	if wasMoving {
		maxAge = 4500
	}
	var plan Plan
	duration := 0.0
	if target == nil || now-target.Seen > maxAge {
		state.Velocity.Yaw = smoothVelocity(state.Velocity.Yaw, 0, dt*1000)
		state.Velocity.Pitch = smoothVelocity(state.Velocity.Pitch, 0, dt*1000)
		state.Velocity.Forward = smoothVelocity(state.Velocity.Forward, 0, dt*1000)
	} else {
		plan = CenteringPlan(*target, *view, mode, aspect, wasMoving)
		o := plan.Offset
		if o.Yaw == 0 && o.Pitch == 0 && o.Forward == 0 {
			state.OffCenter = nil
		} else if state.OffCenter == nil {
			copy := now
			state.OffCenter = &copy
		}
		if !wasMoving {
			duration = EstimateDuration(o, state.Velocity, target.Panorama)
		}
		values := [3]float64{o.Yaw, o.Pitch, o.Forward}
		v := [3]*float64{&state.Velocity.Yaw, &state.Velocity.Pitch, &state.Velocity.Forward}
		for i := range values {
			if values[i] == 0 {
				*v[i] = 0
			} else {
				*v[i] = smoothVelocity(*v[i], velocity(values[i], target.Panorama, i == 2), dt*1000)
			}
		}
	}
	next := *view
	next.Yaw += state.Velocity.Yaw * dt
	next.Pitch = projection.Clamp(next.Pitch+state.Velocity.Pitch*dt, -85, 85)
	next.Forward = math.Min(next.Forward+state.Velocity.Forward*dt, 35)
	constrained := projection.Constrain(mode, *view, next, aspect)
	if math.Abs(constrained.Yaw-next.Yaw) > .0001 {
		state.Velocity.Yaw = 0
	}
	if math.Abs(constrained.Pitch-next.Pitch) > .0001 {
		state.Velocity.Pitch = 0
	}
	if math.Abs(constrained.Forward-next.Forward) > .0001 {
		state.Velocity.Forward = 0
	}
	*view = constrained
	state.Moving = state.Velocity.Yaw != 0 || state.Velocity.Pitch != 0 || state.Velocity.Forward != 0
	return Movement{!wasMoving && state.Moving, wasMoving && !state.Moving, duration, plan}
}
