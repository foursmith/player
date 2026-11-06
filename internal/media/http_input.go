package media

import (
	"errors"
	"io"
	"math"
	"strconv"
	"strings"
	"unsafe"

	"github.com/ebitengine/purego"
)

const httpShortSeek = 1 << 20

// Keep two native HTTP cursors so distant tracks can continue on their own
// response. FFmpeg owns the byte buffers.
type httpInput struct {
	address        string
	interrupt      avInterrupt
	buffered       *avIO
	cursors        [2]*avIO
	active         int
	position, size int64
}

var httpReadCallback = purego.NewCallback(httpRead)
var httpSeekCallback = purego.NewCallback(httpSeek)

func isHTTP(address string) bool {
	scheme, _, _ := strings.Cut(address, ":")
	return scheme == "http" || scheme == "https"
}

func (input *httpInput) openCursor() (*avIO, int32) {
	var options unsafe.Pointer
	av.util.DictSet(&options, "rw_timeout", "10000000", 0)
	av.util.DictSet(&options, "short_seek_size", strconv.Itoa(httpShortSeek), 0)
	var source *avIO
	ret := av.io.Open(&source, input.address, avIORead, &input.interrupt, &options)
	av.util.DictFree(&options)
	if ret < 0 && source != nil {
		av.io.Close(&source)
	}
	return source, ret
}

func (d *Decoder) openHTTP(address string) error {
	input := &httpInput{address: address, interrupt: d.format.InterruptCallback}
	d.cancellation.input = input
	source, ret := input.openCursor()
	if ret < 0 {
		return d.failure(ret)
	}
	input.cursors[0] = source
	d.format.Pb = source
	// Keep ownership even if allocation fails before avformat_open_input.
	d.format.Flags |= avCustomIO
	input.size = av.io.Size(source)
	if source.Seekable&avIOSeekable == 0 || input.size <= 0 {
		return nil
	}
	buffer := av.util.Malloc(32 << 10)
	if buffer == nil {
		return errors.New("cannot allocate HTTP input buffer")
	}
	input.buffered = av.io.Alloc(buffer, 32<<10, 0, d.cancellation.token, httpReadCallback, 0, httpSeekCallback)
	if input.buffered == nil {
		av.util.Free(buffer)
		return errors.New("cannot allocate HTTP input context")
	}
	d.format.Pb = input.buffered
	return nil
}

func httpRead(token uintptr, buffer *byte, size int32) uintptr {
	value, ok := callbacks.Load(token)
	code := int32(avExit)
	if ok {
		cancel := value.(*cancellation)
		if cancel.value.Load() == 0 && cancel.input != nil {
			code = cancel.input.read(buffer, size)
		}
	}
	return uintptr(uint32(code))
}

func httpSeek(token uintptr, offset int64, whence int32) uintptr {
	value, ok := callbacks.Load(token)
	if !ok {
		return ^uintptr(0)
	}
	cancel := value.(*cancellation)
	if cancel.value.Load() != 0 || cancel.input == nil {
		return ^uintptr(0)
	}
	return uintptr(cancel.input.seek(offset, whence))
}

func (input *httpInput) seek(offset int64, whence int32) int64 {
	whence &^= avSeekForce
	if whence == avSeekSize {
		return input.size
	}
	var base int64
	switch whence {
	case io.SeekStart:
	case io.SeekCurrent:
		base = input.position
	case io.SeekEnd:
		base = input.size
	default:
		return -1
	}
	if offset < -base || offset > math.MaxInt64-base {
		return -1
	}
	input.position = base + offset
	return input.position
}

func (input *httpInput) cursor() (*avIO, int32) {
	nearest := -1
	distance := int64(math.MaxInt64)
	for i, source := range input.cursors {
		if source == nil {
			continue
		}
		start := source.Pos - int64(uintptr(source.BufEnd)-uintptr(source.Buffer))
		if input.position >= start && input.position <= source.Pos {
			input.active = i
			return source, 0
		}
		at := source.Pos - int64(uintptr(source.BufEnd)-uintptr(source.BufPtr))
		if delta := input.position - at; delta >= 0 && delta <= httpShortSeek && delta < distance {
			nearest, distance = i, delta
		}
	}
	if nearest < 0 {
		// Preserve the last-used response when switching to a distant track.
		nearest = 1 - input.active
		if input.cursors[nearest] == nil {
			source, ret := input.openCursor()
			if ret < 0 {
				return nil, ret
			}
			input.cursors[nearest] = source
		}
	}
	input.active = nearest
	return input.cursors[nearest], 0
}

func (input *httpInput) read(buffer *byte, size int32) int32 {
	if input.position >= input.size {
		return avEOF
	}
	cursor, ret := input.cursor()
	if ret < 0 {
		return ret
	}
	if at := av.io.Seek(cursor, input.position, io.SeekStart); at != input.position {
		if at < 0 {
			return int32(at)
		}
		return avExternal
	}
	// Partial reads retain AVIO's native buffer for small backward seeks.
	// Writing into FFmpeg's destination avoids copying through a Go byte cache.
	ret = av.io.Partial(cursor, buffer, size)
	if ret > 0 {
		input.position += int64(ret)
	} else if ret == 0 {
		ret = avEOF
	}
	return ret
}

func (input *httpInput) close() {
	if input.buffered != nil {
		av.util.Free(input.buffered.Buffer)
		av.io.Free(&input.buffered)
	}
	for i := range input.cursors {
		if input.cursors[i] != nil {
			av.io.Close(&input.cursors[i])
		}
	}
}
