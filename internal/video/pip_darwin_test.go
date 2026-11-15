package video

import (
	"runtime"
	"testing"

	"github.com/ebitengine/purego/objc"
)

// Validate the private system contract without presenting a window or starting
// an application. Runtime checks also protect users on incompatible OS releases.
func TestSystemPictureInPictureAPI(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := loadPIP(); err != nil {
		t.Fatal(err)
	}
}

// PIP resizes its container, so the supplied content view must follow it even
// while playback is paused. No window or application is launched by this test.
func TestPIPVideoViewFollowsContainerResize(t *testing.T) {
	if err := loadPIP(); err != nil {
		t.Fatal(err)
	}
	defer metalPool()()
	container := send(send(class("NSView"), "alloc"), "initWithFrame:", cgRect{Width: 480, Height: 270})
	defer send(container, "release")
	view := newPIPVideoView(480, 270)
	defer send(view, "release")
	send(container, "addSubview:", view)
	for _, size := range []cgSize{{Width: 960, Height: 540}, {Width: 240, Height: 135}, {Width: 640, Height: 360}} {
		send(container, "setFrameSize:", size)
		bounds := objc.Send[cgRect](view, selector("bounds"))
		frame := objc.Send[cgRect](view, selector("frame"))
		if bounds.Width != size.Width || bounds.Height != size.Height || frame.X != 0 || frame.Y != 0 {
			t.Fatalf("container %+v left video frame %+v, bounds %+v", size, frame, bounds)
		}
	}
}
