package media

import (
	"reflect"
	"testing"
	"unsafe"

	"github.com/ebitengine/purego"
)

func TestFFmpegCallbackWindowsSignatures(t *testing.T) {
	// Windows compiles callbacks at runtime, so a cross-link check cannot find
	// an invalid signature. Check that contract even on non-Windows CI hosts.
	for name, callback := range map[string]any{"interrupt": mediaInterrupt, "pixel format": hardwarePixelFormat} {
		t.Run(name, func(t *testing.T) {
			typeOf := reflect.TypeOf(callback)
			if typeOf.NumOut() != 1 || typeOf.Out(0).Kind() != reflect.Uintptr {
				t.Fatal("Windows callbacks must return one uintptr-sized result")
			}
			for i := range typeOf.NumIn() {
				if typeOf.In(i).Size() > unsafe.Sizeof(uintptr(0)) {
					t.Fatalf("argument %d exceeds Windows callback register size", i)
				}
			}
		})
	}
}

func TestFFmpegCallbacksPreserveCIntResults(t *testing.T) {
	// Invoke the native trampolines with FFmpeg's actual int32 return types,
	// rather than just calling the Go functions. This covers signed -1 as well
	// as cancellation and the selected hardware pixel format.
	var interrupt func(uintptr) int32
	purego.RegisterFunc(&interrupt, interruptCallback)
	cancel := newCancellation()
	defer cancel.close()
	if got := interrupt(cancel.token); got != 0 {
		t.Fatalf("uncancelled input: %d", got)
	}
	cancel.cancel()
	if got := interrupt(cancel.token); got != 1 {
		t.Fatalf("cancelled input: %d", got)
	}
	if got := interrupt(0); got != 1 {
		t.Fatalf("unknown input: %d", got)
	}
	var format func(*avCodecContext, *int32) int32
	purego.RegisterFunc(&format, hardwareCallback)
	formats := [...]int32{42, 7, -1}
	ctx := &avCodecContext{Opaque: cancel.token}
	cancel.hardwareFormat.Store(42)
	if got := format(ctx, &formats[0]); got != 42 {
		t.Fatalf("hardware pixel format: %d", got)
	}
	ctx.Opaque = 0
	if got := format(ctx, &formats[0]); got != -1 {
		t.Fatalf("AV_PIX_FMT_NONE lost its sign: %d", got)
	}
}
