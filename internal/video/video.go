// Package video composes the existing VR projections below MyGo's native UI.
package video

import (
	"github.com/foursmith/player/internal/media"
	"github.com/foursmith/player/internal/projection"
	"image"
)

type Parameters struct {
	Mode       projection.Mode
	View       projection.View
	SplitCount int
	Portrait   bool
	Quality    int
	FOV        float64
}
type Surface interface {
	Present(frame *media.Frame, params Parameters, width, height, scale float64) error
	Capture(frame *media.Frame, params Parameters, width, height int) (*image.RGBA, error)
	Close()
}
