package media

import (
	"math"
	"time"
)

// audioTimeline interpolates the consumed-sample clock between device buffer
// callbacks. A queued-byte count advances in blocks, not at video frame times.
type audioTimeline struct {
	raw, value float64
	sampledAt  time.Time
	starved    bool
}

func (c *audioTimeline) reset(position float64) {
	*c = audioTimeline{raw: math.NaN(), value: position}
}

func (c *audioTimeline) sample(now time.Time, consumed, bufferedEnd, rate float64) float64 {
	if c.sampledAt.IsZero() || consumed != c.raw || c.starved {
		c.raw, c.sampledAt = consumed, now
	}
	position := consumed + now.Sub(c.sampledAt).Seconds()*rate
	// No samples remain after an underrun. Do not extrapolate past audio
	// that has actually been decoded and queued for the output device.
	c.starved = position >= bufferedEnd
	position = min(position, bufferedEnd)
	// Callback observation can vary by a worker tick; keep presentation
	// monotonic until the next reset for a seek, source or playback rate.
	c.value = max(c.value, position)
	return c.value
}
