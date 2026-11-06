// Package native loads the shared libraries shipped beside the application.
package native

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

var libraries = struct {
	sync.Mutex
	handles map[string]uintptr
}{handles: make(map[string]uintptr)}

// Open keeps libraries loaded for the process lifetime. Native frames, callbacks
// and concurrent inference workers can outlive the object that first loaded one.
func Open(name string) (uintptr, error) {
	libraries.Lock()
	defer libraries.Unlock()
	if handle := libraries.handles[name]; handle != 0 {
		return handle, nil
	}
	var last error
	for _, path := range libraryCandidates(name) {
		handle, err := openLibrary(path)
		if err == nil {
			libraries.handles[name] = handle
			return handle, nil
		}
		last = err
	}
	return 0, fmt.Errorf("load %s: %w", name, last)
}

// InferenceLibrary uses the same override and search rules for all callers.
func InferenceLibrary() string {
	if path := os.Getenv("FSVR_TFLITE_LIBRARY"); path != "" {
		return path
	}
	return HostTarget().InferenceLibrary()
}

// FindLibrary resolves a local runtime for packaging without loading it.
func FindLibrary(name string) (string, error) {
	for _, path := range libraryCandidates(name) {
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return filepath.Abs(path)
		}
	}
	return "", fmt.Errorf("native library %s is missing; set FSVR_NATIVE_LIB_DIR to the local runtime directory", name)
}

func libraryCandidates(name string) []string {
	if filepath.IsAbs(name) || strings.ContainsAny(name, `/\`) {
		return []string{name}
	}
	var candidates []string
	executable, _ := os.Executable()
	root := filepath.Dir(executable)
	for _, dir := range []string{os.Getenv("FSVR_NATIVE_LIB_DIR"), filepath.Join(root, "lib"), filepath.Join(root, "..", "Resources", "lib"), root} {
		if dir != "" {
			candidates = append(candidates, filepath.Join(dir, name))
		}
	}
	if target := HostTarget(); target.OS == "darwin" && name == target.InferenceLibrary() {
		candidates = append(candidates, filepath.Join(".cache", "tflite", name))
	}
	candidates = append(candidates, systemLibraries(name)...)
	return append(candidates, name)
}

// Bind resolves a set of function fields before publishing the API to callers.
func Bind(handle uintptr, api any, names ...string) error {
	value := reflect.ValueOf(api).Elem()
	if len(names) != value.NumField() {
		return fmt.Errorf("native API has %d fields for %d symbols", value.NumField(), len(names))
	}
	for i, name := range names {
		symbol, err := lookupSymbol(handle, name)
		if err != nil {
			return fmt.Errorf("resolve %s: %w", name, err)
		}
		purego.RegisterFunc(value.Field(i).Addr().Interface(), symbol)
	}
	return nil
}

// Pointer reinterprets an address returned by a native API. It must never be
// used to hide a Go pointer from the garbage collector.
func Pointer(address uintptr) unsafe.Pointer {
	return *(*unsafe.Pointer)(unsafe.Pointer(&address))
}

// String copies a native NUL-terminated string while its owner is still alive.
func String(p *byte) string {
	if p == nil {
		return ""
	}
	n := 0
	for *(*byte)(unsafe.Add(unsafe.Pointer(p), n)) != 0 {
		n++
	}
	return string(unsafe.Slice(p, n))
}
