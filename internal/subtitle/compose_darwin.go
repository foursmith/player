package subtitle

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework CoreText -framework CoreGraphics -framework Foundation
#include <stdlib.h>
int fsvr_subtitle(unsigned char*,int,int,int,const char*,double);
*/
import "C"
import (
	"fmt"
	"image"
	"unsafe"
)

const NativeComposition = true

func Compose(img *image.RGBA, text string, cssWidth float64) error {
	if text == "" {
		return nil
	}
	s := C.CString(text)
	defer C.free(unsafe.Pointer(s))
	if code := C.fsvr_subtitle((*C.uchar)(unsafe.Pointer(&img.Pix[0])), C.int(img.Rect.Dx()), C.int(img.Rect.Dy()), C.int(img.Stride), s, C.double(cssWidth)); code != 0 {
		return fmt.Errorf("subtitle composition: %d", code)
	}
	return nil
}
