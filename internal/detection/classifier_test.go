package detection

import (
	"image"
	"image/color"
	"os"
	"testing"
)

func TestClassifierInputKeepsWholeFrameAndBlackPadding(t *testing.T) {
	for _, bounds := range []image.Rectangle{image.Rect(10, 20, 14, 22), image.Rect(-2, -4, 0, 0)} {
		img := image.NewRGBA(bounds)
		for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
			for x := bounds.Min.X; x < bounds.Max.X; x++ {
				img.SetRGBA(x, y, color.RGBA{R: 240, G: 120, B: 30, A: 255})
			}
		}
		input := make([]float32, 8*8*3)
		for i := range input {
			input[i] = 17 // Padding must overwrite a previous frame's pixels.
		}
		classifierInput(input, 8, 8, img)
		for y := range 8 {
			for x := range 8 {
				inside := y >= 2 && y < 6
				if bounds.Dy() > bounds.Dx() {
					inside = x >= 2 && x < 6
				}
				want := [3]float32{}
				if inside {
					want = [3]float32{240, 120, 30}
				}
				for channel := range 3 {
					if got := input[(y*8+x)*3+channel]; got != want[channel] {
						t.Fatalf("bounds %v pixel %d,%d: got %v want %v", bounds, x, y, got, want[channel])
					}
				}
			}
		}
	}
}

func TestImageClassifierRejectsFaceOutputContract(t *testing.T) {
	library := os.Getenv("FSVR_TFLITE_LIBRARY")
	if library == "" {
		t.Skip("LiteRT runtime unavailable")
	}
	model, err := os.ReadFile("../../assets/models/blaze_face_short_range.tflite")
	if err != nil {
		t.Fatal(err)
	}
	if classifier, err := NewImageClassifier(library, model, 7); err == nil {
		classifier.Close()
		t.Fatal("face tensors were accepted as classification scores")
	}
}

func projectionClassifier(t testing.TB) *ImageClassifier {
	t.Helper()
	model, err := os.ReadFile("../../assets/models/projection.tflite")
	if err != nil {
		t.Fatal(err)
	}
	classifier, err := NewImageClassifier(os.Getenv("FSVR_TFLITE_LIBRARY"), model, 7)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(classifier.Close)
	return classifier
}

func TestProjectionModel(t *testing.T) {
	classifier := projectionClassifier(t)
	if _, err := classifier.Classify(image.NewRGBA(image.Rect(0, 0, 320, 160))); err != nil {
		t.Fatal(err)
	}
	classifier.Close()
	if _, err := classifier.Classify(image.NewRGBA(image.Rect(0, 0, 320, 160))); err == nil {
		t.Fatal("closed classifier accepted an inference")
	}
}

func BenchmarkProjectionClassifier(b *testing.B) {
	classifier := projectionClassifier(b)
	img := image.NewRGBA(image.Rect(0, 0, 320, 160))
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := classifier.Classify(img); err != nil {
			b.Fatal(err)
		}
	}
}
