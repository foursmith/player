package projection

import (
	"image"
	"math"
)

const projectionConfirmationFrames = 9

type FrameEvidence struct {
	Mode            Mode
	Usable          bool
	Similarity      float64
	StereoAgreement float64
	StereoLayout    Mode
	WideDisparity   bool
	MeanLuma        float64
	Variance        float64
	BrightFraction  float64
	Rejection       string
}

type FrameDetector struct {
	Samples       int
	votes         [7]int
	partialStereo [7]int
}

func (d *FrameDetector) Observe(evidence FrameEvidence) {
	if evidence.Usable {
		d.Samples++
		d.votes[evidence.Mode]++
		if evidence.StereoLayout.Binocular() && evidence.Similarity >= .75 && evidence.StereoAgreement >= .125 {
			d.partialStereo[evidence.StereoLayout]++
		}
	}
}

func (d *FrameDetector) Votes() [7]int              { return d.votes }
func (d *FrameDetector) PartialStereoVotes() [7]int { return d.partialStereo }

// SamplingComplete keeps the usual three-frame fast path for clear formats.
// Conflicting binocular subtypes use all nine frames for confirmation; unresolved
// results may use all nine attempts within the sampler's existing time limit.
func (d *FrameDetector) SamplingComplete(model *ModelScores) bool {
	if d.Samples < 3 {
		return false
	}
	if d.Samples >= 9 {
		return true
	}
	mode, stable := d.Result()
	if stable && mode.Fisheye() {
		return true
	}
	if model == nil || model.Samples == 0 {
		return stable
	}
	predicted, _, confident := model.Result()
	if predicted == SBSFisheye && model.Samples < projectionConfirmationFrames {
		return false
	}
	if stable && mode.Binocular() {
		// EQR correspondence establishes the eye layout but does not prove
		// the lens projection. Verify a conflicting model subtype on more
		// views before either adopting or dismissing it.
		return !predicted.Binocular() || predicted == mode
	}
	if confident {
		return true
	}
	if _, supported := d.modelSupportedLayout(model); supported {
		return true
	}
	if predicted, _, _, accepted := model.BinocularConsensus(); accepted {
		return d.partialStereoSupports(predicted)
	}
	return false
}

func (d *FrameDetector) partialStereoSupports(mode Mode) bool {
	if mode == SBSFisheye {
		mode = SBS180
	}
	return d.partialStereo[mode] >= 2 && d.partialStereo[mode]*3 >= d.Samples*2
}

// A third matching frame can confirm a layout even when other frames lack
// sufficient texture or were mistaken for mono panoramas. Require model layout
// agreement, two-thirds of positive VR evidence, and no competing binocular
// projection. Unmatched frames abstain here: they do not prove a 2D layout.
// A layout-only model score never selects the projection subtype.
func (d *FrameDetector) modelSupportedLayout(model *ModelScores) (Mode, bool) {
	layout, _, confident := model.BinocularLayout()
	if !confident || d.votes[layout] < 3 {
		return Flat, false
	}
	positive := 0
	for mode, votes := range d.votes {
		if Mode(mode).IsVR() {
			positive += votes
		}
		if Mode(mode).Binocular() && Mode(mode) != layout && votes > 0 {
			return Flat, false
		}
	}
	return layout, d.votes[layout]*3 >= positive*2
}

// Result requires agreement across multiple informative, non-black frames.
// Three matching binocular views can also establish a layout when all remaining
// views are unmatched, rather than affirmative evidence of another VR format.
func (d *FrameDetector) Result() (Mode, bool) {
	if d.Samples < 2 {
		return Flat, false
	}
	for mode, votes := range d.votes {
		if Mode(mode) != Flat && votes >= 2 && votes*3 >= d.Samples*2 {
			return Mode(mode), true
		}
	}
	for mode, votes := range d.votes {
		if !Mode(mode).Binocular() || votes < 3 {
			continue
		}
		conflicting := false
		for other, count := range d.votes {
			conflicting = conflicting || other != mode && Mode(other).IsVR() && count > 0
		}
		if !conflicting {
			return Mode(mode), true
		}
	}
	if d.votes[Flat] >= 2 && d.votes[Flat]*3 >= d.Samples*2 {
		return Flat, true
	}
	return Flat, false
}

type Decision struct {
	Mode   Mode
	Reason string
}

