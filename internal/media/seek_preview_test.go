package media

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"image/color"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"
	"time"
)

func chapterPreviewFixture(t *testing.T, extension string) string {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("FFmpeg fixture encoder unavailable")
	}
	dir := t.TempDir()
	metadata := filepath.Join(dir, "chapters.txt")
	text := ";FFMETADATA1\n[CHAPTER]\nTIMEBASE=1/1000\nSTART=0\nEND=400\ntitle=Intro\n[CHAPTER]\nTIMEBASE=1/1000\nSTART=400\nEND=800\ntitle=正文\n[CHAPTER]\nTIMEBASE=1/1000\nSTART=800\nEND=1200\ntitle=Ending\n"
	if err := os.WriteFile(metadata, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "chapters."+extension)
	filter := "color=c=black:s=80x40:r=25:d=0.4[a];color=c=red:s=80x40:r=25:d=0.4[b];color=c=blue:s=80x40:r=25:d=0.4[c];[a][b][c]concat=n=3:v=1:a=0"
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	args := []string{"-v", "error", "-f", "lavfi", "-i", filter, "-f", "ffmetadata", "-i", metadata, "-map", "0:v", "-map_metadata", "1", "-map_chapters", "1", "-c:v", "libx264", "-threads", "1", "-g", "1000", "-sc_threshold", "0", "-pix_fmt", "yuv420p", file}
	if out, err := exec.CommandContext(ctx, "ffmpeg", args...).CombinedOutput(); err != nil {
		t.Fatalf("fixture: %v: %s", err, out)
	}
	return file
}

func TestChaptersAndSeekPreviewThroughSparseKeyframes(t *testing.T) {
	for _, extension := range []string{"mp4", "mkv"} {
		t.Run(extension, func(t *testing.T) {
			file := chapterPreviewFixture(t, extension)
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			d, err := Open(ctx, Source{URL: file}, false)
			if err != nil {
				t.Fatal(err)
			}
			defer d.Close()
			chapters := d.Metadata().Chapters
			if len(chapters) != 3 || chapters[1].Title != "正文" || chapters[1].Start != .4 || chapters[2].Start != .8 {
				t.Fatalf("container chapters lost: %+v", chapters)
			}
			if ChapterAt(chapters, .399) != 0 || ChapterAt(chapters, .4) != 1 || ChapterAt(chapters, .8) != 2 {
				t.Fatal("chapter boundary maps to the wrong chapter")
			}
			if err := d.Seek(.5); err != nil {
				t.Fatal(err)
			}
			preview, err := OpenSeekPreview(ctx, Source{URL: file})
			if err != nil {
				t.Fatal(err)
			}
			defer preview.Close()
			for _, sample := range []struct {
				at        float64
				red, blue bool
			}{{1.0, false, true}, {.1, false, false}, {.55, true, false}, {100, false, true}, {.4, true, false}} {
				img, err := preview.Image(sample.at, true, nil)
				if err != nil {
					t.Fatal(sample.at, err)
				}
				pixel := color.RGBAModel.Convert(img.At(100, 50)).(color.RGBA)
				if img.Bounds().Dx() > 240 || img.Bounds().Dy() > 135 || (pixel.R > 150) != sample.red || (pixel.B > 150) != sample.blue {
					t.Fatalf("wrong preview at %.2f: bounds=%v pixel=%v", sample.at, img.Bounds(), pixel)
				}
			}
			frame, err := d.Next()
			if err != nil {
				t.Fatal(err)
			}
			defer frame.Close()
			if frame.PTS() < .5 || frame.PTS() > .55 {
				t.Fatalf("preview moved the playing decoder: %.3f", frame.PTS())
			}
			if _, err := preview.Image(.2, true, func() bool { return true }); !errors.Is(err, context.Canceled) {
				t.Fatal("obsolete preview did not yield", err)
			}
			cancel()
			if _, err := preview.Image(.6, true, nil); !errors.Is(err, context.Canceled) {
				t.Fatal("preview ignored source cancellation", err)
			}
		})
	}
}

func TestSeekPreviewReadsHTTPRangeSource(t *testing.T) {
	file := chapterPreviewFixture(t, "mp4")
	server := httptest.NewServer(http.FileServer(http.Dir(filepath.Dir(file))))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	p, err := OpenSeekPreview(ctx, Source{URL: server.URL + "/" + filepath.Base(file)})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	img, err := p.Image(.6, true, nil)
	if err != nil || img.RGBAAt(100, 50).R < 150 {
		t.Fatal("HTTP preview lost accurate seeking", err)
	}
}

