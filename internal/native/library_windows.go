package native

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

func openLibrary(path string) (uintptr, error) {
	flags := uintptr(windows.LOAD_LIBRARY_SEARCH_DEFAULT_DIRS)
	// Absolute paths also search beside the DLL for its private dependencies.
	if len(path) > 2 && (path[1] == ':' || path[0] == '\\') {
		flags |= windows.LOAD_LIBRARY_SEARCH_DLL_LOAD_DIR
	}
	handle, err := windows.LoadLibraryEx(path, 0, flags)
	return uintptr(handle), err
}

func lookupSymbol(handle uintptr, name string) (uintptr, error) {
	return windows.GetProcAddress(windows.Handle(handle), name)
}

func systemLibraries(name string) []string {
	var paths []string
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		paths = append(paths, filepath.Join(dir, name))
	}
	return paths
}