// Resolve protects stable fisheye and binocular evidence. Model scores refine
// the projection, but mono predictions cannot collapse a confirmed eye layout.
func (d *FrameDetector) Resolve(fallback Mode, model *ModelScores) Decision {
	mode, decisive := d.Result()
	fisheye := decisive && mode.Fisheye()
	binocular := decisive && mode.Binocular()
	reason := "filename_fallback"
	if decisive {
		reason = "frame_consensus"
	}
	if !decisive || mode == Flat && fallback.IsVR() {
		mode = fallback
		reason = "filename_fallback"
	}
	if fisheye {
		reason = "fisheye_boundary"
	}
	if model != nil && !fisheye {
		if predicted, _, confident := model.Result(); confident {
			// Reliable eye correspondence fixes the binocular layout. A mono
			// prediction may not collapse it, even with a high softmax score.
			if binocular && predicted.IsVR() && !predicted.Binocular() {
				return Decision{mode, "binocular_protection"}
			}
			if model.Samples < projectionConfirmationFrames && (predicted == SBSFisheye || binocular && predicted.Binocular() && predicted != mode) {
				return Decision{mode, "projection_conflict"}
			}
			return Decision{predicted, "model_confident"}
		}
		if mode == Flat {
			if layout, supported := d.modelSupportedLayout(model); supported {
				return Decision{layout, "binocular_layout_consensus"}
			}
			predicted, _, _, agreed := model.BinocularConsensus()
			// This only rescues an otherwise-flat fallback. It cannot replace
			// an established VR subtype or use semantic model votes alone.
			if agreed && d.partialStereoSupports(predicted) {
				if predicted == SBSFisheye && model.Samples < projectionConfirmationFrames {
					return Decision{mode, "projection_conflict"}
				}
				return Decision{predicted, "model_frame_consensus"}
			}
		}
	}
	return Decision{mode, reason}
}

type grayFrame struct {
	w, h   int
	luma   []float64
	detail []float64
}

func grayscale(img image.Image) grayFrame {
	b := img.Bounds()
	g := grayFrame{w: b.Dx(), h: b.Dy(), luma: make([]float64, b.Dx()*b.Dy()), detail: make([]float64, b.Dx()*b.Dy())}
	for y := range g.h {
		for x := range g.w {
			r, green, blue, _ := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
			g.luma[y*g.w+x] = (.2126*float64(r) + .7152*float64(green) + .0722*float64(blue)) / 65535
		}
	}
	for y := 4; y < g.h-4; y++ {
		for x := 4; x < g.w-4; x++ {
			i := y*g.w + x
			g.detail[i] = g.luma[i] - (g.luma[i-4]+g.luma[i+4]+g.luma[i-4*g.w]+g.luma[i+4*g.w])/4
		}
	}
	return g
}

// AnalyzeFrame compares corresponding eyes with an adaptive disparity search. A
// separate high-pass correlation rejects flat gradients and featureless titles.
// Circular masks and panorama seam/pole continuity supply projection evidence.
func AnalyzeFrame(img image.Image, aspect float64) FrameEvidence {
	g := grayscale(img)
	result := FrameEvidence{Mode: Flat, StereoLayout: Flat}
	if g.w < 64 || g.h < 32 {
		result.Rejection = "too_small"
		return result
	}
	var sum, square float64
	bright := 0
	for _, v := range g.luma {
		sum += v
		square += v * v
		if v > .06 {
			bright++
		}
	}
	n := float64(len(g.luma))
	result.MeanLuma, result.Variance, result.BrightFraction = sum/n, square/n-(sum/n)*(sum/n), float64(bright)/n
	if result.BrightFraction < .03 || result.MeanLuma < .015 {
		result.Rejection = "black_frame"
		return result
	}
	if result.Variance < .0002 {
		result.Rejection = "low_detail"
		return result
	}
	result.Usable = true
	if aspect >= 1.75 && aspect <= 2.25 {
		result.StereoLayout = SBS180
		left, right := image.Rect(0, 0, g.w/2, g.h), image.Rect(g.w/2, 0, g.w, g.h)
		fisheye := g.circular(left) && g.circular(right)
		if fisheye {
			// Two visible lens rims establish the packed fisheye format even
			// when near subjects or occlusion defeat stereo correspondence.
			result.Mode = SBSFisheye
			return result
		}
		similarity, matches, wide := g.stereo(left, right)
		result.Similarity = similarity
		result.StereoAgreement = matches
		result.WideDisparity = wide
		if similarity >= .75 && matches >= .4 {
			result.Mode = SBS180
			return result
		}
		// Binocular correspondence takes priority over a smooth seam/pole
		// pattern that can also occur in the background of a stereo clip.
		if aspect >= 1.9 && aspect <= 2.1 && g.panorama() {
			result.Mode = Mono360
			return result
		}
	}
	if aspect >= .85 && aspect <= 1.15 {
		result.StereoLayout = TB360
		if g.circular(image.Rect(0, 0, g.w, g.h)) {
			result.Mode = MonoFisheye
			return result
		}
		top, bottom := image.Rect(0, 0, g.w, g.h/2), image.Rect(0, g.h/2, g.w, g.h)
		similarity, matches, wide := g.stereo(top, bottom)
		result.Similarity = max(result.Similarity, similarity)
		result.StereoAgreement = matches
		result.WideDisparity = wide
		if similarity >= .75 && matches >= .4 {
			result.Mode = TB360
			return result
		}
	}
	return result
}

