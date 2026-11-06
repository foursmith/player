package detection

import (
	"errors"
	"image"
	"math"
)

// ImageClassifier accepts an NHWC float32 RGB image and returns softmax scores.
// Model weights may be float16; its input and output must remain float32.
// One worker owns the classifier and its reused buffers.
type ImageClassifier struct {
	ptr           *inference
	width, height int
	input, output []float32
}

func NewImageClassifier(library string, model []byte, classes int) (*ImageClassifier, error) {
	ptr, err := openInference(library, model)
	if err != nil {
		return nil, err
	}
	c := &ImageClassifier{ptr: ptr, height: ptr.size(1), width: ptr.size(2)}
	outputSize, outputErr := ptr.output(0, nil)
	if ptr.size(0) != 1 || ptr.size(3) != 3 || c.width < 1 || c.height < 1 || c.width > 2048 || c.height > 2048 || ptr.outputCount() != 1 || classes < 2 || outputErr != nil || outputSize != classes {
		c.Close()
		return nil, errors.New("incompatible image classifier tensors")
	}
	c.input = make([]float32, c.width*c.height*3)
	c.output = make([]float32, classes)
	return c, nil
}

func (c *ImageClassifier) Close() {
	if c != nil && c.ptr != nil {
		c.ptr.close()
		c.ptr = nil
	}
}

// Classify preserves the entire frame's layout, fitting it on a black canvas.
// The returned buffer is valid until the next call on this classifier.
func (c *ImageClassifier) Classify(img *image.RGBA) ([]float32, error) {
	if c.ptr == nil {
		return nil, errors.New("image classifier closed")
	}
	if img == nil || img.Bounds().Empty() {
		return nil, errors.New("empty classifier image")
	}
	classifierInput(c.input, c.width, c.height, img)
	if err := c.ptr.run(c.input); err != nil {
		return nil, err
	}
	if _, err := c.ptr.output(0, c.output); err != nil {
		return nil, err
	}
	var sum float64
	for _, score := range c.output {
		if math.IsNaN(float64(score)) || math.IsInf(float64(score), 0) || score < 0 || score > 1 {
			return nil, errors.New("invalid classifier probability")
		}
		sum += float64(score)
	}
	if math.Abs(sum-1) > .01 {
		return nil, errors.New("classifier output must contain softmax probabilities")
	}
	return c.output, nil
}

func classifierInput(dst []float32, width, height int, img *image.RGBA) {
	clear(dst)
	b := img.Bounds()
	scale := math.Min(float64(width)/float64(b.Dx()), float64(height)/float64(b.Dy()))
	w, h := max(1, int(math.Round(float64(b.Dx())*scale))), max(1, int(math.Round(float64(b.Dy())*scale)))
	padX, padY := (width-w)/2, (height-h)/2
	for y := range h {
		sy := max(0, min(float64(b.Dy()-1), (float64(y)+.5)*float64(b.Dy())/float64(h)-.5))
		y0 := int(sy)
		y1 := min(b.Dy()-1, y0+1)
		fy := float32(sy - float64(y0))
		for x := range w {
			sx := max(0, min(float64(b.Dx()-1), (float64(x)+.5)*float64(b.Dx())/float64(w)-.5))
			x0 := int(sx)
			x1 := min(b.Dx()-1, x0+1)
			fx := float32(sx - float64(x0))
			at := ((y+padY)*width + x + padX) * 3
			p0, p1 := img.PixOffset(b.Min.X+x0, b.Min.Y+y0), img.PixOffset(b.Min.X+x1, b.Min.Y+y0)
			p2, p3 := img.PixOffset(b.Min.X+x0, b.Min.Y+y1), img.PixOffset(b.Min.X+x1, b.Min.Y+y1)
			for channel := range 3 {
				a, b := float32(img.Pix[p0+channel]), float32(img.Pix[p1+channel])
				v, d := float32(img.Pix[p2+channel]), float32(img.Pix[p3+channel])
				dst[at+channel] = (a*(1-fx)+b*fx)*(1-fy) + (v*(1-fx)+d*fx)*fy
			}
		}
	}
}
