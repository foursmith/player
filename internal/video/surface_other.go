//go:build !darwin

package video

func New(window uintptr) (Surface, error) { return &rgbaSurface{}, nil }
