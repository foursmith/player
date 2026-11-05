package media

import (
	"math"
	"testing"
	"time"
)

func TestAudioTimelineSmoothsDeviceBufferCallbacks(t *testing.T) {
	for _, rate := range []float64{.5, 1, 1.5, 2} {
		var clock audioTimeline
		clock.reset(0)
		start := time.Unix(0, 0)
		quantum := 1024.0 / 48000
		previous, nextFrame, lastFrameAt := 0.0, 1.0/30, 0
		maxFrameGap, frames := 0, 0
		for ms := range 2001 {
			seconds := float64(ms) / 1000
			consumed := math.Floor(seconds/quantum) * quantum * rate
			position := clock.sample(start.Add(time.Duration(ms)*time.Millisecond), consumed, consumed+.25*rate, rate)
			if position < previous || math.Abs(position-seconds*rate) > .0011*rate {
				t.Fatalf("rate %.1f at %dms: clock %.6f, previous %.6f, wall %.6f", rate, ms, position, previous, seconds*rate)
			}
			previous = position
			if position >= nextFrame {
				maxFrameGap = max(maxFrameGap, ms-lastFrameAt)
				lastFrameAt = ms
				nextFrame += 1.0 / 30
				frames++
			}
		}
		// A 30fps video must not inherit the device's alternating 21/42ms
		// consumption steps. Allow one millisecond for callback observation.
		if maxFrameGap > int(math.Ceil(1000/(30*rate)))+1 || math.Abs(float64(frames)-60*rate) > 1 {
			t.Fatalf("rate %.1f: %d frames, largest gap %dms", rate, frames, maxFrameGap)
		}
	}
}

func TestAudioTimelineStopsAtBufferedEndAndRecoversWithoutJumping(t *testing.T) {
	var clock audioTimeline
	clock.reset(10)
	start := time.Unix(0, 0)
	clock.sample(start, 10, 10.015, 1)
	if got := clock.sample(start.Add(time.Second), 10.015, 10.015, 1); got != 10.015 {
		t.Fatal("clock passed the end of available audio:", got)
	}
	if got := clock.sample(start.Add(5*time.Second), 10.015, 10.015, 1); got != 10.015 {
		t.Fatal("empty audio advanced the video:", got)
	}
	if got := clock.sample(start.Add(6*time.Second), 10.015, 10.5, 1); got != 10.015 {
		t.Fatal("refilling audio skipped over the starved interval:", got)
	}
	if got := clock.sample(start.Add(6*time.Second+5*time.Millisecond), 10.015, 10.5, 1); math.Abs(got-10.020) > .000001 {
		t.Fatal("refilled audio failed to advance smoothly:", got)
	}
}

func TestAudioTimelineStaysMonotonicAndResetsForSeekAndResume(t *testing.T) {
	var clock audioTimeline
	clock.reset(0)
	start := time.Unix(0, 0)
	clock.sample(start, 0, 1, 1)
	before := clock.sample(start.Add(24*time.Millisecond), 0, 1, 1)
	if after := clock.sample(start.Add(25*time.Millisecond), 1024.0/48000, 1, 1); after < before {
		t.Fatal("late callback observation moved the video backwards")
	}
	clock.reset(2)
	if got := clock.sample(start.Add(time.Hour), 1.99, 3, 1); got != 2 {
		t.Fatal("resume lost its paused position or included paused time:", got)
	}
	clock.reset(.5)
	if got := clock.sample(start.Add(2*time.Hour), .5, 1, 2); got != .5 {
		t.Fatal("a backwards seek retained the old clock:", got)
	}
	if got := clock.sample(start.Add(2*time.Hour+10*time.Millisecond), .5, 1, 2); got != .52 {
		t.Fatal("clock interpolation ignored playback rate:", got)
	}
}
