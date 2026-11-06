package video

import (
	_ "embed"
	"errors"
	"image"
	"log"
	"math"
	"sync"
	"unsafe"

	"github.com/egoist/mygo/ui"
	"github.com/foursmith/player/internal/media"
	"github.com/foursmith/player/internal/native"
)

//go:embed projection.hlsl
var d3dSource string

const TransparentWindow = true

// Six 16-byte registers, matching Projection in projection.hlsl.
type d3dParameters struct {
	Camera                       [4]float32
	Layout                       [4]int32
	Output, Matrix, Chroma, Flat [4]float32
}

// direct3d follows the Metal surface: a native video layer below the UI and a
// separate, small offscreen target for face tracking. Decoder pictures never
// leave GPU memory during presentation. Copying a decoder array slice to a
// shader-readable texture is a GPU copy, with no staging texture or RGBA upload.
type direct3d struct {
	mu                                            sync.Mutex
	window, owner, device, context                uintptr
	vertex, fragment, constants, sampler, raster  uintptr
	input                                         uintptr
	views                                         [2]uintptr
	inputDesc                                     d3dTextureDesc
	serial                                        uint64
	swap, target, composition, visual, screenView uintptr
	screenSize                                    image.Point
	capture, captureView, staging                 uintptr
	captureSize                                   image.Point
	converter                                     media.RGBAConverter
	fallback                                      rgbaSurface
	failedOwner                                   uintptr
}

func New(window uintptr) (Surface, error) {
	if _, err := d3dShaders(); err != nil {
		return nil, err
	}
	return &direct3d{window: window}, nil
}

func (r *direct3d) releaseGPU() {
	if r.target != 0 {
		d3dCall(r.target, 3, 0)
		d3dCall(r.composition, 3)
	}
	d3dFree(&r.views[0], &r.views[1], &r.input, &r.captureView, &r.capture, &r.staging,
		&r.screenView, &r.visual, &r.target, &r.composition, &r.swap,
		&r.vertex, &r.fragment, &r.constants, &r.sampler, &r.raster, &r.context, &r.device)
	r.owner, r.serial = 0, 0
	r.inputDesc = d3dTextureDesc{}
	r.screenSize, r.captureSize = image.Point{}, image.Point{}
	r.converter.Close()
}

func (r *direct3d) prepare(frame *media.Frame, texture media.D3D11Texture, hardware bool) error {
	owner := uintptr(1) // Software decode uses a renderer-owned GPU device.
	if hardware {
		owner = texture.Device
	}
	if r.owner == owner && r.device != 0 {
		return r.source(frame, texture, hardware)
	}
	r.releaseGPU()
	if hardware {
		r.device, r.context = texture.Device, texture.Context
		d3dCall(r.device, 1)
		d3dCall(r.context, 1)
	} else {
		proc := d3dLibrary.NewProc("D3D11CreateDevice")
		if err := proc.Find(); err != nil {
			return err
		}
		level := uint32(0xb000)
		hr, _, _ := proc.Call(0, 1, 0, 0x20, uintptr(unsafe.Pointer(&level)), 1, 7,
			uintptr(unsafe.Pointer(&r.device)), 0, uintptr(unsafe.Pointer(&r.context)))
		if err := d3dCheck("create rendering device", hr); err != nil {
			return err
		}
	}
	code, err := d3dShaders()
	if err != nil {
		return err
	}
	vertex, fragment := code[0], code[1]
	if err := d3dCheck("create vertex shader", d3dCall(r.device, 12, uintptr(unsafe.Pointer(&vertex[0])), uintptr(len(vertex)), 0, uintptr(unsafe.Pointer(&r.vertex)))); err != nil {
		return err
	}
	if err := d3dCheck("create pixel shader", d3dCall(r.device, 15, uintptr(unsafe.Pointer(&fragment[0])), uintptr(len(fragment)), 0, uintptr(unsafe.Pointer(&r.fragment)))); err != nil {
		return err
	}
	buffer := d3dBufferDesc{Size: uint32(unsafe.Sizeof(d3dParameters{})), Bind: 4}
	if err := d3dCheck("create parameters", d3dCall(r.device, 3, uintptr(unsafe.Pointer(&buffer)), 0, uintptr(unsafe.Pointer(&r.constants)))); err != nil {
		return err
	}
	sampler := d3dSampler{Filter: 0x15, U: 3, V: 3, W: 3, Comparison: 1, MaxLOD: math.MaxFloat32}
	if err := d3dCheck("create sampler", d3dCall(r.device, 23, uintptr(unsafe.Pointer(&sampler)), uintptr(unsafe.Pointer(&r.sampler)))); err != nil {
		return err
	}
	raster := d3dRasterizer{Fill: 3, Cull: 1, DepthClip: 1}
	if err := d3dCheck("create rasterizer", d3dCall(r.device, 22, uintptr(unsafe.Pointer(&raster)), uintptr(unsafe.Pointer(&r.raster)))); err != nil {
		return err
	}
	r.owner = owner
	return r.source(frame, texture, hardware)
}

