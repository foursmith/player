package native

import (
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWindowsLoadsRelativeRuntimeWithPrivateDependencies(t *testing.T) {
	path, err := FindLibrary(InferenceLibrary())
	if err != nil {
		t.Skip("local LiteRT runtime unavailable")
	}
	dir := filepath.Dir(filepath.Dir(path))
	relative, err := filepath.Rel(dir, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	handle, err := openLibrary(relative)
	if err != nil {
		t.Fatal("relative runtime path could not load its dependencies", err)
	}
	defer windows.FreeLibrary(windows.Handle(handle))
	if _, err := lookupSymbol(handle, "TfLiteVersion"); err != nil {
		t.Fatal(err)
	}
}
