package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Native cross builds still require a matching C toolchain and media SDK.
// Dependency resolution runs on the target OS; it never guesses host libraries.
func portable(name string) error {
	dir := filepath.Join(*output, runtime.GOOS+"-"+runtime.GOARCH)
	lib := filepath.Join(dir, "lib")
	if err := os.MkdirAll(lib, 0755); err != nil {
		return err
	}
	runtimePath := os.Getenv("FSVR_TFLITE_LIBRARY")
	if runtimePath == "" {
		return fmt.Errorf("FSVR_TFLITE_LIBRARY is required for %s", runtime.GOOS)
	}
	runtimeName := "libtensorflowlite_c.so"
	if runtime.GOOS == "windows" {
		runtimeName = "tensorflowlite_c.dll"
	}
	rt := filepath.Join(lib, runtimeName)
	if err := copyFile(runtimePath, rt); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		// LoadLibrary and the executable's loader search beside each binary.
		// SDK bin directories contain their complete dependent DLL set.
		var dirs []string
		if toolchain, err := exec.LookPath("ffmpeg"); err == nil {
			dirs = append(dirs, filepath.Dir(toolchain))
		}
		dirs = append(dirs, filepath.Dir(runtimePath))
		if sdk := os.Getenv("FSVR_NATIVE_LIB_DIR"); sdk != "" {
			dirs = append(dirs, sdk)
		}
		copied := map[string]bool{}
		for _, sdk := range dirs {
			files, err := os.ReadDir(sdk)
			if err != nil {
				return err
			}
			for _, file := range files {
				if file.IsDir() || !strings.EqualFold(filepath.Ext(file.Name()), ".dll") {
					continue
				}
				name := strings.ToLower(file.Name())
				if copied[name] {
					continue
				}
				copied[name] = true
				src := filepath.Join(sdk, file.Name())
				for _, dest := range []string{dir, lib} {
					if err = copyFile(src, filepath.Join(dest, file.Name())); err != nil {
						return err
					}
				}
			}
		}
		fmt.Println("Packaged native Windows SDK:", dir)
		return nil
	}
	if runtime.GOOS != "linux" {
		return fmt.Errorf("unsupported package platform: %s", runtime.GOOS)
	}
	if _, err := exec.LookPath("patchelf"); err != nil {
		return fmt.Errorf("Linux packaging requires patchelf: %w", err)
	}
	executable := filepath.Join(dir, name)
	if _, err := os.Stat(executable); err != nil {
		return err
	}
	queue := []string{executable, rt}
	seen := map[string]bool{runtimeName: true}
	for len(queue) > 0 {
		path := queue[0]
		queue = queue[1:]
		output, err := exec.Command("ldd", path).CombinedOutput()
		if err != nil {
			return fmt.Errorf("ldd %s: %w: %s", path, err, output)
		}
		for _, line := range strings.Split(string(output), "\n") {
			fields := strings.Fields(line)
			if len(fields) < 3 || fields[1] != "=>" {
				continue
			}
			dep := fields[2]
			if dep == "not" {
				return fmt.Errorf("missing dependency: %s", line)
			}
			name := filepath.Base(dep)
			system := false
			for _, prefix := range []string{"libc.so", "libm.so", "libdl.so", "libpthread.so", "librt.so", "libresolv.so", "ld-linux"} {
				if strings.HasPrefix(name, prefix) {
					system = true
				}
			}
			if system || seen[name] {
				continue
			}
			seen[name] = true
			dest := filepath.Join(lib, name)
			if err := copyFile(dep, dest); err != nil {
				return err
			}
			queue = append(queue, dest)
		}
		rpath := "$ORIGIN"
		if path == executable {
			rpath = "$ORIGIN/lib"
		}
		if err := command("patchelf", "--set-rpath", rpath, path); err != nil {
			return err
		}
	}
	fmt.Println("Packaged native Linux libraries:", dir)
	return nil
}
