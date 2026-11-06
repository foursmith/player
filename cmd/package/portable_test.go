package main

import (
	"archive/zip"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/foursmith/player/internal/native"
)

func windowsSDK(t *testing.T) (native.Target, string) {
	t.Helper()
	target := native.Target{OS: "windows", Arch: "amd64"}
	dir := t.TempDir()
	t.Setenv("FSVR_NATIVE_LIB_DIR", dir)
	t.Setenv("FSVR_TFLITE_LIBRARY", "")
	for _, name := range append(target.MediaLibraries(), target.InferenceLibrary(), "dependency.dll") {
		data := make([]byte, 128)
		binary.LittleEndian.PutUint16(data, 0x8664)
		if err := os.WriteFile(filepath.Join(dir, name), data, 0644); err != nil {
			t.Fatal(err)
		}
	}
	return target, dir
}

func TestTargetRuntimeIsPreparedBeforeArchiving(t *testing.T) {
	t.Chdir(t.TempDir())
	target, sdk := windowsSDK(t)
	if err := os.MkdirAll(filepath.Join(sdk, "notices", "ffmpeg"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sdk, "notices", "ffmpeg", "LICENSE.txt"), []byte("upstream license"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := preparePortable(target); err != nil {
		t.Fatal(err)
	}
	prepared := filepath.Join("resources", target.Directory(), "lib")
	if _, err := os.Stat(filepath.Join(prepared, "dependency.dll")); err != nil {
		t.Fatal(err)
	}
	// Simulate MyGo copying the prepared resources alongside the executable.
	dir := filepath.Join("build", target.Directory())
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.CopyFS(dir, os.DirFS(filepath.Join("resources", target.Directory()))); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Player.exe"), []byte("player"), 0755); err != nil {
		t.Fatal(err)
	}
	oldOutput := *output
	*output = "build"
	t.Cleanup(func() { *output = oldOutput })
	if err := portable(packageConfig{Name: "Player", Slug: "player", Version: "1"}, target); err != nil {
		t.Fatal(err)
	}
	archive, err := zip.OpenReader(filepath.Join(dir, "player-1-windows-amd64.zip"))
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	names := make(map[string]bool)
	for _, file := range archive.File {
		names[file.Name] = true
	}
	for _, name := range []string{"Player.exe", "lib/SDL3.dll", "lib/avcodec-63.dll", "lib/tensorflowlite_c.dll", "lib/dependency.dll", "runtime-notices/ffmpeg/LICENSE.txt"} {
		if !names[name] {
			t.Fatalf("portable archive omitted %s", name)
		}
	}
	checksums, err := os.ReadFile("build/SHA256SUMS")
	if err != nil || !strings.Contains(string(checksums), "player-1-windows-amd64.zip") {
		t.Fatalf("archive checksum: %s, %v", checksums, err)
	}
}

func TestInvalidTargetRuntimePreservesPreparedLibraries(t *testing.T) {
	t.Chdir(t.TempDir())
	target, sdk := windowsSDK(t)
	if err := preparePortable(target); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(sdk, "SDL3.dll")
	data := make([]byte, 128)
	binary.LittleEndian.PutUint16(data, 0xaa64)
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
	if err := preparePortable(target); err == nil {
		t.Fatal("accepted ARM DLL for amd64")
	}
	prepared := filepath.Join("resources", target.Directory(), "lib", "SDL3.dll")
	if err := target.CheckLibrary(prepared); err != nil {
		t.Fatalf("failed preparation replaced existing runtime: %v", err)
	}
}
