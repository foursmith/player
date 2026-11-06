// Package apple contains the shared CoreFoundation/CoreVideo bindings.
package apple

import (
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/foursmith/player/internal/native"
)

const (
	NV12Video = 0x34323076
	NV12Full  = 0x34323066
	P010Video = 0x78343230
	P010Full  = 0x78663230
)

var Core struct {
	Retain     func(uintptr) uintptr
	Release    func(uintptr)
	Equal      func(uintptr, uintptr) bool
	Dictionary func(uintptr, *uintptr, *uintptr, int64, uintptr, uintptr) uintptr
	Number     func(uintptr, int32, *int32) uintptr
}

var Video struct {
	Format      func(uintptr) uint32
	PlaneCount  func(uintptr) uintptr
	PlaneWidth  func(uintptr, uintptr) uintptr
	PlaneHeight func(uintptr, uintptr) uintptr
	Attachment  func(uintptr, uintptr, *uint32) uintptr
}

var dictionaryKeys, dictionaryValues uintptr
var True uintptr

func Framework(name string) (uintptr, error) {
	return native.Open("/System/Library/Frameworks/" + name + ".framework/" + name)
}

// Constant reads an exported CF object.
func Constant(handle uintptr, name string) (uintptr, error) {
	address, err := purego.Dlsym(handle, name)
	if err != nil {
		return 0, err
	}
	return *(*uintptr)(native.Pointer(address)), nil
}

var Load = sync.OnceValue(func() error {
	cf, err := Framework("CoreFoundation")
	if err != nil {
		return err
	}
	if err := native.Bind(cf, &Core, "CFRetain", "CFRelease", "CFEqual", "CFDictionaryCreate", "CFNumberCreate"); err != nil {
		return err
	}
	if dictionaryKeys, err = purego.Dlsym(cf, "kCFTypeDictionaryKeyCallBacks"); err != nil {
		return err
	}
	if dictionaryValues, err = purego.Dlsym(cf, "kCFTypeDictionaryValueCallBacks"); err != nil {
		return err
	}
	if True, err = Constant(cf, "kCFBooleanTrue"); err != nil {
		return err
	}
	cv, err := Framework("CoreVideo")
	if err != nil {
		return err
	}
	return native.Bind(cv, &Video, "CVPixelBufferGetPixelFormatType", "CVPixelBufferGetPlaneCount", "CVPixelBufferGetWidthOfPlane", "CVPixelBufferGetHeightOfPlane", "CVBufferCopyAttachment")
})

func Dictionary(keys, values []uintptr) uintptr {
	return Core.Dictionary(0, unsafe.SliceData(keys), unsafe.SliceData(values), int64(len(keys)), dictionaryKeys, dictionaryValues)
}

func Number(value int32) uintptr { return Core.Number(0, 3, &value) }
