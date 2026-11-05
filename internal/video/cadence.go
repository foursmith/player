package video

import "math"

// ScheduleFrame keeps playback cadence stable while interactions render at once.
func ScheduleFrame(now, fps, next float64, interaction bool) (bool, float64) {
	if interaction {
		return true, next
	}
	interval := 1000 / math.Max(1, fps)
	if next == 0 || math.IsNaN(next) || math.IsInf(next, 0) {
		return true, now + interval
	}
	if now < next-.5 {
		return false, next
	}
	following := next + interval
	if following <= now {
		following += (math.Floor((now-following)/interval) + 1) * interval
	}
	return true, following
}
func PixelRatio(quality int, scale float64) float64 {
	ratios := [...]float64{.6, .8, 1, 1.1}
	return math.Min(4, math.Max(.5, scale*ratios[max(0, min(3, quality))]))
}
