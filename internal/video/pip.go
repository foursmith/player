package video

import "github.com/foursmith/player/internal/media"

// PictureInPicture owns a system presentation. All methods run on the UI thread.
type PictureInPicture interface {
	Present(*media.Frame, Parameters, string, bool) error
	Size() (float64, float64)
	Closed() bool
	Close()
}

type PictureInPictureActions struct {
	Play, Pause func()
}
