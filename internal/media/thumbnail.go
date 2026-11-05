package media

import (
	"context"
	"errors"
	"image"
	"io"
	"math"
)

type Preview struct {
	Image    image.Image
	Duration float64
}

// Thumbnail reads duration and a small still through an independent silent decoder. It
// tries later positions when a keyframe is black, without seeking the player.
func Thumbnail(ctx context.Context, source string) (Preview, error) {
	d, err := openContext(ctx, Source{URL: source}, false, true)
	if err != nil {
		return Preview{}, err
	}
	defer d.Close()
	m := d.Metadata()
	preview := Preview{Duration: m.Duration}
	scale := min(240/float64(max(1, m.Width)), 135/float64(max(1, m.Height)))
	w, h := max(1, int(math.Round(float64(m.Width)*scale))), max(1, int(math.Round(float64(m.Height)*scale)))
	var result *image.RGBA
	for _, fraction := range []float64{.1, .35, .65} {
		at := m.Duration * fraction
		if m.Duration > 0 {
			if err := d.seek(at, false); err != nil {
				return preview, err
			}
		}
		frame, err := d.Next()
		if errors.Is(err, io.EOF) {
			continue
		}
		if err != nil {
			return preview, err
		}
		result, err = frame.RGBA(w, h)
		frame.Close()
		if err != nil {
			return preview, err
		}
		if thumbnailVisible(result) {
			preview.Image = result
			return preview, nil
		}
		// Sparse keyframes can all point into the same black introduction.
		if m.Duration > 0 {
			if err := d.Seek(at); err != nil {
				return preview, err
			}
			frame, err := d.Next()
			if err == nil {
				result, err = frame.RGBA(w, h)
				frame.Close()
			}
			if err != nil && !errors.Is(err, io.EOF) {
				return preview, err
			}
			if result != nil && thumbnailVisible(result) {
				preview.Image = result
				return preview, nil
			}
		}
	}
	if result == nil {
		return preview, errors.New("video has no thumbnail frame")
	}
	preview.Image = result
	return preview, nil
}

func thumbnailVisible(img *image.RGBA) bool {
	var total int64
	for i := 0; i+2 < len(img.Pix); i += 4 {
		total += int64(img.Pix[i]) + int64(img.Pix[i+1]) + int64(img.Pix[i+2])
	}
	return total > int64(img.Rect.Dx()*img.Rect.Dy())*12
}
