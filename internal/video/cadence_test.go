package video

import (
	"math"
	"testing"
)

func TestCadenceAndInteraction(t *testing.T) {
	render, next := ScheduleFrame(0, 30, 0, false)
	if !render || math.Abs(next-1000.0/30) > .0001 {
		t.Fatal(render, next)
	}
	render, n := ScheduleFrame(16, 30, next, false)
	if render || n != next {
		t.Fatal(render, n)
	}
	if render, _ = ScheduleFrame(16, 30, next, true); !render {
		t.Fatal("interaction delayed")
	}
	render, next = ScheduleFrame(1000, 30, next, false)
	if !render || next <= 1000 || next > 1034 {
		t.Fatal(render, next)
	}
	if PixelRatio(0, 2) != 1.2 || PixelRatio(3, 5) != 4 {
		t.Fatal("quality scale")
	}
}
