package main

import (
	"github.com/foursmith/player/internal/detection"
	"image"
)

// faceModels belongs to one inference/export worker, never the UI thread.
type faceModels struct{ short, full *detection.Detector }

func (m *faceModels) detect(img *image.RGBA, full bool) ([]detection.Face, error) {
	detector := m.short
	if full {
		detector = m.full
	}
	if detector == nil {
		name := "short"
		if full {
			name = "full"
		}
		model, err := assets.ReadFile("assets/models/blaze_face_" + name + "_range.tflite")
		if err != nil {
			return nil, err
		}
		detector, err = detection.New(runtimeLibrary(), model)
		if err != nil {
			return nil, err
		}
		if full {
			m.full = detector
		} else {
			m.short = detector
		}
	}
	return detector.Detect(img)
}
func (m *faceModels) close() { m.short.Close(); m.full.Close() }
