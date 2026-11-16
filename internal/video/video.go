// Package video renders flat videos and VR projections below MyGo's native UI.
package video

import (
	"image"

	"github.com/foursmith/player/internal/media"
	"github.com/foursmith/player/internal/projection"
)

type Parameters struct {
	DisableAmbientLight bool
	Generation          uint64 // playback/seek generation; resets ambient history
	Mode                projection.Mode
	View                projection.View
	FlatView            FlatView
	SplitCount          int
	Portrait            bool
	FOV                 float64
}
type Surface interface {
	Present(frame *media.Frame, params Parameters, width, height, scale float64) error
	Capture(frame *media.Frame, params Parameters, width, height int) (*image.RGBA, error)
	// Clear drops presentation state while keeping the surface reusable.
	Clear()
	Close()
}
