package media

/*
#include <stdlib.h>
#include "ffmpeg.h"
*/
import "C"

import (
	"context"
	"errors"
	"unsafe"

	"github.com/foursmith/player/internal/library"
)

// Probe reads container metadata without starting playback or decoding preview frames.
func Probe(ctx context.Context, source string) (library.MediaInfo, error) {
	if err := ctx.Err(); err != nil {
		return library.MediaInfo{}, err
	}
	cancel := &cancellation{ptr: C.fsvr_cancel_new()}
	if cancel.ptr == nil {
		return library.MediaInfo{}, errors.New("cannot allocate media cancellation")
	}
	stop := context.AfterFunc(ctx, cancel.cancel)
	defer cancel.close()
	defer stop()
	name := C.CString(source)
	defer C.free(unsafe.Pointer(name))
	var value C.FsvrInfo
	var message [1024]C.char
	ret := C.fsvr_probe(name, cancel.ptr, &value, &message[0], C.int(len(message)))
	if err := ctx.Err(); err != nil {
		return library.MediaInfo{}, err
	}
	if ret < 0 {
		return library.MediaInfo{}, errors.New(C.GoString(&message[0]))
	}
	return (library.MediaInfo{Duration: float64(value.duration), Width: int(value.width), Height: int(value.height), BitRate: int64(value.bit_rate)}).Normalized(), nil
}
