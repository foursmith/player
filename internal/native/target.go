package native

import (
	"debug/elf"
	"debug/macho"
	"debug/pe"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

type Target struct{ OS, Arch string }

func HostTarget() Target           { return Target{runtime.GOOS, runtime.GOARCH} }
func (t Target) String() string    { return t.OS + "/" + t.Arch }
func (t Target) Directory() string { return t.OS + "-" + t.Arch }
func (t Target) Library(name string, major int) string {
	switch t.OS {
	case "darwin":
		return fmt.Sprintf("lib%s.%d.dylib", name, major)
	case "windows":
		return fmt.Sprintf("%s-%d.dll", name, major)
	default:
		return fmt.Sprintf("lib%s.so.%d", name, major)
	}
}
func (t Target) MediaLibraries() []string {
	return []string{t.Library("avutil", 61), t.Library("swresample", 7), t.Library("avcodec", 63), t.Library("avformat", 63), t.Library("avfilter", 12), t.Library("swscale", 10), t.AudioLibrary()}
}
func (t Target) AudioLibrary() string {
	sdl := "libSDL3.so.0"
	switch t.OS {
	case "darwin":
		sdl = "libSDL3.0.dylib"
	case "windows":
		sdl = "SDL3.dll"
	}
	return sdl
}
func (t Target) InferenceLibrary() string {
	switch t.OS {
	case "darwin":
		return "libtensorflowlite_c.2.17.1.dylib"
	case "windows":
		return "tensorflowlite_c.dll"
	default:
		return "libtensorflowlite_c.so"
	}
}
func (t Target) InferencePath() string {
	if path := os.Getenv("FSVR_TFLITE_LIBRARY"); path != "" {
		return path
	}
	if dir := os.Getenv("FSVR_NATIVE_LIB_DIR"); dir != "" {
		return filepath.Join(dir, t.InferenceLibrary())
	}
	if t.OS == "darwin" {
		return filepath.Join(".cache", "tflite", t.InferenceLibrary())
	}
	return t.InferenceLibrary()
}

// CheckLibrary examines the file without loading target code on the build host.
func (t Target) CheckLibrary(path string) error {
	valid := false
	switch t.OS {
	case "windows":
		file, err := pe.Open(path)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		defer file.Close()
		valid = file.Machine == pe.IMAGE_FILE_MACHINE_AMD64 && t.Arch == "amd64" || file.Machine == pe.IMAGE_FILE_MACHINE_ARM64 && t.Arch == "arm64"
	case "linux":
		file, err := elf.Open(path)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		defer file.Close()
		valid = file.Class == elf.ELFCLASS64 && file.Data == elf.ELFDATA2LSB && (file.Machine == elf.EM_X86_64 && t.Arch == "amd64" || file.Machine == elf.EM_AARCH64 && t.Arch == "arm64")
	case "darwin":
		file, err := macho.Open(path)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		defer file.Close()
		valid = file.Cpu == macho.CpuAmd64 && t.Arch == "amd64" || file.Cpu == macho.CpuArm64 && t.Arch == "arm64"
	}
	if !valid {
		return fmt.Errorf("%s does not match target %s", path, t)
	}
	return nil
}
