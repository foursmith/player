// deps fetches the pinned inference runtime; FFmpeg/SDL come from the toolchain.
package main

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	if path := os.Getenv("FSVR_TFLITE_LIBRARY"); path != "" {
		_, err := os.Stat(path)
		return err
	}
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		return fmt.Errorf("set FSVR_TFLITE_LIBRARY to a TensorFlow Lite C runtime for %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	const digest = "c183be044a6ad0c6976a6ddd2fafb9d723b03c46c9f06870ab9a55201d71c2d0"
	dir := filepath.Join(".cache", "tflite")
	target := filepath.Join(dir, "libtensorflowlite_c.2.17.1.dylib")
	if _, err := os.Stat(target); err == nil {
		return nil
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	client := &http.Client{Timeout: 2 * time.Minute}
	r, err := client.Get("https://github.com/tphakala/tflite_c/releases/download/v2.17.1/tflite_c_v2.17.1_darwin_arm64.tar.gz")
	if err != nil {
		return err
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		return fmt.Errorf("runtime download: HTTP %d", r.StatusCode)
	}
	f, err := os.CreateTemp(dir, "runtime-*.tar.gz")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(io.MultiWriter(f, h), io.LimitReader(r.Body, 200<<20)); err != nil {
		return err
	}
	if fmt.Sprintf("%x", h.Sum(nil)) != digest {
		return fmt.Errorf("runtime archive checksum mismatch")
	}
	if _, err = f.Seek(0, 0); err != nil {
		return err
	}
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if filepath.Base(header.Name) != filepath.Base(target) || header.Typeflag != tar.TypeReg {
			continue
		}
		out, err := os.CreateTemp(dir, "runtime-*.dylib")
		if err != nil {
			return err
		}
		defer os.Remove(out.Name())
		_, copyErr := io.Copy(out, tr)
		closeErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		if err = os.Rename(out.Name(), target); err != nil {
			return err
		}
		fmt.Println("LiteRT 2.17.1:", target)
		return nil
	}
	return fmt.Errorf("runtime library missing from archive")
}
