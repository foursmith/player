package media

import (
	"context"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestProbeReadsContainerInfoWithoutPlayback(t *testing.T) {
	file := fixture(t, true)
	for _, extension := range []string{".mp4", ".mkv"} {
		path := file
		if extension == ".mkv" {
			path = filepath.Join(t.TempDir(), "clip.mkv")
			if output, err := exec.Command("ffmpeg", "-v", "error", "-i", file, "-c", "copy", path).CombinedOutput(); err != nil {
				t.Fatalf("create MKV: %v %s", err, output)
			}
		}
		info, err := Probe(context.Background(), path)
		if err != nil || info.Width != 320 || info.Height != 160 || math.Abs(info.Duration-1) > .1 {
			t.Fatalf("container metadata missing for %s: %+v %v", extension, info, err)
		}
	}
}

func TestProbeCancellationInterruptsBlockedOpen(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := Probe(ctx, server.URL); done <- err }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("probe did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal("probe lost cancellation", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("probe cancellation did not interrupt the network open")
	}
}
