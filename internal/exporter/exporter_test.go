package exporter

import (
	"context"
	"encoding/json"
	"github.com/foursmith/player/internal/projection"
	"github.com/foursmith/player/internal/subtitle"
	"github.com/foursmith/player/internal/video"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestCodecExportsIncludeAudioAndTrim(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("FFmpeg toolchain unavailable")
	}
	dir := t.TempDir()
	source := filepath.Join(dir, "source.mp4")
	args := []string{"-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "color=c=red:s=320x160:r=24:d=1.5", "-f", "lavfi", "-i", "sine=frequency=440:duration=1.5", "-c:v", "libx264", "-c:a", "aac", "-shortest", source}
	if out, err := exec.Command("ffmpeg", args...).CombinedOutput(); err != nil {
		t.Fatalf("fixture: %v: %s", err, out)
	}
	for _, mode := range []projection.Mode{projection.Mono360, projection.Flat} {
		for _, format := range []string{"mp4", "webm", "motion-photo"} {
			t.Run(projection.IDs[mode]+"/"+format, func(t *testing.T) {
				destination := filepath.Join(dir, "clip."+format)
				progress := 0
				err := Export(context.Background(), Options{Source: source, Destination: destination, Format: format, Start: .25, End: .75, Width: 96, Height: 160, FPS: 24, BitRate: 300000, CSSWidth: 96, Cues: []subtitle.Cue{{Start: .25, End: .75, Text: "中文 • subtitles"}}, Parameters: video.Parameters{Mode: mode, View: projection.DefaultView(), SplitCount: 1}, Progress: func(p int) { progress = p }})
				if err != nil {
					t.Fatal(err)
				}
				if progress != 100 {
					t.Fatal(progress)
				}
				if format == "motion-photo" {
					data, err := os.ReadFile(destination)
					if err != nil || len(data) < 1000 || data[0] != 0xff || data[1] != 0xd8 {
						t.Fatal("missing motion photo", err)
					}
					return
				}
				data, err := exec.Command("ffprobe", "-v", "error", "-show_streams", "-of", "json", destination).Output()
				if err != nil {
					t.Fatal(err)
				}
				var probe struct {
					Streams []struct {
						CodecType     string `json:"codec_type"`
						Width, Height int
					}
				}
				if err = json.Unmarshal(data, &probe); err != nil {
					t.Fatal(err)
				}
				audio, videoFound := false, false
				for _, s := range probe.Streams {
					if s.CodecType == "audio" {
						audio = true
					}
					if s.CodecType == "video" {
						videoFound = s.Width == 96 && s.Height == 160
					}
				}
				if !audio || !videoFound {
					t.Fatalf("missing encoded streams: %s", data)
				}
			})
		}
	}
}