func (g grayFrame) correlation(a, b image.Rectangle, dx, dy int, values []float64, targetEye image.Rectangle) float64 {
	var sx, sy, xx, yy, xy, n float64
	for y := a.Dy() / 6; y < a.Dy()*5/6; y += 2 {
		for x := a.Dx() / 6; x < a.Dx()*5/6; x += 2 {
			x1, y1 := a.Min.X+x, a.Min.Y+y
			x2, y2 := b.Min.X+x+dx, b.Min.Y+y+dy
			if x2 < targetEye.Min.X+1 || x2 >= targetEye.Max.X-1 || y2 < targetEye.Min.Y+1 || y2 >= targetEye.Max.Y-1 {
				continue
			}
			u, v := values[y1*g.w+x1], values[y2*g.w+x2]
			sx, sy, xx, yy, xy, n = sx+u, sy+v, xx+u*u, yy+v*v, xy+u*v, n+1
		}
	}
	if n < 64 || xx-sx*sx/n < n*.00002 || yy-sy*sy/n < n*.00002 {
		return 0
	}
	return (xy - sx*sy/n) / math.Sqrt((xx-sx*sx/n)*(yy-sy*sy/n))
}

func (g grayFrame) stereo(a, b image.Rectangle) (float64, float64, bool) {
	similarity, matches := g.stereoRange(a, b, a.Dx()/8)
	if similarity >= .75 && matches >= .4 {
		return similarity, matches, false
	}
	// Close subjects can have more than an eighth-eye-width of horizontal
	// disparity. Retry those frames without relaxing correlation thresholds.
	wideSimilarity, wideMatches := g.stereoRange(a, b, a.Dx()/4)
	if wideMatches > matches || wideMatches == matches && wideSimilarity > similarity {
		return wideSimilarity, wideMatches, true
	}
	return similarity, matches, false
}

func (g grayFrame) stereoRange(a, b image.Rectangle, disparity int) (float64, float64) {
	var similarity, matches float64
	// Nearby subjects have different disparity from the background. Match
	// separate patches rather than requiring one translation for the whole eye.
	for row := range 4 {
		for col := range 4 {
			x, y := a.Dx()/10+col*a.Dx()/5, a.Dy()/10+row*a.Dy()/5
			w, h := a.Dx()/5, a.Dy()/5
			pa := image.Rect(a.Min.X+x, a.Min.Y+y, a.Min.X+x+w, a.Min.Y+y+h)
			pb := image.Rect(b.Min.X+x, b.Min.Y+y, b.Min.X+x+w, b.Min.Y+y+h)
			best, bestLuma, bestDetail := 0.0, 0.0, 0.0
			for dy := -max(1, a.Dy()/20); dy <= max(1, a.Dy()/20); dy++ {
				for dx := -disparity; dx <= disparity; dx++ {
					luma := g.correlation(pa, pb, dx, dy, g.luma, b)
					if luma < .5 {
						continue
					}
					detail := g.correlation(pa, pb, dx, dy, g.detail, b)
					score := .7*luma + .3*detail
					if score > best {
						best, bestLuma, bestDetail = score, luma, detail
					}
				}
			}
			if bestLuma >= .72 && bestDetail >= .4 {
				matches++
				similarity += bestLuma
			}
		}
	}
	return similarity / max(1, matches), matches / 16
}

func (g grayFrame) panorama() bool {
	var seam, interior, top, bottom, middle float64
	for y := 1; y < g.h-1; y++ {
		seam += math.Abs(g.luma[y*g.w+1] - g.luma[y*g.w+g.w-2])
		for x := 1; x < g.w-1; x++ {
			diff := math.Abs(g.luma[y*g.w+x+1] - g.luma[y*g.w+x])
			if y < g.h/16 {
				top += diff
			} else if y > g.h*15/16 {
				bottom += diff
			} else if y > g.h/3 && y < g.h*2/3 {
				middle += diff
			}
		}
		interior += math.Abs(g.luma[y*g.w+g.w/5] - g.luma[y*g.w+g.w/2])
	}
	// Do not treat a letterbox's uniform black bands as spherical poles.
	var poleBrightness float64
	for x := range g.w {
		poleBrightness += g.luma[2*g.w+x] + g.luma[(g.h-3)*g.w+x]
	}
	return poleBrightness/float64(2*g.w) > .04 && interior > float64(g.h)*.05 && seam < interior*.15 && middle > .1 && top*16 < middle*3*.4 && bottom*16 < middle*3*.4
}
