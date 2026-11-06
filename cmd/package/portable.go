package main

import (
	"archive/zip"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/foursmith/player/internal/native"
)

func isLinuxSystemLibrary(name string) bool {
	for _, prefix := range []string{"libc.so", "libm.so", "libmvec.so", "libdl.so", "libpthread.so", "librt.so", "libresolv.so", "libutil.so", "ld-linux", "libgcc_s.so", "libstdc++.so"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// Prepare local resources before MyGo writes archives, .deb and checksums.
func preparePortable(target native.Target) error {
	sdk := os.Getenv("FSVR_NATIVE_LIB_DIR")
	libraries := make(map[string]string)
	if sdk != "" {
		entries, err := os.ReadDir(sdk)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			name := entry.Name()
			shared := target.OS == "windows" && strings.EqualFold(filepath.Ext(name), ".dll") || target.OS == "linux" && strings.Contains(name, ".so") && !isLinuxSystemLibrary(name)
			if !entry.IsDir() && shared {
				libraries[name] = filepath.Join(sdk, name)
			}
		}
	}
	for _, name := range target.MediaLibraries() {
		if _, ok := libraries[name]; ok {
			continue
		}
		path, err := native.FindLibrary(name)
		if err != nil {
			return err
		}
		libraries[name] = path
	}
	libraries[target.InferenceLibrary()] = target.InferencePath()
	if target.OS == "linux" && sdk == "" {
		// Resolve dependencies using the local Linux installation.
		var queue []string
		for _, path := range libraries {
			queue = append(queue, path)
		}
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
				if fields[2] == "not" {
					return fmt.Errorf("missing native dependency: %s", line)
				}
				name := filepath.Base(fields[2])
				if _, ok := libraries[name]; ok || isLinuxSystemLibrary(name) {
					continue
				}
				libraries[name] = fields[2]
				queue = append(queue, fields[2])
			}
		}
	}
	for _, path := range libraries {
		if err := target.CheckLibrary(path); err != nil {
			return err
		}
	}
	if target.OS == "linux" {
		if _, err := exec.LookPath("patchelf"); err != nil {
			return fmt.Errorf("Linux packaging requires host patchelf: %w", err)
		}
	}
	// Replace only the generated library directory of this exact target.
	root := filepath.Join("resources", target.Directory())
	if err := os.MkdirAll(root, 0755); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(root, ".native-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	if err := os.Chmod(stage, 0755); err != nil {
		return err
	}
	for name, source := range libraries {
		dest := filepath.Join(stage, name)
		if err := copyFile(source, dest); err != nil {
			return err
		}
		if target.OS == "linux" {
			if err := command("patchelf", "--set-rpath", "$ORIGIN", dest); err != nil {
				return err
			}
		}
	}
	if target.OS == "linux" {
		for name := range libraries {
			needed, err := target.LibraryDependencies(filepath.Join(stage, name))
			if err != nil {
				return err
			}
			for _, dependency := range needed {
				if _, ok := libraries[dependency]; !ok && !isLinuxSystemLibrary(dependency) {
					return fmt.Errorf("%s needs %s, which is missing from the target SDK", name, dependency)
				}
			}
		}
	} else {
		available := make(map[string]bool)
		for name := range libraries {
			available[strings.ToLower(name)] = true
		}
		for name := range libraries {
			needed, err := target.LibraryDependencies(filepath.Join(stage, name))
			if err != nil {
				return err
			}
			for _, dependency := range needed {
				if !available[strings.ToLower(dependency)] && !native.WindowsSystemLibrary(dependency) {
					return fmt.Errorf("%s needs %s, which is missing from the target SDK", name, dependency)
				}
			}
		}
	}
	lib := filepath.Join(root, "lib")
	if err := os.RemoveAll(lib); err != nil {
		return err
	}
	if err := os.Rename(stage, lib); err != nil {
		return err
	}
	// Preserve the local SDK's license notices with its libraries.
	notices := filepath.Join(root, "runtime-notices")
	if err := os.RemoveAll(notices); err != nil {
		return err
	}
	if sdk != "" {
		if info, err := os.Stat(filepath.Join(sdk, "notices")); err == nil && info.IsDir() {
			if err := os.CopyFS(notices, os.DirFS(filepath.Join(sdk, "notices"))); err != nil {
				return err
			}
		}
	}
	fmt.Printf("Prepared %d native libraries for %s\n", len(libraries), target)
	return nil
}

func portable(config packageConfig, target native.Target) error {
	dir := filepath.Join(*output, target.Directory())
	executable := config.Slug
	if target.OS == "windows" {
		executable = config.Name + ".exe"
	}
	if info, err := os.Stat(filepath.Join(dir, executable)); err != nil || info.IsDir() {
		return fmt.Errorf("target executable is missing: %s", filepath.Join(dir, executable))
	}
	for _, name := range append(target.MediaLibraries(), target.InferenceLibrary()) {
		if err := target.CheckLibrary(filepath.Join(dir, "lib", name)); err != nil {
			return err
		}
	}
	if target.OS == "windows" {
		// MyGo skips NSIS in this workflow. The ZIP includes the executable,
		// resources and complete native runtime as one portable application.
		archive := filepath.Join(dir, fmt.Sprintf("%s-%s-%s.zip", config.Slug, config.Version, target.Directory()))
		if err := writePortableZip(dir, archive); err != nil {
			return err
		}
		if err := refreshChecksum(archive, filepath.Join(*output, "SHA256SUMS")); err != nil {
			return err
		}
	} else {
		// MyGo doesn't map the base C/C++ Debian packages to Arch packages.
		// Keep its generated PKGBUILD accurate as well as the .deb metadata.
		pkgbuild := filepath.Join(dir, "PKGBUILD")
		if data, err := os.ReadFile(pkgbuild); err == nil {
			lines := strings.Split(string(data), "\n")
			for i, line := range lines {
				if strings.HasPrefix(line, "depends=(") && strings.HasSuffix(line, ")") && !strings.Contains(line, "'glibc'") {
					lines[i] = strings.TrimSuffix(line, ")") + " 'glibc' 'gcc-libs')"
				}
			}
			if err := os.WriteFile(pkgbuild, []byte(strings.Join(lines, "\n")), 0644); err != nil {
				return err
			}
		}
		for _, name := range []string{
			fmt.Sprintf("%s-%s-%s.tar.gz", config.Slug, config.Version, target.Directory()),
			fmt.Sprintf("%s_%s_%s.deb", config.Slug, config.Version, target.Arch),
		} {
			if err := refreshChecksum(filepath.Join(dir, name), filepath.Join(*output, "SHA256SUMS")); err != nil {
				return err
			}
		}
	}
	fmt.Println("Packaged native runtime:", dir)
	return nil
}

func writePortableZip(dir, path string) (err error) {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := file.Close(); err == nil {
			err = closeErr
		}
	}()
	archive := zip.NewWriter(file)
	err = filepath.WalkDir(dir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if strings.HasSuffix(entry.Name(), ".zip") {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		name, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		header.Name, header.Method = filepath.ToSlash(name), zip.Deflate
		out, err := archive.CreateHeader(header)
		if err != nil {
			return err
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(out, in)
		closeErr := in.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
	closeErr := archive.Close()
	if err == nil {
		err = closeErr
	}
	return err
}
