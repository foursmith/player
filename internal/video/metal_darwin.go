package video

import (
	_ "embed"
	"errors"
	"fmt"
	"image"
	"runtime"
	"structs"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego/objc"
	"github.com/foursmith/player/internal/apple"
	"github.com/foursmith/player/internal/media"
	"github.com/foursmith/player/internal/native"
)

//go:embed projection.metal
var metalSource string

const TransparentWindow = false

// Scalar layout shared with Params in projection.metal.
type metalParameters struct {
	Yaw, Pitch, Zoom, Forward                  float32
	Mode, Split, Portrait, RGBA                int32
	Aspect, Width, Height, FOV                 float32
	KR, KB, YOffset, YScale, UVOffset, UVScale float32
	FlatX, FlatY, FlatZoom                     float32
}
type cgSize struct {
	_             structs.HostLayout
	Width, Height float64
}
type cgRect struct {
	_                   structs.HostLayout
	X, Y, Width, Height float64
}
type mtlRegion struct {
	_                             structs.HostLayout
	X, Y, Z, Width, Height, Depth uintptr
}
type mtlColor struct {
	_          structs.HostLayout
	R, G, B, A float64
}

var metalAPI struct{ Device func() objc.ID }
var textureAPI struct {
	Cache   func(uintptr, uintptr, objc.ID, uintptr, *uintptr) int32
	Texture func(uintptr, uintptr, uintptr, uintptr, uintptr, uintptr, uintptr, uintptr, *uintptr) int32
	Get     func(uintptr) objc.ID
}
var matrixKeys struct{ Matrix, BT601, BT2020 uintptr }
var metalSelectors sync.Map

func selector(name string) objc.SEL {
	if value, ok := metalSelectors.Load(name); ok {
		return value.(objc.SEL)
	}
	value, _ := metalSelectors.LoadOrStore(name, objc.RegisterName(name))
	return value.(objc.SEL)
}
func send(id objc.ID, name string, args ...any) objc.ID { return id.Send(selector(name), args...) }
func class(name string) objc.ID                         { return objc.ID(objc.GetClass(name)) }
func metalPool() func() {
	// Autorelease pools belong to a thread, including in offscreen workers.
	runtime.LockOSThread()
	pool := send(send(class("NSAutoreleasePool"), "alloc"), "init")
	return func() { send(pool, "drain"); runtime.UnlockOSThread() }
}
func metalError(errorObject objc.ID, stage string) error {
	if errorObject != 0 {
		return fmt.Errorf("Metal %s: %s", stage, objc.Send[string](send(errorObject, "localizedDescription"), selector("UTF8String")))
	}
	return fmt.Errorf("Metal %s failed", stage)
}

var loadMetal = sync.OnceValue(func() error {
	if err := apple.Load(); err != nil {
		return err
	}
	for _, name := range []string{"AppKit", "QuartzCore"} {
		if _, err := apple.Framework(name); err != nil {
			return err
		}
	}
	h, err := apple.Framework("Metal")
	if err != nil {
		return err
	}
	if err := native.Bind(h, &metalAPI, "MTLCreateSystemDefaultDevice"); err != nil {
		return err
	}
	cv, err := apple.Framework("CoreVideo")
	if err != nil {
		return err
	}
	if err := native.Bind(cv, &textureAPI, "CVMetalTextureCacheCreate", "CVMetalTextureCacheCreateTextureFromImage", "CVMetalTextureGetTexture"); err != nil {
		return err
	}
	for _, item := range []struct {
		dst  *uintptr
		name string
	}{{&matrixKeys.Matrix, "kCVImageBufferYCbCrMatrixKey"}, {&matrixKeys.BT601, "kCVImageBufferYCbCrMatrix_ITU_R_601_4"}, {&matrixKeys.BT2020, "kCVImageBufferYCbCrMatrix_ITU_R_2020"}} {
		if *item.dst, err = apple.Constant(cv, item.name); err != nil {
			return err
		}
	}
	return nil
})

type metal struct {
	mu                                            sync.Mutex
	device, queue, pipeline, layer, view, capture objc.ID
	cache                                         uintptr
}

