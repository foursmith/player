package video

import (
	"image"
	"image/color"
	"math"

	"github.com/foursmith/player/internal/projection"
)

const (
	ambientSize     = 7  // Matches the color-field grid in all three shaders.
	ambientDirect   = -1 // Capture the current frame without presentation history.
	ambientDisabled = -2
)

type ambientField [ambientSize * ambientSize][3]float64

func newAmbientField(source *image.RGBA) (field ambientField) {
	for y := range ambientSize {
		for x := range ambientSize {
			sample := sampleRGBA(source, (float64(x)+.5)/ambientSize, (float64(y)+.5)/ambientSize)
			field[y*ambientSize+x] = [3]float64{sample[0], sample[1], sample[2]}
		}
	}
	return field
}

// color mirrors the GPU glow for software captures. Its fixed source samples
// are prepared once per capture, while live playback uses GPU textures.
func (field *ambientField) color(u, v, aspect float64, pixel image.Point, spread ambientSpread) color.RGBA {
	mx, my := aspect/math.Min(aspect, 1), 1/math.Min(aspect, 1)
	sx, sy := spread.Left, spread.Top
	if u >= .5 {
		sx = spread.Right
	}
	if v >= .5 {
		sy = spread.Bottom
	}
	dx, dy := (u-projection.Clamp(u, 0, 1))*mx, (v-projection.Clamp(v, 0, 1))*my
	reach := projection.Clamp(math.Hypot(dx/sx, dy/sy), 0, 1)
	t := projection.Clamp(math.Hypot(dx, dy)/.15, 0, 1)
	transition := t * t * (3 - 2*t)
	fade := (1 - .22*transition) * (1 - .78*reach*reach*(3-2*reach))
	centerU, centerV := projection.Clamp(u, 0, 1)*.92+.04, projection.Clamp(v, 0, 1)*.92+.04
	blur := .085 + .07*reach
	var light [3]float64
	total := 0.0
	for y := range ambientSize {
		for x := range ambientSize {
			su, sv := (float64(x)+.5)/ambientSize, (float64(y)+.5)/ambientSize
			dx, dy := (su-centerU)/blur, (sv-centerV)/blur
			weight := math.Exp(-(dx*dx + dy*dy) / 2)
			sample := field[y*ambientSize+x]
			for c := range light {
				light[c] += sample[c] * weight
			}
			total += weight
		}
	}
	for i := range light {
		light[i] /= total * 255
	}
	luminance := light[0]*.2126 + light[1]*.7152 + light[2]*.0722
	gain := 1 + .35*transition*(1-max(light[0], light[1], light[2]))
	noise := ambientNoise(pixel)
	var out [3]uint8
	for i := range light {
		glow := projection.Clamp((luminance+(light[i]-luminance)*(1+.25*transition))*gain, 0, 1) * fade * 255
		out[i] = uint8(projection.Clamp(glow+noise, 0, 255) + .5)
	}
	return color.RGBA{out[0], out[1], out[2], 255}
}

// The reach fits the actual space on each side of the transformed video.
// Values use the source's short side as unit, matching the native shaders.
type ambientSpread struct{ Left, Top, Right, Bottom float64 }

func flatAmbientSpread(view FlatView, aspect, sourceAspect float64) ambientSpread {
	view = view.Constrain(aspect, sourceAspect)
	ex, ey := flatExtent(aspect, sourceAspect)
	mx, my := sourceAspect/math.Min(sourceAspect, 1), 1/math.Min(sourceAspect, 1)
	return ambientSpread{
		Left:   max(.000001, (.5*ex/view.Zoom-.5+view.X)*mx),
		Top:    max(.000001, (.5*ey/view.Zoom-.5+view.Y)*my),
		Right:  max(.000001, (.5*ex/view.Zoom-.5-view.X)*mx),
		Bottom: max(.000001, (.5*ey/view.Zoom-.5-view.Y)*my),
	}
}

// Zero-mean spatial dither in 8-bit code values. It is fixed in physical pixel
// coordinates, so a paused gradient stays still and neutral colors stay neutral.
func ambientNoise(pixel image.Point) float64 {
	fract := func(v float64) float64 { return v - math.Floor(v) }
	return fract(52.9829189*fract(float64(pixel.X)*.06711056+float64(pixel.Y)*.00583715)) - .5
}

func sampleRGBA(source *image.RGBA, u, v float64) [4]float64 {
	w, h := source.Bounds().Dx(), source.Bounds().Dy()
	sx, sy := projection.Clamp(u*float64(w)-.5, 0, float64(w-1)), projection.Clamp(v*float64(h)-.5, 0, float64(h-1))
	x, y := int(sx), int(sy)
	fx, fy := sx-float64(x), sy-float64(y)
	a, b := source.RGBAAt(x, y), source.RGBAAt(min(x+1, w-1), y)
	c, d := source.RGBAAt(x, min(y+1, h-1)), source.RGBAAt(min(x+1, w-1), min(y+1, h-1))
	var out [4]float64
	for i, values := range [4][4]uint8{{a.R, b.R, c.R, d.R}, {a.G, b.G, c.G, d.G}, {a.B, b.B, c.B, d.B}, {a.A, b.A, c.A, d.A}} {
		out[i] = (float64(values[0])*(1-fx)+float64(values[1])*fx)*(1-fy) + (float64(values[2])*(1-fx)+float64(values[3])*fx)*fy
	}
	return out
}

// ambientHistory follows media time, independently of the video's frame rate.
// Repainting a paused frame does not add weight. A seek or new source starts
// with its own color field instead of carrying light over from the old scene.
type ambientHistory struct {
	serial, generation uint64
	pts                float64
	size               image.Point
	valid              bool
}

func (h *ambientHistory) step(serial uint64, pts float64, generation uint64, size image.Point) (alpha float32, changed bool) {
	reset := !h.valid || generation != h.generation || size != h.size
	if !reset && serial == h.serial {
		return 0, false
	}
	dt := pts - h.pts
	if reset || dt <= 0 || dt > .5 {
		alpha = 1
	} else {
		alpha = float32(-math.Expm1(-dt / .35))
	}
	*h = ambientHistory{serial: serial, generation: generation, pts: pts, size: size, valid: true}
	return alpha, true
}