func (r *direct3d) source(frame *media.Frame, texture media.D3D11Texture, hardware bool) error {
	if r.input != 0 && r.serial == frame.ID() {
		return nil
	}
	w, h := frame.Size()
	desc := d3dTextureDesc{Width: uint32(w), Height: uint32(h), Format: 28, Mips: 1, Array: 1, Samples: 1, Bind: 8}
	var img *image.RGBA
	if hardware {
		d3dCall(texture.Texture, 10, uintptr(unsafe.Pointer(&desc)))
		if desc.Format != 103 && desc.Format != 104 && desc.Format != 105 {
			return errors.New("D3D11 decoder texture is not NV12/P010/P016")
		}
		desc.Mips, desc.Array, desc.Samples, desc.Quality = 1, 1, 1, 0
		desc.Usage, desc.Bind, desc.CPUAccess, desc.Misc = 0, 8, 0, 0
	} else {
		var err error
		img, err = r.converter.Convert(frame, w, h)
		if err != nil {
			return err
		}
	}
	if r.input == 0 || r.inputDesc != desc {
		d3dFree(&r.views[0], &r.views[1], &r.input)
		if err := d3dCheck("create video texture", d3dCall(r.device, 5, uintptr(unsafe.Pointer(&desc)), 0, uintptr(unsafe.Pointer(&r.input)))); err != nil {
			return err
		}
		formats := []uint32{28}
		if desc.Format == 103 {
			formats = []uint32{61, 49}
		}
		if desc.Format == 104 || desc.Format == 105 {
			formats = []uint32{56, 35}
		}
		for i, format := range formats {
			view := d3dViewDesc{Format: format, Dimension: 4, Data: [4]uint32{0, 1}}
			if err := d3dCheck("create video plane view", d3dCall(r.device, 7, r.input, uintptr(unsafe.Pointer(&view)), uintptr(unsafe.Pointer(&r.views[i])))); err != nil {
				return err
			}
		}
		r.inputDesc = desc
	}
	if hardware {
		d3dCall(r.context, 46, r.input, 0, 0, 0, 0, texture.Texture, uintptr(texture.Subresource), 0)
	} else {
		d3dCall(r.context, 48, r.input, 0, 0, uintptr(unsafe.Pointer(&img.Pix[0])), uintptr(img.Stride), 0)
	}
	r.serial = frame.ID()
	return nil
}

func (r *direct3d) screen(width, height int) error {
	if r.swap != 0 && r.screenSize == image.Pt(width, height) {
		return nil
	}
	d3dFree(&r.screenView)
	if r.swap == 0 {
		host, err := d3dVideoHost(r.window)
		if err != nil {
			return err
		}
		var dxgi, adapter, factory uintptr
		defer d3dFree(&dxgi, &adapter, &factory)
		if err := d3dCheck("query DXGI device", d3dCall(r.device, 0, uintptr(unsafe.Pointer(&dxgiDevice)), uintptr(unsafe.Pointer(&dxgi)))); err != nil {
			return err
		}
		if err := d3dCheck("get adapter", d3dCall(dxgi, 7, uintptr(unsafe.Pointer(&adapter)))); err != nil {
			return err
		}
		if err := d3dCheck("get factory", d3dCall(adapter, 6, uintptr(unsafe.Pointer(&dxgiFactory2)), uintptr(unsafe.Pointer(&factory)))); err != nil {
			return err
		}
		desc := d3dSwapDesc{Width: uint32(width), Height: uint32(height), Format: 87, Samples: 1, Usage: 0x20, Count: 2, Effect: 3, Alpha: 1}
		if err := d3dCheck("create composition swap chain", d3dCall(factory, 24, r.device, uintptr(unsafe.Pointer(&desc)), 0, uintptr(unsafe.Pointer(&r.swap)))); err != nil {
			return err
		}
		if err := dcompCreate.Find(); err != nil {
			return err
		}
		hr, _, _ := dcompCreate.Call(dxgi, uintptr(unsafe.Pointer(&compositionDevice)), uintptr(unsafe.Pointer(&r.composition)))
		if err := d3dCheck("create composition device", hr); err != nil {
			return err
		}
		// Share the content HWND with MyGo, whose UI owns its top tree. Putting
		// video on the parent clips it out beneath the full-size content child.
		if err := d3dCheck("create video layer", d3dCall(r.composition, 6, host, 0, uintptr(unsafe.Pointer(&r.target)))); err != nil {
			return err
		}
		if err := d3dCheck("create video visual", d3dCall(r.composition, 7, uintptr(unsafe.Pointer(&r.visual)))); err != nil {
			return err
		}
		if err := d3dCheck("attach video swap chain", d3dCall(r.visual, 15, r.swap)); err != nil {
			return err
		}
		if err := d3dCheck("attach video visual", d3dCall(r.target, 3, r.visual)); err != nil {
			return err
		}
		if err := d3dCheck("commit video layer", d3dCall(r.composition, 3)); err != nil {
			return err
		}
	} else {
		if err := d3dCheck("resize video", d3dCall(r.swap, 13, 2, uintptr(width), uintptr(height), 0, 0)); err != nil {
			return err
		}
	}
	var back uintptr
	defer d3dFree(&back)
	if err := d3dCheck("get video back buffer", d3dCall(r.swap, 9, 0, uintptr(unsafe.Pointer(&d3dTexture2D)), uintptr(unsafe.Pointer(&back)))); err != nil {
		return err
	}
	if err := d3dCheck("create video output view", d3dCall(r.device, 9, back, 0, uintptr(unsafe.Pointer(&r.screenView)))); err != nil {
		return err
	}
	r.screenSize = image.Pt(width, height)
	return nil
}