func New(window uintptr) (_ Surface, err error) {
	if err := loadMetal(); err != nil {
		return nil, err
	}
	defer metalPool()()
	r := &metal{device: metalAPI.Device()}
	defer func() {
		if err != nil {
			r.Close()
		}
	}()
	if r.device == 0 {
		return nil, errors.New("Metal device unavailable")
	}
	r.queue = send(r.device, "newCommandQueue")
	if r.queue == 0 {
		return nil, errors.New("Metal command queue unavailable")
	}
	source := send(class("NSString"), "stringWithUTF8String:", metalSource)
	var nativeError objc.ID
	library := send(r.device, "newLibraryWithSource:options:error:", source, objc.ID(0), &nativeError)
	if library == 0 {
		return nil, metalError(nativeError, "shader compilation")
	}
	defer send(library, "release")
	vertex := send(library, "newFunctionWithName:", send(class("NSString"), "stringWithUTF8String:", "vertexMain"))
	fragment := send(library, "newFunctionWithName:", send(class("NSString"), "stringWithUTF8String:", "fragmentMain"))
	defer send(vertex, "release")
	defer send(fragment, "release")
	descriptor := send(class("MTLRenderPipelineDescriptor"), "new")
	defer send(descriptor, "release")
	send(descriptor, "setVertexFunction:", vertex)
	send(descriptor, "setFragmentFunction:", fragment)
	send(send(send(descriptor, "colorAttachments"), "objectAtIndexedSubscript:", uintptr(0)), "setPixelFormat:", uintptr(80))
	r.pipeline = send(r.device, "newRenderPipelineStateWithDescriptor:error:", descriptor, &nativeError)
	if r.pipeline == 0 {
		return nil, metalError(nativeError, "pipeline creation")
	}
	if status := textureAPI.Cache(0, 0, r.device, 0, &r.cache); status != 0 {
		return nil, fmt.Errorf("CVMetalTextureCacheCreate: %d", status)
	}
	if window != 0 {
		r.view = send(send(objc.ID(window), "contentView"), "retain")
		send(r.view, "setWantsLayer:", true)
		r.layer = send(send(class("CAMetalLayer"), "layer"), "retain")
		send(r.layer, "setDevice:", r.device)
		send(r.layer, "setPixelFormat:", uintptr(80))
		send(r.layer, "setFramebufferOnly:", true)
		send(r.layer, "setOpaque:", true)
		send(r.layer, "setAutoresizingMask:", uintptr(18))
		send(r.layer, "setFrame:", objc.Send[cgRect](r.view, selector("bounds")))
		send(send(r.view, "layer"), "insertSublayer:atIndex:", r.layer, uint32(0))
	}
	return r, nil
}
func parameters(p Parameters, aspect, sourceAspect float64) metalParameters {
	fov := p.FOV
	if fov <= 0 {
		fov = 80
	}
	flat := p.FlatView.Constrain(aspect, sourceAspect)
	portrait := int32(0)
	if p.Portrait {
		portrait = 1
	}
	return metalParameters{Yaw: float32(p.View.Yaw), Pitch: float32(p.View.Pitch), Zoom: float32(p.View.Zoom), Forward: float32(p.View.Forward), Mode: int32(p.Mode), Split: int32(max(1, p.SplitCount)), Portrait: portrait, FOV: float32(fov), FlatX: float32(flat.X), FlatY: float32(flat.Y), FlatZoom: float32(flat.Zoom)}
}
func source(f *media.Frame) (uintptr, *image.RGBA, error) {
	if p := f.Native(); p != nil {
		return uintptr(p), nil, nil
	}
	w, h := f.Size()
	img, err := f.RGBA(w, h)
	return 0, img, err
}
func textureDescriptor(format uintptr, width, height int, usage uintptr) objc.ID {
	desc := send(class("MTLTextureDescriptor"), "texture2DDescriptorWithPixelFormat:width:height:mipmapped:", format, uintptr(width), uintptr(height), false)
	send(desc, "setStorageMode:", uintptr(0))
	send(desc, "setUsage:", usage)
	return desc
}

