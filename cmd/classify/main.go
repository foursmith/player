// Command classify evaluates an experimental local seven-class model. It never
// renames media or updates playback history.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"image"
	"image/draw"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"path/filepath"
	"time"

	"github.com/foursmith/player/internal/detection"
	"github.com/foursmith/player/internal/media"
	"github.com/foursmith/player/internal/projection"
)

type result struct {
	File           string             `json:"file"`
	Mode           string             `json:"mode,omitempty"`
	Decisive       bool               `json:"decisive"`
	Scores         map[string]float64 `json:"scores,omitempty"`
	Heuristic      string             `json:"heuristic,omitempty"`
	Metadata       string             `json:"metadata_mode,omitempty"`
	Spherical      string             `json:"spherical,omitempty"`
	Stereo         string             `json:"stereo,omitempty"`
	Resolved       string             `json:"resolved_mode,omitempty"`
	Reason         string             `json:"reason,omitempty"`
	ConfidentVotes map[string]int     `json:"confident_frame_votes,omitempty"`
	FrameModes     []string           `json:"frame_modes,omitempty"`
	Samples        int                `json:"samples"`
	InferenceMS    float64            `json:"inference_ms"`
	TotalMS        float64            `json:"total_ms"`
	Error          string             `json:"error,omitempty"`
}

func classify(ctx context.Context, classifier *detection.ImageClassifier, path string) (r result) {
	r.File = filepath.Base(path)
	began := time.Now()
	defer func() { r.TotalMS = float64(time.Since(began).Microseconds()) / 1000 }()
	scores, heuristic := &projection.ModelScores{}, &projection.FrameDetector{}
	var inferenceErr error
	var metadataMode projection.Mode
	var metadataConclusive bool
	observe := func(img *image.RGBA, aspect float64) bool {
		evidence := projection.AnalyzeFrame(img, aspect)
		if !evidence.Usable {
			return false
		}
		r.FrameModes = append(r.FrameModes, projection.IDs[evidence.Mode])
		started := time.Now()
		probabilities, err := classifier.Classify(img)
		r.InferenceMS += float64(time.Since(started).Microseconds()) / 1000
		if err != nil {
			inferenceErr = err
			return true
		}
		if !scores.Add(probabilities) {
			inferenceErr = errors.New("invalid model probabilities")
			return true
		}
		heuristic.Observe(evidence)
		return heuristic.SamplingComplete(scores)
	}
	var err error
	switch filepath.Ext(path) {
	case ".png", ".jpg", ".jpeg":
		var file *os.File
		file, err = os.Open(path)
		if err == nil {
			var decoded image.Image
			decoded, _, err = image.Decode(file)
			file.Close()
			if err == nil {
				img := image.NewRGBA(image.Rect(0, 0, decoded.Bounds().Dx(), decoded.Bounds().Dy()))
				draw.Draw(img, img.Bounds(), decoded, decoded.Bounds().Min, draw.Src)
				observe(img, float64(img.Rect.Dx())/float64(img.Rect.Dy()))
			}
		}
	default:
		var decoder *media.Decoder
		decoder, err = media.Open(ctx, media.Source{URL: path}, false)
		if err == nil {
			metadata := decoder.Metadata()
			decoder.Close()
			r.Spherical, r.Stereo = metadata.Spherical, metadata.Stereo
			metadataMode, metadataConclusive = projection.MetadataMode(metadata.Spherical, metadata.Stereo, float64(metadata.Width)/float64(max(1, metadata.Height)))
			if metadataConclusive {
				r.Metadata = projection.IDs[metadataMode]
			}
			err = media.SampleFrames(ctx, media.Source{URL: path}, func(img *image.RGBA) bool {
				return observe(img, float64(metadata.Width)/float64(max(1, metadata.Height)))
			}, nil)
		}
	}
	if inferenceErr != nil {
		err = inferenceErr
	}
	if err != nil {
		r.Error = err.Error()
	}
	if scores.Samples > 0 {
		mode, _, decisive := scores.Result()
		r.Mode, r.Decisive, r.Samples = projection.IDs[mode], decisive, scores.Samples
		r.Scores = map[string]float64{}
		r.ConfidentVotes = map[string]int{}
		votes := scores.ConfidentVotes()
		for i, p := range scores.Probabilities() {
			r.Scores[projection.IDs[i]] = p
			if votes[i] > 0 {
				r.ConfidentVotes[projection.IDs[i]] = votes[i]
			}
		}
	}
	if mode, ok := heuristic.Result(); ok {
		r.Heuristic = projection.IDs[mode]
	}
	decision := heuristic.Resolve(projection.Detect(filepath.Base(path), "", ""), scores)
	if metadataConclusive {
		decision = projection.Decision{Mode: metadataMode, Reason: "spatial_metadata"}
	}
	r.Resolved, r.Reason = projection.IDs[decision.Mode], decision.Reason
	return r
}

func main() {
	modelPath := flag.String("model", "assets/models/projection.tflite", "local TFLite projection model")
	library := flag.String("runtime", os.Getenv("FSVR_TFLITE_LIBRARY"), "LiteRT C library")
	recent := flag.String("recent", "", "read recent media paths from this state.json")
	timeout := flag.Duration("timeout", 15*time.Second, "sampling time limit per file")
	flag.Parse()
	model, err := os.ReadFile(*modelPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	classifier, err := detection.NewImageClassifier(*library, model, len(projection.IDs))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer classifier.Close()
	paths := flag.Args()
	if *recent != "" {
		data, err := os.ReadFile(*recent)
		var state struct {
			RecentMedia []struct{ Path, SourceID string }
		}
		if err != nil || json.Unmarshal(data, &state) != nil {
			fmt.Fprintln(os.Stderr, "cannot read recent media list")
			os.Exit(1)
		}
		for _, item := range state.RecentMedia {
			if item.SourceID == "" && item.Path != "" {
				paths = append(paths, item.Path)
			}
		}
	}
	if len(paths) == 0 {
		fmt.Fprintln(os.Stderr, "provide media paths or -recent state.json")
		os.Exit(1)
	}
	encoder := json.NewEncoder(os.Stdout)
	for _, path := range paths {
		ctx, cancel := context.WithTimeout(context.Background(), *timeout)
		encoder.Encode(classify(ctx, classifier, path))
		cancel()
	}
}
