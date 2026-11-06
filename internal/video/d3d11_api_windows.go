package video

import (
	"fmt"
	"sync"
	"syscall"
	"unsafe"

	"github.com/foursmith/player/internal/native"
	"golang.org/x/sys/windows"
)

var d3dLibrary = windows.NewLazySystemDLL("d3d11.dll")
var d3dCompile = windows.NewLazySystemDLL("d3dcompiler_47.dll").NewProc("D3DCompile")
var dcompCreate = windows.NewLazySystemDLL("dcomp.dll").NewProc("DCompositionCreateDevice")
var d3dFindWindow = windows.NewLazySystemDLL("user32.dll").NewProc("FindWindowExW")

// MyGo v0.7.4 draws native UI on this child HWND, rather than NativeHandle's
// top-level HWND (internal/windows/surface.go). Its top composition tree owns
// the UI. The video must use the bottom tree of the same child: a tree on the
// parent is clipped by the child's rectangle, including transparent UI pixels.
func d3dVideoHost(window uintptr) (uintptr, error) {
	class, _ := syscall.UTF16PtrFromString("MyGoSurface")
	host, _, _ := d3dFindWindow.Call(window, 0, uintptr(unsafe.Pointer(class)), 0)
	if host == 0 {
		return 0, fmt.Errorf("D3D11: MyGo content window is unavailable")
	}
	return host, nil
}

var dxgiDevice = windows.GUID{Data1: 0x54ec77fa, Data2: 0x1377, Data3: 0x44e6, Data4: [8]byte{0x8c, 0x32, 0x88, 0xfd, 0x5f, 0x44, 0xc8, 0x4c}}
var dxgiFactory2 = windows.GUID{Data1: 0x50c83a1c, Data2: 0xe072, Data3: 0x4c48, Data4: [8]byte{0x87, 0xb0, 0x36, 0x30, 0xfa, 0x36, 0xa6, 0xd0}}
var d3dTexture2D = windows.GUID{Data1: 0x6f15aaf2, Data2: 0xd208, Data3: 0x4e89, Data4: [8]byte{0x9a, 0xb4, 0x48, 0x95, 0x35, 0xd3, 0x4f, 0x9c}}
var compositionDevice = windows.GUID{Data1: 0xc37ea93a, Data2: 0xe7aa, Data3: 0x450d, Data4: [8]byte{0xb1, 0x6f, 0x97, 0x46, 0xcb, 0x04, 0x07, 0xf3}}

// COM method indices and scalar layouts follow the Windows SDK's d3d11.h,
// dxgi1_2.h and dcomp.h. Keep pointers passed through SyscallN alive until return.
//
//go:uintptrescapes
func d3dCall(object uintptr, method int, args ...uintptr) uintptr {
	vt := *(*uintptr)(native.Pointer(object))
	fn := *(*uintptr)(unsafe.Add(native.Pointer(vt), method*int(unsafe.Sizeof(uintptr(0)))))
	all := make([]uintptr, len(args)+1)
	all[0] = object
	copy(all[1:], args)
	result, _, _ := syscall.SyscallN(fn, all...)
	return result
}

func d3dCheck(stage string, hr uintptr) error {
	if int32(uint32(hr)) < 0 {
		return fmt.Errorf("D3D11 %s: HRESULT 0x%08x", stage, uint32(hr))
	}
	return nil
}

func d3dFree(objects ...*uintptr) {
	for _, object := range objects {
		if *object != 0 {
			d3dCall(*object, 2)
			*object = 0
		}
	}
}

type d3dTextureDesc struct {
	Width, Height, Mips, Array, Format uint32
	Samples, Quality                   uint32
	Usage, Bind, CPUAccess, Misc       uint32
}
type d3dBufferDesc struct{ Size, Usage, Bind, CPUAccess, Misc, Stride uint32 }
type d3dMapped struct {
	Data                 uintptr
	RowPitch, DepthPitch uint32
}
type d3dViewport struct{ X, Y, Width, Height, MinDepth, MaxDepth float32 }
type d3dRasterizer struct {
	Fill, Cull                               uint32
	FrontCCW, DepthBias                      int32
	DepthBiasClamp, SlopeDepthBias           float32
	DepthClip, Scissor, Multisample, AALines int32
}
type d3dSampler struct {
	Filter, U, V, W        uint32
	MipBias                float32
	Anisotropy, Comparison uint32
	Border                 [4]float32
	MinLOD, MaxLOD         float32
}
type d3dViewDesc struct {
	Format, Dimension uint32
	Data              [4]uint32
}
type d3dSwapDesc struct {
	Width, Height, Format                                         uint32
	Stereo                                                        int32
	Samples, Quality, Usage, Count, Scaling, Effect, Alpha, Flags uint32
}

var d3dShaders = sync.OnceValues(func() (code [2][]byte, err error) {
	code[0], err = compileD3DShader("vertexMain", "vs_4_0")
	if err == nil {
		code[1], err = compileD3DShader("fragmentMain", "ps_4_0")
	}
	return
})

func compileD3DShader(entry, target string) ([]byte, error) {
	if err := d3dCompile.Find(); err != nil {
		return nil, err
	}
	name, _ := syscall.BytePtrFromString("projection.hlsl")
	e, _ := syscall.BytePtrFromString(entry)
	t, _ := syscall.BytePtrFromString(target)
	source := []byte(d3dSource)
	var blob, messages uintptr
	hr, _, _ := d3dCompile.Call(uintptr(unsafe.Pointer(&source[0])), uintptr(len(source)), uintptr(unsafe.Pointer(name)),
		0, 0, uintptr(unsafe.Pointer(e)), uintptr(unsafe.Pointer(t)), 1<<15|1<<13, 0,
		uintptr(unsafe.Pointer(&blob)), uintptr(unsafe.Pointer(&messages)))
	defer d3dFree(&blob, &messages)
	if err := d3dCheck("compile "+entry, hr); err != nil {
		if messages != 0 {
			return nil, fmt.Errorf("%w: %s", err, d3dBlob(messages))
		}
		return nil, err
	}
	return append([]byte(nil), d3dBlob(blob)...), nil
}

func d3dBlob(blob uintptr) []byte {
	data, size := d3dCall(blob, 3), d3dCall(blob, 4)
	return unsafe.Slice((*byte)(native.Pointer(data)), size)
}
