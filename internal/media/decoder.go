package media

/*
#cgo LDFLAGS: -lavformat -lavcodec -lavutil -lavfilter -lswscale -lswresample -lSDL3
#cgo darwin LDFLAGS: -framework VideoToolbox -framework CoreMedia -framework CoreVideo -framework CoreFoundation
#include <stdlib.h>
#include "ffmpeg.h"
*/
import "C"

import (
	"context"
	"errors"
	"image"
	"io"
	"sync"
	"unsafe"
)

type cancellation struct {
	mu  sync.Mutex
	ptr *C.FsvrCancel
}

func (c *cancellation) cancel() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ptr != nil {
		C.fsvr_cancel_set(c.ptr)
	}
}
func (c *cancellation) close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	C.fsvr_cancel_free(c.ptr)
	c.ptr = nil
}

type Decoder struct {
	ptr          *C.FsvrDecoder
	ctx          context.Context
	cancellation *cancellation
	stop         func() bool
}
type Frame struct{ ptr *C.FsvrFrame }
type Source struct {
	URL, Cookies string
	Start        float64
}
type Metadata struct {
	Width, Height       int
	Duration, FrameRate float64
	Decoder             string
}

func Version() string { return C.GoString(C.fsvr_version()) }
func Open(source Source, outputAudio bool) (*Decoder, error) {
	return OpenContext(context.Background(), source, outputAudio)
}
func OpenContext(ctx context.Context, source Source, outputAudio bool) (*Decoder, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cancel := &cancellation{ptr: C.fsvr_cancel_new()}
	if cancel.ptr == nil {
		return nil, errors.New("cannot allocate media cancellation")
	}
	stop := context.AfterFunc(ctx, cancel.cancel)
	name := C.CString(source.URL)
	defer C.free(unsafe.Pointer(name))
	cookies := C.CString(source.Cookies)
	defer C.free(unsafe.Pointer(cookies))
	var message [1024]C.char
	var audio C.int
	if outputAudio {
		audio = 1
	}
	ptr := C.fsvr_open(name, cookies, cancel.ptr, audio, &message[0], C.int(len(message)))
	if ptr == nil {
		stop()
		cancel.close()
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return nil, errors.New(C.GoString(&message[0]))
	}
	d := &Decoder{ptr: ptr, ctx: ctx, cancellation: cancel, stop: stop}
	if err := ctx.Err(); err != nil {
		d.Close()
		return nil, err
	}
	return d, nil
}

func (d *Decoder) failure(message *C.char) error {
	if err := d.ctx.Err(); err != nil {
		return err
	}
	return errors.New(C.GoString(message))
}
func (d *Decoder) Metadata() Metadata {
	return Metadata{int(C.fsvr_width(d.ptr)), int(C.fsvr_height(d.ptr)), float64(C.fsvr_duration(d.ptr)), float64(C.fsvr_fps(d.ptr)), C.GoString(C.fsvr_decoder_name(d.ptr))}
}
func (d *Decoder) Next() (*Frame, error) {
	if err := d.ctx.Err(); err != nil {
		return nil, err
	}
	var ptr *C.FsvrFrame
	var message [1024]C.char
	result := C.fsvr_next(d.ptr, &ptr, &message[0], C.int(len(message)))
	if result < 0 {
		return nil, d.failure(&message[0])
	}
	if result == 0 {
		return nil, io.EOF
	}
	return &Frame{ptr}, nil
}
func (d *Decoder) Seek(seconds float64) error {
	if err := d.ctx.Err(); err != nil {
		return err
	}
	var message [1024]C.char
	if C.fsvr_seek(d.ptr, C.double(seconds), &message[0], C.int(len(message))) < 0 {
		return d.failure(&message[0])
	}
	return nil
}
func (d *Decoder) SetRate(rate float64) error {
	var message [1024]C.char
	if C.fsvr_rate(d.ptr, C.double(rate), &message[0], C.int(len(message))) < 0 {
		return d.failure(&message[0])
	}
	return nil
}

// bufferAudio demuxes ahead without decoding the intervening video packets.
// It runs on the decoder's owner, independently of the decoded-frame queue.
func (d *Decoder) bufferAudio(seconds float64) error {
	if err := d.ctx.Err(); err != nil {
		return err
	}
	var message [1024]C.char
	if C.fsvr_buffer_audio(d.ptr, C.double(seconds), &message[0], C.int(len(message))) < 0 {
		return d.failure(&message[0])
	}
	return nil
}
func (d *Decoder) Pause(paused bool) {
	var p C.int
	if paused {
		p = 1
	}
	C.fsvr_pause(d.ptr, p)
}
func (d *Decoder) Volume(volume float64) { C.fsvr_volume(d.ptr, C.float(volume)) }
func (d *Decoder) Clock() float64        { return float64(C.fsvr_audio_clock(d.ptr)) }
func (d *Decoder) Interrupt()            { C.fsvr_interrupt(d.ptr) }
func (d *Decoder) Close() {
	if d.ptr != nil {
		d.stop()
		C.fsvr_close(d.ptr)
		d.cancellation.close()
		d.ptr = nil
	}
}
func (f *Frame) Clone() *Frame {
	if f == nil || f.ptr == nil {
		return nil
	}
	p := C.fsvr_frame_ref(f.ptr)
	if p == nil {
		return nil
	}
	return &Frame{p}
}
func (f *Frame) Close() {
	if f != nil && f.ptr != nil {
		C.fsvr_frame_free(f.ptr)
		f.ptr = nil
	}
}
func (f *Frame) PTS() float64 { return float64(C.fsvr_frame_pts(f.ptr)) }
func (f *Frame) ID() uint64   { return uint64(C.fsvr_frame_serial(f.ptr)) }
func (f *Frame) Size() (int, int) {
	return int(C.fsvr_frame_width(f.ptr)), int(C.fsvr_frame_height(f.ptr))
}
func (f *Frame) Native() unsafe.Pointer { return C.fsvr_frame_native(f.ptr) }
func (f *Frame) RGBA(width, height int) (*image.RGBA, error) {
	if width < 1 || height < 1 {
		return nil, errors.New("invalid output size")
	}
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	if C.fsvr_frame_rgba(f.ptr, (*C.uchar)(unsafe.Pointer(&img.Pix[0])), C.int(width), C.int(height), C.int(img.Stride)) < 0 {
		return nil, errors.New("FFmpeg frame conversion failed")
	}
	return img, nil
}
