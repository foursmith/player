//go:build !darwin && !windows

package video

const TransparentWindow = false

func New(window uintptr) (Surface, error) { return &rgbaSurface{}, nil }