func (r *direct3d) offscreen(width, height int) error {
	if r.capture != 0 && r.captureSize == image.Pt(width, height) {
		return nil
	}
	d3dFree(&r.captureView, &r.capture, &r.staging)
	desc := d3dTextureDesc{Width: uint32(width), Height: uint32(height), Mips: 1, Array: 1, Samples: 1, Format: 87, Bind: 0x20}
	if err := d3dCheck("create capture target", d3dCall(r.device, 5, uintptr(unsafe.Pointer(&desc)), 0, uintptr(unsafe.Pointer(&r.capture)))); err != nil {
		return err
	}
	if err := d3dCheck("create capture view", d3dCall(r.device, 9, r.capture, 0, uintptr(unsafe.Pointer(&r.captureView)))); err != nil {
		return err
	}
	desc.Usage, desc.Bind, desc.CPUAccess = 3, 0, 0x20000
	if err := d3dCheck("create capture readback", d3dCall(r.device, 5, uintptr(unsafe.Pointer(&desc)), 0, uintptr(unsafe.Pointer(&r.staging)))); err != nil {
		return err
	}
	r.captureSize = image.Pt(width, height)
	return nil
}

func (r *direct3d) parameters(frame *media.Frame, p Parameters, texture media.D3D11Texture, hardware bool, width, height int) d3dParameters {
	w, h := frame.Size()
	fov := p.FOV
	if fov <= 0 {
		fov = 80
	}
	flat := p.FlatView.Constrain(float64(width)/float64(height), float64(w)/float64(h))
	portrait, rgba := int32(0), int32(0)
	if p.Portrait {
		portrait = 1
	}
	if !hardware {
		rgba = 1
	}
	v := p.View
	params := d3dParameters{
		Camera: [4]float32{float32(v.Yaw), float32(v.Pitch), float32(v.Zoom), float32(v.Forward)},
		Layout: [4]int32{int32(p.Mode), int32(max(1, p.SplitCount)), portrait, rgba},
		Output: [4]float32{float32(w) / float32(h), float32(width), float32(height), float32(fov)},
		Matrix: [4]float32{.299, .114, 16.0 / 255, 255.0 / 219},
		Chroma: [4]float32{128.0 / 255, 255.0 / 224, float32(w) / float32(r.inputDesc.Width), float32(h) / float32(r.inputDesc.Height)},
		Flat:   [4]float32{float32(flat.X), float32(flat.Y), float32(flat.Zoom), 0},
	}
	switch texture.Colorspace {
	case 1:
		params.Matrix[0], params.Matrix[1] = .2126, .0722
	case 4:
		params.Matrix[0], params.Matrix[1] = .30, .11
	case 7:
		params.Matrix[0], params.Matrix[1] = .212, .087
	case 9, 10:
		params.Matrix[0], params.Matrix[1] = .2627, .0593
	}
	ten := r.inputDesc.Format == 104 || r.inputDesc.Format == 105
	if ten {
		params.Matrix[2], params.Matrix[3], params.Chroma[0], params.Chroma[1] = 4096.0/65535, 65535.0/56064, 32768.0/65535, 65535.0/57344
	}
	if texture.ColorRange == 2 {
		params.Matrix[2], params.Matrix[3], params.Chroma[1] = 0, 1, 1
		if r.inputDesc.Format == 104 {
			params.Matrix[3], params.Chroma[1] = 65535.0/65472, 65535.0/65472
		}
	}
	return params
}

