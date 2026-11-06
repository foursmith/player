package native

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInferenceRuntimeOverridesAreSharedByLoadingAndPackaging(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FSVR_NATIVE_LIB_DIR", dir)
	t.Setenv("FSVR_TFLITE_LIBRARY", "")
	host := HostTarget()
	paths := []string{filepath.Join(dir, host.InferenceLibrary()), filepath.Join(t.TempDir(), "custom-runtime")}
	for _, path := range paths {
		if err := os.WriteFile(path, []byte("runtime fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for i, want := range paths {
		if i != 0 {
			t.Setenv("FSVR_TFLITE_LIBRARY", want)
		}
		got, err := FindLibrary(InferenceLibrary())
		if err != nil || got != want || host.InferencePath() != want {
			t.Fatalf("loading %q, packaging %q, error %v; want %q", got, host.InferencePath(), err, want)
		}
	}
}
