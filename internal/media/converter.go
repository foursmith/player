package media

import (
	"errors"
	"image"
	"unsafe"
)

// RGBAConverter reuses native conversion and hardware readback buffers. Use one
// per presentation surface, on that surface's rendering thread. The zero value
// is ready to use; Close releases its native resources.
type RGBAConverter struct {
	scale    unsafe.Pointer
	software *avFrame
	input    [3]int32
	color    [2]int32
	output   image.Point
	images   [2]*image.RGBA
	next     int
}

// Convert retains two output images, so the last displayed image stays intact
// while the next one is converted. An image may be overwritten after the next
// Convert call. Consumers retaining it longer must copy its pixels.
func (c *RGBAConverter) Convert(f *Frame, width, height int) (*image.RGBA, error) {
	if f == nil || f.ptr == nil {
		return nil, errors.New("video frame is closed")
	}
	if width < 1 || height < 1 || width > 1<<29-1 || height > 1<<31-1 {
		return nil, errors.New("invalid output size")
	}
	src := f.ptr
	if src.HwFramesCtx != nil {
		if c.software == nil {
			c.software = av.util.FrameAlloc()
			if c.software == nil {
				return nil, errors.New("cannot allocate software video frame")
			}
		}
		format := (*avHWFrames)(unsafe.Pointer(src.HwFramesCtx.Data)).SwFormat
		if c.software.Width != src.Width || c.software.Height != src.Height || c.software.Format != format {
			av.util.FrameUnref(c.software)
		}
		if ret := av.util.TransferFrame(c.software, src, 0); ret < 0 {
			return nil, mediaError(ret)
		}
		src = c.software
	}
	input, output := [3]int32{src.Width, src.Height, src.Format}, image.Pt(width, height)
	if c.scale == nil || c.input != input || c.output != output {
		av.scale.Free(c.scale)
		c.scale = av.scale.Create(src.Width, src.Height, src.Format, int32(width), int32(height), av.util.PixelFormat("rgba"), 2, nil, nil, nil)
		if c.scale == nil {
			return nil, errors.New("cannot create FFmpeg frame converter")
		}
		c.input, c.output = input, output
		c.color = [2]int32{-1, -1}
		// Drop both old buffers on a size change, including the slot that will
		// not be used until the next frame.
		for i, img := range c.images {
			if img != nil && img.Bounds().Size() != output {
				c.images[i] = nil
			}
		}
	}
	// Hardware downloads do not copy frame properties. Read the matrix and
	// range from the original frame, including changes within a video.
	color := [2]int32{f.ptr.Colorspace, f.ptr.ColorRange}
	if c.color != color {
		matrix := color[0]
		switch matrix {
		case 1, 4, 5, 6, 7, 9: // BT.709, FCC, BT.601, SMPTE 240M, BT.2020
		case 10:
			matrix = 9
		default:
			matrix = 5 // FFmpeg's default for unspecified matrices.
		}
		coefficients := av.scale.Coefficients(matrix)
		full := int32(0)
		if color[1] == avColorFull {
			full = 1
		}
		if ret := av.scale.Colorspace(c.scale, coefficients, full, coefficients, 1, 0, 1<<16, 1<<16); ret < 0 {
			return nil, mediaError(ret)
		}
		c.color = color
	}
	img := c.images[c.next]
	if img == nil {
		img = image.NewRGBA(image.Rect(0, 0, width, height))
		c.images[c.next] = img
	}
	plane, stride := [4]*byte{&img.Pix[0]}, [4]int32{int32(img.Stride)}
	if ret := av.scale.Scale(c.scale, &src.Data[0], &src.Linesize[0], 0, src.Height, &plane[0], &stride[0]); ret < 0 {
		return nil, mediaError(ret)
	}
	c.next = 1 - c.next
	return img, nil
}

func (c *RGBAConverter) Close() {
	if c.scale != nil {
		av.scale.Free(c.scale)
	}
	if c.software != nil {
		av.util.FrameFree(&c.software)
	}
	*c = RGBAConverter{}
}

// RGBA returns an independently owned image for analysis and screenshots.
func (f *Frame) RGBA(width, height int) (*image.RGBA, error) {
	var converter RGBAConverter
	defer converter.Close()
	return converter.Convert(f, width, height)
}