// Put all video chunks before a gap and the audio chunks. Chunk tables still
// describe the same movie, but alternating tracks now requires distant reads.
func separatedTracksFixture(t *testing.T) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "separated.mp4")
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("FFmpeg fixture encoder unavailable")
	}
	args := []string{"-v", "error", "-f", "lavfi", "-i", "testsrc2=size=640x360:rate=25:duration=3", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000:duration=3", "-c:v", "libx264", "-preset", "ultrafast", "-threads", "1", "-g", "1000", "-sc_threshold", "0", "-pix_fmt", "yuv420p", "-c:a", "aac", file}
	if out, err := exec.CommandContext(ctx, "ffmpeg", args...).CombinedOutput(); err != nil {
		t.Fatalf("separated tracks fixture: %v: %s", err, out)
	}
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var tracks [][]uint32
	offsets := map[uint32]uint32{}
	var rewrite func([]byte, bool) []byte
	rewrite = func(input []byte, collect bool) []byte {
		return mp4Children(t, input, func(kind string, payload []byte) []byte {
			switch kind {
			case "moov", "trak", "mdia", "minf", "stbl":
				payload = rewrite(payload, collect)
			case "stco":
				payload = bytes.Clone(payload)
				var chunks []uint32
				for i := 8; i+4 <= len(payload); i += 4 {
					at := binary.BigEndian.Uint32(payload[i:])
					if collect {
						chunks = append(chunks, at)
					} else {
						binary.BigEndian.PutUint32(payload[i:], offsets[at])
					}
				}
				if collect {
					tracks = append(tracks, chunks)
				}
			}
			return mp4Atom(kind, payload)
		})
	}
	rewrite(data, true)
	if len(tracks) != 2 {
		t.Fatalf("expected video and audio chunk tables, got %d", len(tracks))
	}
	for pos := 0; pos+8 <= len(data); {
		size := int(binary.BigEndian.Uint32(data[pos:]))
		if size < 8 || pos+size > len(data) {
			t.Fatal("invalid fixture atom")
		}
		if string(data[pos+4:pos+8]) != "mdat" {
			pos += size
			continue
		}
		ends := append(slices.Concat(tracks...), uint32(pos+size))
		slices.Sort(ends)
		var payload []byte
		for i, chunks := range tracks {
			for _, at := range chunks {
				index, _ := slices.BinarySearch(ends, at)
				offsets[at] = uint32(pos + 8 + len(payload))
				payload = append(payload, data[at:ends[index+1]]...)
			}
			if i == 0 {
				payload = append(payload, make([]byte, 2<<20)...)
			}
		}
		out := append(bytes.Clone(data[:pos]), mp4Atom("mdat", payload)...)
		out = append(out, rewrite(data[pos+size:], false)...)
		if err := os.WriteFile(file, out, 0600); err != nil {
			t.Fatal(err)
		}
		return file
	}
	t.Fatal("fixture has no media data")
	return ""
}

func TestHTTPSeekPreviewSkipsDistantAudioTrack(t *testing.T) {
	file := separatedTracksFixture(t)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.ServeFile(w, r, file)
	}))
	t.Cleanup(server.Close)
	p, err := OpenSeekPreview(t.Context(), Source{URL: server.URL + "/clip.mp4"})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	before := requests.Load()
	read := av.format.Read
	var audioPackets int
	av.format.Read = func(format *avFormat, packet *avPacket) int32 {
		ret := read(format, packet)
		if ret >= 0 && format == p.decoder.format && packet.StreamIndex != p.decoder.videoStream {
			audioPackets++
		}
		return ret
	}
	t.Cleanup(func() { av.format.Read = read })
	for _, at := range []float64{2.4, 2.8, .52} {
		img, err := p.Image(at, true, nil)
		if err != nil {
			t.Fatal(at, err)
		}
		if p.lastPTS < at-.0001 || p.lastPTS > at+.041 || img.Bounds().Dx() != 240 || img.Bounds().Dy() != 135 {
			t.Fatal("distant audio changed the precise video frame", at, p.lastPTS)
		}
	}
	if got := requests.Load() - before; got > 8 {
		t.Fatalf("precise previews repeatedly fetched distant audio: %d range requests", got)
	}
	if audioPackets != 0 {
		t.Fatalf("silent previews read %d unused audio packets", audioPackets)
	}
}

func TestSeekPreviewReusesForwardDecodeAndRetainsCachedPixels(t *testing.T) {
	p, err := OpenSeekPreview(context.Background(), Source{URL: chapterPreviewFixture(t, "mp4")})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	seek := av.format.Seek
	seeks := 0
	av.format.Seek = func(format *avFormat, index int32, minTS, target, maxTS int64, flags int32) int32 {
		seeks++
		return seek(format, index, minTS, target, maxTS, flags)
	}
	defer func() { av.format.Seek = seek }()
	first, err := p.Image(.44, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	before := bytes.Clone(first.Pix)
	for _, at := range []float64{.6, 1.0, .12} {
		if _, err := p.Image(at, true, nil); err != nil {
			t.Fatal(at, err)
		}
		if p.lastPTS < at || p.lastPTS > at+.041 {
			t.Fatal("reused decode returned the wrong timestamp", at, p.lastPTS)
		}
	}
	if seeks != 2 {
		t.Fatalf("forward preview repeatedly flushed its decoder: got %d seeks, want initial and backwards seeks only", seeks)
	}
	if !bytes.Equal(first.Pix, before) {
		t.Fatal("converter reuse changed a cached image")
	}
}

func TestKeyframePreviewCanSwitchToPreciseScrubbing(t *testing.T) {
	p, err := OpenSeekPreview(context.Background(), Source{URL: chapterPreviewFixture(t, "mp4")})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	quick, err := p.Image(.6, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if quick.RGBAAt(100, 50).R > 35 {
		t.Fatal("test fixture did not return its early black keyframe")
	}
	precise, err := p.Image(.6, true, nil)
	if err != nil || precise.RGBAAt(100, 50).R < 150 {
		t.Fatal("fast preview did not refine to the requested scene", err)
	}
	if quick.RGBAAt(100, 50).R > 35 {
		t.Fatal("refinement rewrote the previously returned preview")
	}
}