func (r *metal) render(f *media.Frame, p Parameters, output, drawable objc.ID, readback *image.RGBA) error {
	buffer, img, err := source(f)
	if err != nil {
		return err
	}
	sw, sh := f.Size()
	width, height := int(send(output, "width")), int(send(output, "height"))
	params := parameters(p, float64(width)/float64(height), float64(sw)/float64(sh))
	params.Aspect, params.Width, params.Height = float32(sw)/float32(sh), float32(width), float32(height)
	var luma, chroma uintptr
	var ytex, uvtex objc.ID
	if buffer != 0 {
		format := apple.Video.Format(buffer)
		full := format == apple.NV12Full || format == apple.P010Full
		ten := format == apple.P010Video || format == apple.P010Full
		params.KR, params.KB = .2126, .0722
		matrix := apple.Video.Attachment(buffer, matrixKeys.Matrix, nil)
		if matrix != 0 {
			if apple.Core.Equal(matrix, matrixKeys.BT601) {
				params.KR, params.KB = .299, .114
			} else if apple.Core.Equal(matrix, matrixKeys.BT2020) {
				params.KR, params.KB = .2627, .0593
			}
			apple.Core.Release(matrix)
		}
		params.YOffset, params.YScale, params.UVOffset, params.UVScale = 16.0/255, 255.0/219, 128.0/255, 255.0/224
		if ten {
			params.YOffset, params.YScale, params.UVOffset, params.UVScale = 4096.0/65535, 65535.0/56064, 32768.0/65535, 65535.0/57344
		}
		if full {
			params.YOffset, params.YScale, params.UVScale = 0, 1, 1
			if ten {
				params.YScale, params.UVScale = 65535.0/65472, 65535.0/65472
			}
		}
		if apple.Video.PlaneCount(buffer) < 2 {
			return errors.New("Metal video frame requires two planes")
		}
		yformat, uvformat := uintptr(10), uintptr(30)
		if ten {
			yformat, uvformat = 20, 60
		}
		if status := textureAPI.Texture(0, r.cache, buffer, 0, yformat, apple.Video.PlaneWidth(buffer, 0), apple.Video.PlaneHeight(buffer, 0), 0, &luma); status != 0 {
			return fmt.Errorf("Metal luma texture: %d", status)
		}
		if status := textureAPI.Texture(0, r.cache, buffer, 0, uvformat, apple.Video.PlaneWidth(buffer, 1), apple.Video.PlaneHeight(buffer, 1), 1, &chroma); status != 0 {
			apple.Core.Release(luma)
			return fmt.Errorf("Metal chroma texture: %d", status)
		}
		ytex, uvtex = textureAPI.Get(luma), textureAPI.Get(chroma)
	} else {
		ytex = send(r.device, "newTextureWithDescriptor:", textureDescriptor(70, sw, sh, 1))
		if ytex == 0 {
			return errors.New("Metal RGBA texture unavailable")
		}
		defer send(ytex, "release")
		send(ytex, "replaceRegion:mipmapLevel:withBytes:bytesPerRow:", mtlRegion{Width: uintptr(sw), Height: uintptr(sh), Depth: 1}, uintptr(0), unsafe.Pointer(&img.Pix[0]), uintptr(img.Stride))
		uvtex = ytex
		params.RGBA = 1
	}
	pass := send(class("MTLRenderPassDescriptor"), "renderPassDescriptor")
	attachment := send(send(pass, "colorAttachments"), "objectAtIndexedSubscript:", uintptr(0))
	send(attachment, "setTexture:", output)
	send(attachment, "setLoadAction:", uintptr(2))
	send(attachment, "setStoreAction:", uintptr(1))
	send(attachment, "setClearColor:", mtlColor{A: 1})
	command := send(r.queue, "commandBuffer")
	encoder := send(command, "renderCommandEncoderWithDescriptor:", pass)
	if encoder == 0 {
		if luma != 0 {
			apple.Core.Release(luma)
		}
		if chroma != 0 {
			apple.Core.Release(chroma)
		}
		return errors.New("Metal render encoder unavailable")
	}
	send(encoder, "setRenderPipelineState:", r.pipeline)
	send(encoder, "setFragmentBytes:length:atIndex:", unsafe.Pointer(&params), unsafe.Sizeof(params), uintptr(0))
	send(encoder, "setFragmentTexture:atIndex:", ytex, uintptr(0))
	send(encoder, "setFragmentTexture:atIndex:", uvtex, uintptr(1))
	send(encoder, "drawPrimitives:vertexStart:vertexCount:", uintptr(3), uintptr(0), uintptr(3))
	send(encoder, "endEncoding")
	if drawable != 0 {
		send(command, "presentDrawable:", drawable)
	}
	if buffer != 0 {
		// The frame can close as soon as Present returns, while the GPU still reads
		// its two CVMetalTextures. The command owns all three CF references.
		apple.Core.Retain(buffer)
		completed := objc.NewBlock(func(_ objc.Block, _ objc.ID) {
			apple.Core.Release(luma)
			apple.Core.Release(chroma)
			apple.Core.Release(buffer)
		})
		send(command, "addCompletedHandler:", completed)
		completed.Release()
	}
	send(command, "commit")
	if readback != nil {
		send(command, "waitUntilCompleted")
		if send(command, "status") == 5 {
			return metalError(send(command, "error"), "capture")
		}
		send(output, "getBytes:bytesPerRow:fromRegion:mipmapLevel:", unsafe.Pointer(&readback.Pix[0]), uintptr(readback.Stride), mtlRegion{Width: uintptr(width), Height: uintptr(height), Depth: 1}, uintptr(0))
	}
	return nil
}
func (r *metal) Present(f *media.Frame, p Parameters, w, h, scale float64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.device == 0 {
		return errors.New("video surface closed")
	}
	if r.layer == 0 {
		return errors.New("video surface has no window")
	}
	if w <= 0 || h <= 0 || scale <= 0 {
		return errors.New("invalid video surface size")
	}
	defer metalPool()()
	transaction := class("CATransaction")
	send(transaction, "begin")
	send(transaction, "setDisableActions:", true)
	send(r.layer, "setFrame:", objc.Send[cgRect](r.view, selector("bounds")))
	send(r.layer, "setContentsScale:", scale)
	send(r.layer, "setDrawableSize:", cgSize{Width: w * scale, Height: h * scale})
	send(transaction, "commit")
	drawable := send(r.layer, "nextDrawable")
	if drawable == 0 {
		return nil
	}
	return r.render(f, p, send(drawable, "texture"), drawable, nil)
}
func (r *metal) Capture(f *media.Frame, p Parameters, w, h int) (*image.RGBA, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.device == 0 {
		return nil, errors.New("video surface closed")
	}
	if w <= 0 || h <= 0 {
		return nil, errors.New("invalid video capture size")
	}
	defer metalPool()()
	if r.capture == 0 || int(send(r.capture, "width")) != w || int(send(r.capture, "height")) != h {
		if r.capture != 0 {
			send(r.capture, "release")
		}
		r.capture = send(r.device, "newTextureWithDescriptor:", textureDescriptor(80, w, h, 4))
	}
	if r.capture == 0 {
		return nil, errors.New("Metal capture texture unavailable")
	}
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	if err := r.render(f, p, r.capture, 0, out); err != nil {
		return nil, err
	}
	for i := 0; i < len(out.Pix); i += 4 {
		out.Pix[i], out.Pix[i+2] = out.Pix[i+2], out.Pix[i]
	}
	return out, nil
}
func (r *metal) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.device == 0 {
		return
	}
	defer metalPool()()
	send(r.layer, "removeFromSuperlayer")
	for _, object := range []*objc.ID{&r.capture, &r.layer, &r.view, &r.pipeline, &r.queue, &r.device} {
		if *object != 0 {
			send(*object, "release")
			*object = 0
		}
	}
	if r.cache != 0 {
		apple.Core.Release(r.cache)
		r.cache = 0
	}
}
