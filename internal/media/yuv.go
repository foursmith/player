package media

import (
	"sync"
	"unsafe"
)

var yuv420Formats = sync.OnceValue(func() [3]int32 {
	return [3]int32{av.util.PixelFormat("yuv420p"), av.util.PixelFormat("yuvj420p"), av.util.PixelFormat("nv12")}
})

// YUV420 borrows an 8-bit frame's planes until its Frame is closed. Colorspace
// uses FFmpeg's AVColorSpace values, as D3D11Texture does. Hardware frames and
// formats or matrices requiring conversion are left to RGBAConverter.
type YUV420 struct {
	Width, Height int
	Planes        [3][]byte
	Strides       [3]int
	Interleaved   bool
	Colorspace    int32
	FullRange     bool
}

func (f *Frame) YUV420() (YUV420, bool) {
	if f == nil || f.ptr == nil || f.ptr.HwFramesCtx != nil {
		return YUV420{}, false
	}
	src := f.ptr
	if src.Width <= 0 || src.Height <= 0 {
		return YUV420{}, false
	}
	format := src.Format
	formats := yuv420Formats()
	nv12 := format == formats[2]
	full := format == formats[1]
	if !nv12 && !full && format != formats[0] {
		return YUV420{}, false
	}
	switch src.Colorspace {
	case 0, 1, 2, 3, 5, 6, 9, 10:
	default:
		return YUV420{}, false
	}
	out := YUV420{Width: int(src.Width), Height: int(src.Height), Interleaved: nv12,
		Colorspace: src.Colorspace, FullRange: full || src.ColorRange == avColorFull}
	cw, ch := (out.Width+1)/2, (out.Height+1)/2
	widths, heights := [3]int{out.Width, cw, cw}, [3]int{out.Height, ch, ch}
	count := 3
	if nv12 {
		widths[1], count = 2*cw, 2
	}
	for i := range count {
		stride := int(src.Linesize[i])
		// Negative strides and missing planes retain the swscale path.
		if src.Data[i] == nil || stride < widths[i] || stride > int(^uint(0)>>1)/heights[i] {
			return YUV420{}, false
		}
		out.Strides[i] = stride
		out.Planes[i] = unsafe.Slice(src.Data[i], stride*(heights[i]-1)+widths[i])
	}
	return out, true
}
