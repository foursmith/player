package media

import (
	"context"
	"errors"
	"image"
	"io"
	"math"
	"math/rand/v2"
	"time"
)

type SampleTrace struct {
	Stage             string
	Attempt           int
	Requested, PTS    float64
	Strategy          string
	ReplacedDuplicate bool
	DuplicatePTS      float64
	Width, Height     int
	Decoder           string
	Elapsed           time.Duration
	Err               error
}

// sampleTimes spreads attempts across three parts of the clip, with independent
// jitter inside each interval. Further rounds replace black or unusable frames.
func sampleTimes(duration float64, random func() float64) []float64 {
	times := make([]float64, 9)
	rotations := [3]int{int(random() * 3), int(random() * 3), int(random() * 3)}
	for i := range times {
		slot := (i/3 + rotations[i%3]) % 3
		fraction := (float64(i%3) + (float64(slot)+random())/3) / 3
		times[i] = max(0, duration) * (.03 + .94*fraction)
	}
	return times
}

// SampleFrames uses its own silent decoder and never seeks the player. AVC
// thumbnails use software to avoid unsupported hardware sessions; other codecs
// retain hardware acceleration for efficient high-resolution sampling.
// Returning true from visit stops sampling. At most nine small frames are read.
// Optional trace reports setup and every attempted frame on the sampling owner.
func SampleFrames(ctx context.Context, source string, visit func(*image.RGBA) bool, trace func(SampleTrace)) error {
	opened := time.Now()
	d, err := openContext(ctx, Source{URL: source}, false, true)
	if err != nil {
		if trace != nil {
			trace(SampleTrace{Stage: "open", Elapsed: time.Since(opened), Err: err})
		}
		return err
	}
	defer d.Close()
	metadata := d.Metadata()
	if trace != nil {
		trace(SampleTrace{Stage: "open", Width: metadata.Width, Height: metadata.Height, Decoder: metadata.Decoder, Elapsed: time.Since(opened)})
	}
	width := 320
	height := max(32, min(320, int(float64(width)*float64(metadata.Height)/float64(max(1, metadata.Width))+.5)))
	keyframes := metadata.Duration > 30
	var seen []float64
	for attempt, at := range sampleTimes(metadata.Duration, rand.Float64) {
		if err := ctx.Err(); err != nil {
			return err
		}
		started := time.Now()
		info := SampleTrace{Stage: "frame", Attempt: attempt + 1, Requested: at, PTS: math.NaN(), Width: width, Height: height, Strategy: "sequential"}
		emit := func(err error) {
			if trace != nil {
				info.Err, info.Elapsed, info.Decoder = err, time.Since(started), d.Metadata().Decoder
				trace(info)
			}
		}
		if metadata.Duration > 0 {
			info.Strategy = "exact"
			if keyframes {
				info.Strategy = "near_keyframe"
			}
			if err := d.seek(at, !keyframes); err != nil {
				emit(err)
				return err
			}
		}
		frame, err := d.Next()
		if errors.Is(err, io.EOF) {
			emit(err)
			continue
		}
		if err != nil {
			emit(err)
			return err
		}
		duplicate := false
		for _, pts := range seen {
			duplicate = duplicate || math.Abs(pts-frame.PTS()) < .5/max(1, metadata.FrameRate)
		}
		if duplicate && keyframes {
			// A long GOP may send several random positions to the same black
			// keyframe. Decode to the precise position before visiting again.
			info.ReplacedDuplicate, info.DuplicatePTS, info.Strategy = true, frame.PTS(), "exact_duplicate_replacement"
			frame.Close()
			if err := d.Seek(at); err != nil {
				emit(err)
				return err
			}
			frame, err = d.Next()
			if errors.Is(err, io.EOF) {
				emit(err)
				continue
			}
			if err != nil {
				emit(err)
				return err
			}
		}
		info.PTS = frame.PTS()
		seen = append(seen, frame.PTS())
		img, err := frame.RGBA(width, height)
		frame.Close()
		if err != nil {
			emit(err)
			return err
		}
		emit(nil)
		if visit(img) {
			return nil
		}
	}
	return nil
}
