package media

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// HLS segment requests must retain authentication after the manifest is opened.
func TestAuthenticatedHLSReachesTheDecoder(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("FFmpeg fixture encoder unavailable")
	}
	dir := t.TempDir()
	playlist := filepath.Join(dir, "playlist.m3u8")
	args := []string{"-v", "error", "-f", "lavfi", "-i", "color=c=red:s=160x80:r=10:d=1", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-g", "5", "-hls_time", "0.5", "-hls_list_size", "0", playlist}
	if output, err := exec.Command("ffmpeg", args...).CombinedOutput(); err != nil {
		t.Fatalf("fixture: %v %s", err, output)
	}
	var denied, segments atomic.Int64
	files := http.FileServer(http.Dir(dir))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Emby-Token") != "token" {
			denied.Add(1)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if filepath.Ext(r.URL.Path) == ".ts" {
			segments.Add(1)
		}
		if filepath.Ext(r.URL.Path) == ".m3u8" {
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		}
		files.ServeHTTP(w, r)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	decoder, err := Open(ctx, Source{URL: server.URL + "/playlist.m3u8", Headers: "X-Emby-Token: token\r\n"}, false)
	if err != nil {
		t.Fatal(err)
	}
	defer decoder.Close()
	frame, err := decoder.Next()
	if err != nil {
		t.Fatal(err)
	}
	frame.Close()
	if denied.Load() != 0 || segments.Load() == 0 {
		t.Fatal("HLS authentication or segment loading failed", denied.Load(), segments.Load())
	}
}
