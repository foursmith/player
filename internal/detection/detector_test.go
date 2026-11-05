package detection

import (
	"image"
	"image/draw"
	_ "image/jpeg"
	"math"
	"os"
	"testing"
)

func TestBlazeFaceAnchors(t *testing.T) {
	if n := len(anchors(128)); n != 896 {
		t.Fatalf("short range anchors: %d", n)
	}
	if n := len(anchors(192)); n != 2304 {
		t.Fatalf("full range anchors: %d", n)
	}
}
func TestWeightedNMS(t *testing.T) {
	faces := weightedNMS([]Face{{.1, .1, .2, .2, .9}, {.11, .1, .2, .2, .8}, {.6, .6, .1, .1, .7}}, .45)
	if len(faces) != 2 || math.Abs(faces[0].X-(.1*.9+.11*.8)/1.7) > 1e-8 {
		t.Fatalf("wrong merged boxes: %+v", faces)
	}
}

func TestWeightedNMSConsumesDegenerateBoxes(t *testing.T) {
	if got := weightedNMS([]Face{{Width: 0, Height: 0, Score: .9}, {Width: .2, Height: .2, Score: .8}}, .45); len(got) != 2 {
		t.Fatal(got)
	}
}
func TestBundledFaceModels(t *testing.T) {
	library := os.Getenv("FSVR_TFLITE_LIBRARY")
	if library == "" {
		t.Skip("run just test to load the prepared LiteRT runtime")
	}
	for _, name := range []string{"short", "full"} {
		t.Run(name, func(t *testing.T) {
			model, err := os.ReadFile("../../assets/models/blaze_face_" + name + "_range.tflite")
			if err != nil {
				t.Fatal(err)
			}
			detector, err := New(library, model)
			if err != nil {
				t.Fatal(err)
			}
			defer detector.Close()
			faces, err := detector.Detect(image.NewRGBA(image.Rect(0, 0, 320, 320)))
			if err != nil {
				t.Fatal(err)
			}
			if len(faces) != 0 {
				t.Fatalf("faces in an empty frame: %+v", faces)
			}
			file, err := os.Open("testdata/portrait.jpg")
			if err != nil {
				t.Fatal(err)
			}
			portrait, _, err := image.Decode(file)
			file.Close()
			if err != nil {
				t.Fatal(err)
			}
			rgba := image.NewRGBA(portrait.Bounds())
			draw.Draw(rgba, rgba.Bounds(), portrait, portrait.Bounds().Min, draw.Src)
			faces, err = detector.Detect(rgba)
			if err != nil || len(faces) != 1 || faces[0].Score < .7 {
				t.Fatalf("positive model inference: %+v, %v", faces, err)
			}
		})
	}
}

func BenchmarkBundledFaceModels(b *testing.B) {
	library := os.Getenv("FSVR_TFLITE_LIBRARY")
	if library == "" {
		b.Skip("LiteRT runtime unavailable")
	}
	img := image.NewRGBA(image.Rect(0, 0, 320, 320))
	for _, name := range []string{"short", "full"} {
		b.Run(name, func(b *testing.B) {
			model, err := os.ReadFile("../../assets/models/blaze_face_" + name + "_range.tflite")
			if err != nil {
				b.Fatal(err)
			}
			detector, err := New(library, model)
			if err != nil {
				b.Fatal(err)
			}
			defer detector.Close()
			if _, err := detector.Detect(img); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			for b.Loop() {
				if _, err := detector.Detect(img); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
