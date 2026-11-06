package media

import (
	"runtime"
	"syscall"
	"unsafe"
)

// D3D11Texture borrows a decoder texture while its Frame remains alive. All
// immediate-context commands, including copies and rendering, must run between
// Lock and Unlock: FFmpeg uses the same recursive device lock for decoding.
type D3D11Texture struct {
	Device, Context, Texture uintptr
	Subresource              uint32
	Colorspace, ColorRange   int32
	lock, unlock, opaque     uintptr
}

// AVD3D11VADeviceContext, from FFmpeg 9's hwcontext_d3d11va.h on Windows 64-bit.
type avD3D11Device struct {
	Device, Context, VideoDevice, VideoContext uintptr
	Lock, Unlock, Opaque                       uintptr
	BindFlags, MiscFlags                       uint32
}

func (f *Frame) D3D11Texture() (D3D11Texture, bool) {
	if f == nil || f.ptr == nil || f.ptr.HwFramesCtx == nil || f.ptr.Format != av.util.PixelFormat("d3d11") {
		return D3D11Texture{}, false
	}
	frames := (*avHWFrames)(unsafe.Pointer(f.ptr.HwFramesCtx.Data))
	if frames.DeviceCtx == nil || frames.DeviceCtx.Hwctx == nil {
		return D3D11Texture{}, false
	}
	d := (*avD3D11Device)(frames.DeviceCtx.Hwctx)
	return D3D11Texture{
		Device: d.Device, Context: d.Context, Texture: uintptr(unsafe.Pointer(f.ptr.Data[0])),
		Subresource: uint32(uintptr(unsafe.Pointer(f.ptr.Data[1]))),
		Colorspace:  f.ptr.Colorspace, ColorRange: f.ptr.ColorRange,
		lock: d.Lock, unlock: d.Unlock, opaque: d.Opaque,
	}, d.Device != 0 && d.Context != 0 && d.Lock != 0 && d.Unlock != 0
}

func (t D3D11Texture) Lock() {
	runtime.LockOSThread()
	syscall.SyscallN(t.lock, t.opaque)
}
func (t D3D11Texture) Unlock() {
	syscall.SyscallN(t.unlock, t.opaque)
	runtime.UnlockOSThread()
}
