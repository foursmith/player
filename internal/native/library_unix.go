//go:build darwin || linux

package native

import (
	"os"
	"path/filepath"
	"runtime"

	"github.com/ebitengine/purego"
)

func openLibrary(path string) (uintptr, error) {
	return purego.Dlopen(path, purego.RTLD_NOW|purego.RTLD_GLOBAL)
}

func lookupSymbol(handle uintptr, name string) (uintptr, error) {
	return purego.Dlsym(handle, name)
}

func systemLibraries(name string) []string {
	if runtime.GOOS != "darwin" {
		dirs := append(filepath.SplitList(os.Getenv("PATH")), "/usr/local/lib", "/usr/lib", "/lib")
		arch := "x86_64-linux-gnu"
		if runtime.GOARCH == "arm64" {
			arch = "aarch64-linux-gnu"
		}
		dirs = append(dirs, filepath.Join("/usr/lib", arch), filepath.Join("/lib", arch))
		var paths []string
		for _, dir := range dirs {
			paths = append(paths, filepath.Join(dir, name))
		}
		return paths
	}
	var paths []string
	for _, root := range []string{"/opt/homebrew", "/usr/local"} {
		for _, formula := range []string{"ffmpeg", "sdl3"} {
			paths = append(paths, filepath.Join(root, "opt", formula, "lib", name))
		}
		paths = append(paths, filepath.Join(root, "lib", name))
	}
	return paths
}
