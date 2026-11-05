package main

import (
	"image"
	"path/filepath"
	"testing"

	"github.com/foursmith/player/internal/projection"
)

func TestDefaultProjectionModelLoadsAndRunsWithoutExternalWeights(t *testing.T) {
	t.Setenv("FSVR_PROJECTION_MODEL", "")
	classifier := projectionClassifier("", nil)
	if classifier == nil {
		t.Fatal("cannot load bundled projection model")
	}
	defer classifier.Close()
	probabilities, err := classifier.Classify(image.NewRGBA(image.Rect(0, 0, 320, 160)))
	if err != nil {
		t.Fatal(err)
	}
	if !(&projection.ModelScores{}).Add(probabilities) {
		t.Fatal("bundled projection model did not return seven softmax scores")
	}
}

func TestProjectionModelSupportsDisablingAndMissingOverrides(t *testing.T) {
	t.Setenv("FSVR_PROJECTION_MODEL", "off")
	if projectionClassifier("off", nil) != nil {
		t.Fatal("projection model disable switch was ignored")
	}
	if projectionClassifier(filepath.Join(t.TempDir(), "missing.tflite"), nil) != nil {
		t.Fatal("unavailable override produced a classifier")
	}
}
