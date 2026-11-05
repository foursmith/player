// Package detection runs the unchanged BlazeFace models through LiteRT's C API.
package detection

/*
#cgo linux LDFLAGS: -ldl
#include <stdlib.h>
#include "tflite.h"
*/
import "C"
import (
	"errors"
	"fmt"
	"image"
	"math"
	"sort"
	"unsafe"
)

type Face struct{ X, Y, Width, Height, Score float64 }

func (f Face) Center() (float64, float64) { return f.X + f.Width/2, f.Y + f.Height/2 }
func (f Face) Size() float64              { return math.Sqrt(math.Max(0, f.Width*f.Height)) }

type anchor struct{ X, Y float64 }
type Detector struct {
	ptr           *C.FsvrInference
	Width, Height int
	anchors       []anchor
	input         []float32
	outputs       [][]float32
	message       [1024]C.char
}

func New(library string, model []byte) (*Detector, error) {
	if len(model) == 0 {
		return nil, errors.New("empty face model")
	}
	path := C.CString(library)
	defer C.free(unsafe.Pointer(path))
	var message [1024]C.char
	ptr := C.fsvr_inference_open(path, (*C.uchar)(unsafe.Pointer(&model[0])), C.size_t(len(model)), &message[0], C.int(len(message)))
	if ptr == nil {
		return nil, errors.New(C.GoString(&message[0]))
	}
	d := &Detector{ptr: ptr, Height: int(C.fsvr_inference_size(ptr, 1)), Width: int(C.fsvr_inference_size(ptr, 2))}
	if d.Width <= 0 || d.Height <= 0 {
		d.Close()
		return nil, errors.New("invalid face model input")
	}
	d.anchors = anchors(d.Width)
	d.input = make([]float32, d.Width*d.Height*3)
	d.outputs = make([][]float32, int(C.fsvr_inference_output_count(ptr)))
	for i := range d.outputs {
		size := int(C.fsvr_inference_output(ptr, C.int(i), nil, 0))
		d.outputs[i] = make([]float32, size)
	}
	return d, nil
}
func (d *Detector) Close() {
	if d != nil && d.ptr != nil {
		C.fsvr_inference_close(d.ptr)
		d.ptr = nil
	}
}
func anchors(size int) []anchor {
	var result []anchor
	if size == 192 {
		for y := range 48 {
			for x := range 48 {
				result = append(result, anchor{(float64(x) + .5) / 48, (float64(y) + .5) / 48})
			}
		}
		return result
	}
	for _, layer := range []struct{ stride, count int }{{8, 2}, {16, 6}} {
		cells := (size + layer.stride - 1) / layer.stride
		for y := range cells {
			for x := range cells {
				for range layer.count {
					result = append(result, anchor{(float64(x) + .5) / float64(cells), (float64(y) + .5) / float64(cells)})
				}
			}
		}
	}
	return result
}
func (d *Detector) Detect(img *image.RGBA) ([]Face, error) {
	if d.ptr == nil {
		return nil, errors.New("face detector closed")
	}
	if img == nil || img.Bounds().Empty() {
		return nil, errors.New("empty detector image")
	}
	input := d.input
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	scale := math.Min(float64(d.Width)/float64(w), float64(d.Height)/float64(h))
	padX, padY := (float64(d.Width)-float64(w)*scale)/2, (float64(d.Height)-float64(h)*scale)/2
	for y := range d.Height {
		for x := range d.Width {
			sx, sy := (float64(x)+.5-padX)/scale-.5, (float64(y)+.5-padY)/scale-.5
			at := (y*d.Width + x) * 3
			if sx < -0.5 || sy < -0.5 || sx >= float64(w)-.5 || sy >= float64(h)-.5 {
				input[at], input[at+1], input[at+2] = -1, -1, -1
				continue
			}
			x0, y0 := max(0, min(w-1, int(math.Floor(sx)))), max(0, min(h-1, int(math.Floor(sy))))
			x1, y1 := min(w-1, x0+1), min(h-1, y0+1)
			fx, fy := math.Max(0, sx-float64(x0)), math.Max(0, sy-float64(y0))
			for channel := range 3 {
				a := float64(img.Pix[y0*img.Stride+x0*4+channel])
				b := float64(img.Pix[y0*img.Stride+x1*4+channel])
				c := float64(img.Pix[y1*img.Stride+x0*4+channel])
				e := float64(img.Pix[y1*img.Stride+x1*4+channel])
				value := (a*(1-fx)+b*fx)*(1-fy) + (c*(1-fx)+e*fx)*fy
				input[at+channel] = float32(value/127.5 - 1)
			}
		}
	}
	if C.fsvr_inference_run(d.ptr, (*C.float)(unsafe.Pointer(&input[0])), C.size_t(len(input)), &d.message[0], C.int(len(d.message))) < 0 {
		return nil, errors.New(C.GoString(&d.message[0]))
	}
	var boxes, scores []float32
	for i, output := range d.outputs {
		size := len(output)
		if size == 0 {
			continue
		}
		if C.fsvr_inference_output(d.ptr, C.int(i), (*C.float)(unsafe.Pointer(&output[0])), C.size_t(size)) == 0 {
			return nil, errors.New("cannot read face inference output")
		}
		if size == len(d.anchors)*16 {
			boxes = output
		} else if size == len(d.anchors) {
			scores = output
		}
	}
	if boxes == nil || scores == nil {
		return nil, fmt.Errorf("unexpected BlazeFace outputs for %dx%d", d.Width, d.Height)
	}
	var faces []Face
	for i, score := range scores {
		confidence := 1 / (1 + math.Exp(-math.Max(-100, math.Min(100, float64(score)))))
		if confidence < .6 {
			continue
		}
		a := d.anchors[i]
		box := boxes[i*16:]
		cx := (float64(box[0]) + a.X*float64(d.Width) - padX) / scale / float64(w)
		cy := (float64(box[1]) + a.Y*float64(d.Height) - padY) / scale / float64(h)
		bw := float64(box[2]) / scale / float64(w)
		bh := float64(box[3]) / scale / float64(h)
		faces = append(faces, Face{cx - bw/2, cy - bh/2, bw, bh, confidence})
	}
	faces = weightedNMS(faces, .45)
	for i := range faces {
		f := &faces[i]
		f.X = math.Round(f.X*float64(w)) / float64(w)
		f.Y = math.Round(f.Y*float64(h)) / float64(h)
		f.Width = math.Round(f.Width*float64(w)) / float64(w)
		f.Height = math.Round(f.Height*float64(h)) / float64(h)
	}
	sort.SliceStable(faces, func(i, j int) bool { return faces[i].Width*faces[i].Height > faces[j].Width*faces[j].Height })
	return faces[:min(8, len(faces))], nil
}
func intersectionOverUnion(a, b Face) float64 {
	x := math.Max(0, math.Min(a.X+a.Width, b.X+b.Width)-math.Max(a.X, b.X))
	y := math.Max(0, math.Min(a.Y+a.Height, b.Y+b.Height)-math.Max(a.Y, b.Y))
	intersection := x * y
	return intersection / math.Max(1e-12, a.Width*a.Height+b.Width*b.Height-intersection)
}
func weightedNMS(faces []Face, threshold float64) []Face {
	sort.SliceStable(faces, func(i, j int) bool { return faces[i].Score > faces[j].Score })
	var output []Face
	for len(faces) > 0 {
		best := faces[0]
		var rest []Face
		x, y := best.X*best.Score, best.Y*best.Score
		w, h, weight := best.Width*best.Score, best.Height*best.Score, best.Score
		// Consume the seed even when its area is zero and its self-IoU is 0.
		for _, f := range faces[1:] {
			if intersectionOverUnion(best, f) > threshold {
				x += f.X * f.Score
				y += f.Y * f.Score
				w += f.Width * f.Score
				h += f.Height * f.Score
				weight += f.Score
			} else {
				rest = append(rest, f)
			}
		}
		if weight > 0 {
			output = append(output, Face{x / weight, y / weight, w / weight, h / weight, best.Score})
		}
		faces = rest
	}
	return output
}