func (r *direct3d) draw(frame *media.Frame, p Parameters, texture media.D3D11Texture, hardware bool, view uintptr, width, height int) {
	params := r.parameters(frame, p, texture, hardware, width, height)
	d3dCall(r.context, 48, r.constants, 0, 0, uintptr(unsafe.Pointer(&params)), 0, 0)
	d3dCall(r.context, 17, 0)
	d3dCall(r.context, 24, 4) // triangle list, generated from SV_VertexID
	d3dCall(r.context, 11, r.vertex, 0, 0)
	d3dCall(r.context, 9, r.fragment, 0, 0)
	d3dCall(r.context, 16, 0, 1, uintptr(unsafe.Pointer(&r.constants)))
	d3dCall(r.context, 8, 0, 2, uintptr(unsafe.Pointer(&r.views[0])))
	d3dCall(r.context, 10, 0, 1, uintptr(unsafe.Pointer(&r.sampler)))
	d3dCall(r.context, 43, r.raster)
	viewport := d3dViewport{Width: float32(width), Height: float32(height), MaxDepth: 1}
	d3dCall(r.context, 44, 1, uintptr(unsafe.Pointer(&viewport)))
	d3dCall(r.context, 33, 1, uintptr(unsafe.Pointer(&view)), 0)
	d3dCall(r.context, 35, 0, 0, 0xffffffff)
	d3dCall(r.context, 13, 3, 0) // Draw
	var none [2]uintptr
	d3dCall(r.context, 8, 0, 2, uintptr(unsafe.Pointer(&none[0])))
	d3dCall(r.context, 33, 0, 0, 0)
}

func lockTexture(frame *media.Frame) (media.D3D11Texture, bool, func()) {
	texture, hardware := frame.D3D11Texture()
	if !hardware {
		return texture, false, func() {}
	}
	texture.Lock()
	return texture, true, texture.Unlock
}

func (r *direct3d) Present(frame *media.Frame, p Parameters, width, height, scale float64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.window == 0 || width <= 0 || height <= 0 || scale <= 0 {
		return errors.New("invalid D3D11 video window or size")
	}
	texture, hardware, unlock := lockTexture(frame)
	defer unlock()
	owner := uintptr(1)
	if hardware {
		owner = texture.Device
	}
	var err error
	if r.failedOwner != owner {
		err = r.prepare(frame, texture, hardware)
		w, h := max(1, int(math.Round(width*scale))), max(1, int(math.Round(height*scale)))
		if err == nil {
			err = r.screen(w, h)
		}
		if err == nil {
			r.draw(frame, p, texture, hardware, r.screenView, w, h)
			err = d3dCheck("present", d3dCall(r.swap, 8, 0, 0))
		}
		if err == nil {
			r.failedOwner = 0
			return nil
		}
		r.failedOwner = owner
		log.Printf("[video] native D3D11 unavailable, using portable surface: %v", err)
		r.releaseGPU()
	}
	return r.fallback.Present(frame, p, width, height, scale)
}

func (r *direct3d) Paint(p *ui.Painter, rect ui.Rect) {
	if r.failedOwner != 0 {
		r.fallback.Paint(p, rect)
	}
}

func (r *direct3d) Capture(frame *media.Frame, p Parameters, width, height int) (*image.RGBA, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if width <= 0 || height <= 0 {
		return nil, errors.New("invalid D3D11 capture size")
	}
	texture, hardware, unlock := lockTexture(frame)
	defer unlock()
	if err := r.prepare(frame, texture, hardware); err != nil {
		return nil, err
	}
	if err := r.offscreen(width, height); err != nil {
		return nil, err
	}
	r.draw(frame, p, texture, hardware, r.captureView, width, height)
	d3dCall(r.context, 47, r.staging, r.capture) // GPU copy of the small rendered view.
	var mapped d3dMapped
	if err := d3dCheck("map capture", d3dCall(r.context, 14, r.staging, 0, 1, 0, uintptr(unsafe.Pointer(&mapped)))); err != nil {
		return nil, err
	}
	defer d3dCall(r.context, 15, r.staging, 0)
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		row := unsafe.Slice((*byte)(native.Pointer(mapped.Data+uintptr(y)*uintptr(mapped.RowPitch))), width*4)
		out := img.Pix[y*img.Stride:][:width*4]
		for x := 0; x < len(out); x += 4 {
			out[x], out[x+1], out[x+2], out[x+3] = row[x+2], row[x+1], row[x], row[x+3]
		}
	}
	return img, nil
}

func (r *direct3d) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.releaseGPU()
	r.fallback.Close()
	r.failedOwner = 0
}
func (r *direct3d) Close() { r.Clear() }

var _ Surface = (*direct3d)(nil)
