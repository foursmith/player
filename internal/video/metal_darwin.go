package video

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework Cocoa -framework Metal -framework QuartzCore -framework CoreVideo
#include "metal.h"
*/
import "C"
import (
	"errors"
	"fmt"
	"github.com/foursmith/player/internal/media"
	"image"
	"sync"
	"unsafe"
)

type metal struct {
	ptr unsafe.Pointer
	mu  sync.Mutex
}

func New(window uintptr) (Surface, error) {
	var message [4096]C.char
	p := C.fsvr_metal_create(C.uintptr_t(window), &message[0], C.int(len(message)))
	if p == nil {
		return nil, errors.New(C.GoString(&message[0]))
	}
	return &metal{ptr: p}, nil
}
func parameters(p Parameters) C.FsvrParameters {
	portrait := 0
	if p.Portrait {
		portrait = 1
	}
	fov := p.FOV
	if fov <= 0 {
		fov = 80
	}
	return C.FsvrParameters{yaw: C.float(p.View.Yaw), pitch: C.float(p.View.Pitch), zoom: C.float(p.View.Zoom), forward: C.float(p.View.Forward), mode: C.int(p.Mode), split: C.int(max(1, p.SplitCount)), portrait: C.int(portrait), fov: C.float(fov)}
}
func source(f *media.Frame) (unsafe.Pointer, *image.RGBA, error) {
	if p := f.Native(); p != nil {
		return p, nil, nil
	}
	w, h := f.Size()
	img, err := f.RGBA(w, h)
	return nil, img, err
}
func (r *metal) Present(f *media.Frame, p Parameters, w, h, scale float64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.ptr == nil {
		return errors.New("video surface closed")
	}
	native, img, err := source(f)
	if err != nil {
		return err
	}
	var bytes *C.uchar
	if img != nil {
		bytes = (*C.uchar)(unsafe.Pointer(&img.Pix[0]))
	}
	sw, sh := f.Size()
	if code := C.fsvr_metal_present(r.ptr, native, bytes, C.int(sw), C.int(sh), parameters(p), C.double(w), C.double(h), C.double(scale)); code < 0 {
		return fmt.Errorf("Metal presentation: %d", code)
	}
	return nil
}
func (r *metal) Capture(f *media.Frame, p Parameters, w, h int) (*image.RGBA, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.ptr == nil {
		return nil, errors.New("video surface closed")
	}
	native, img, err := source(f)
	if err != nil {
		return nil, err
	}
	var bytes *C.uchar
	if img != nil {
		bytes = (*C.uchar)(unsafe.Pointer(&img.Pix[0]))
	}
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	sw, sh := f.Size()
	if code := C.fsvr_metal_capture(r.ptr, native, bytes, C.int(sw), C.int(sh), parameters(p), C.int(w), C.int(h), (*C.uchar)(unsafe.Pointer(&out.Pix[0]))); code < 0 {
		return nil, fmt.Errorf("Metal capture: %d", code)
	}
	for i := 0; i < len(out.Pix); i += 4 {
		out.Pix[i], out.Pix[i+2] = out.Pix[i+2], out.Pix[i]
	}
	return out, nil
}
func (r *metal) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.ptr != nil {
		C.fsvr_metal_close(r.ptr)
		r.ptr = nil
	}
}
