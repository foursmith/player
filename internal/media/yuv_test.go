package media

import (
	"bytes"
	"testing"
)

func TestYUV420BorrowsPaddedPlanes(t *testing.T) {
	if err := loadMedia(); err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{"yuv420p", "yuvj420p", "nv12"} {
		t.Run(format, func(t *testing.T) {
			y, u, v := bytes.Repeat([]byte{100}, 24), bytes.Repeat([]byte{90}, 16), bytes.Repeat([]byte{200}, 16)
			frame := &Frame{ptr: &avFrame{Width: 5, Height: 3, Format: av.util.PixelFormat(format),
				Data: [8]*byte{&y[0], &u[0], &v[0]}, Linesize: [8]int32{8, 8, 8}, Colorspace: 1}}
			planes, ok := frame.YUV420()
			if !ok || planes.Width != 5 || planes.Height != 3 || len(planes.Planes[0]) != 21 ||
				planes.Interleaved != (format == "nv12") || planes.FullRange != (format == "yuvj420p") {
				t.Fatalf("incorrect padded planes: %+v, %v", planes, ok)
			}
			y[0] = 42
			if planes.Planes[0][0] != 42 {
				t.Fatal("luma was copied")
			}
			for _, matrix := range []int32{1, 5, 6, 9, 10} {
				frame.ptr.Colorspace, frame.ptr.ColorRange = matrix, avColorFull
				got, ok := frame.YUV420()
				if !ok || got.Colorspace != matrix || !got.FullRange {
					t.Fatal("frame color metadata was lost")
				}
			}
			for _, matrix := range []int32{4, 7, 8} {
				frame.ptr.Colorspace = matrix
				if _, ok := frame.YUV420(); ok {
					t.Fatal("unsupported matrix bypassed swscale", matrix)
				}
			}
			frame.ptr.Colorspace = 1
			for _, stride := range []int32{-8, 0, 4} {
				frame.ptr.Linesize[0] = stride
				if _, ok := frame.YUV420(); ok {
					t.Fatal("invalid luma stride was accepted", stride)
				}
			}
			frame.ptr.Linesize[0], frame.ptr.Data[1] = 8, nil
			if _, ok := frame.YUV420(); ok {
				t.Fatal("missing chroma was accepted")
			}
		})
	}
	if _, ok := conversionFrame(t, 8, 8).YUV420(); ok {
		t.Fatal("YUV444 bypassed swscale")
	}
	if _, ok := (&Frame{}).YUV420(); ok {
		t.Fatal("closed frame was accepted")
	}
}
