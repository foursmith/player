//go:build !darwin

package subtitle

import "image"

// Other platforms compose through the FFmpeg/libass encoder filter.
const NativeComposition = false

func Compose(img *image.RGBA, text string, cssWidth float64) error { return nil }
