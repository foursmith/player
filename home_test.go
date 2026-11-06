package main

import (
	"bytes"
	"math"
	"os"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestHomeAnimationStopsAndResumesWithWindowFocus(t *testing.T) {
	if err := loadAssets(); err != nil {
		t.Fatal(err)
	}
	a := &application{preferences: defaultPreferences()}
	frames := 0
	view := ui.NewTester(func(c *ui.Context) {
		frames++
		c.SetTheme(darkPlayerTheme)
		a.width, a.height = c.Size()
		a.home(c)
	}, 128, 96)
	if view.FramePending() {
		t.Fatal("unfocused home keeps requesting animation frames")
	}
	a.homeAnimation.setFocused(view.Now(), true)
	view.Frame()
	if !view.FramePending() {
		t.Fatal("focused home did not start its animation")
	}
	view.Advance(100 * time.Millisecond)
	a.homeAnimation.setFocused(view.Now(), false)
	view.Frame()
	if view.FramePending() {
		t.Fatal("home keeps requesting frames after losing focus")
	}
	pausedFrames := frames
	pausedImage := bytes.Clone(view.Image().Pix)
	pausedTime := a.homeAnimation.seconds(view.Now())
	view.Advance(time.Minute)
	if frames != pausedFrames || a.homeAnimation.seconds(view.Now()) != pausedTime {
		t.Fatal("home animation advanced while unfocused")
	}
	// Other state changes may redraw the home while the window is inactive.
	view.Frame()
	if !bytes.Equal(view.Image().Pix, pausedImage) {
		t.Fatal("an inactive redraw changed the paused animation")
	}
	a.homeAnimation.setFocused(view.Now(), true)
	view.Frame()
	if !view.FramePending() || a.homeAnimation.seconds(view.Now()) != pausedTime {
		t.Fatal("refocusing did not resume from the paused position")
	}
	view.Advance(50 * time.Millisecond)
	if a.homeAnimation.seconds(view.Now()) <= pausedTime {
		t.Fatal("home animation did not advance after refocusing")
	}
	view.SetPreferences(ui.Preferences{ReduceMotion: true})
	if view.FramePending() {
		t.Fatal("reduced-motion home keeps requesting animation frames")
	}
}

func TestHomeFishInteractionRepeatsAsOneCycle(t *testing.T) {
	big, little := homeFishMotion(0)
	lunging, retreating := homeFishMotion(18 * .63)
	if lunging.x >= big.x || retreating.x >= little.x {
		t.Fatal("the big fish and little fish did not approach the interaction together")
	}
	_, hidden := homeFishMotion(18 * .72)
	if hidden.opacity != 0 || hidden.scale >= 1 {
		t.Fatal("the little fish did not shrink out of view")
	}
	_, returned := homeFishMotion(18 * .90)
	if math.Abs(returned.x-little.x) > .01 || math.Abs(returned.opacity-1) > .00001 {
		t.Fatal("the little fish did not return before the cycle ended")
	}
	loopBig, loopLittle := homeFishMotion(18)
	if big != loopBig || little != loopLittle {
		t.Fatal("the fish interaction does not loop continuously")
	}
	if os.Getenv("FSVR_UI_SNAPSHOT_DIR") == "" {
		return
	}
	if err := loadAssets(); err != nil {
		t.Fatal(err)
	}
	for _, phase := range []struct {
		name    string
		seconds float64
	}{{"idle", 0}, {"approach", 18 * .63}, {"hidden", 18 * .72}, {"return", 18 * .90}} {
		a := &application{preferences: defaultPreferences(), homeAnimation: homeAnimationClock{elapsed: time.Duration(phase.seconds * float64(time.Second))}}
		view := ui.NewTester(func(c *ui.Context) {
			c.SetTheme(darkPlayerTheme)
			a.width, a.height = c.Size()
			a.home(c)
		}, 800, 640)
		writeUISnapshot(t, "home-fish-"+phase.name+".png", view)
	}
}
