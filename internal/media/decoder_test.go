package media

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"image/color"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// Add ISO BMFF spatial-video boxes to the fixture's video sample entry. The
// fixture stores moov after mdat, so growing moov leaves chunk offsets intact.
func spatialFixture(t *testing.T, stereo byte, half bool) string {
	t.Helper()
	file := fixture(t, false)
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	bounds := make([]byte, 20)
	if half {
		binary.BigEndian.PutUint32(bounds[12:16], 0x40000000)
		binary.BigEndian.PutUint32(bounds[16:20], 0x40000000)
	}
	projection := mp4Atom("proj", append(mp4Atom("prhd", make([]byte, 16)), mp4Atom("equi", bounds)...))
	spherical := mp4Atom("sv3d", append(mp4Atom("svhd", append(make([]byte, 4), []byte("Foursmith test\x00")...)), projection...))
	extra := append(mp4Atom("st3d", []byte{0, 0, 0, 0, stereo}), spherical...)
	inserted := false
	var rewrite func([]byte) []byte
	rewrite = func(input []byte) []byte {
		return mp4Children(t, input, func(kind string, payload []byte) []byte {
			switch kind {
			case "moov", "trak", "mdia", "minf", "stbl":
				payload = rewrite(payload)
			case "stsd":
				payload = append(bytes.Clone(payload[:8]), rewrite(payload[8:])...)
			case "avc1":
				payload = append(bytes.Clone(payload), extra...)
				inserted = true
			}
			return mp4Atom(kind, payload)
		})
	}
	data = rewrite(data)
	if !inserted {
		t.Fatal("video sample entry not found")
	}
	if err = os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
	return file
}

func TestDecoderReadsSpatialMetadata(t *testing.T) {
	for _, tc := range []struct {
		stereo byte
		want   string
		half   bool
	}{{0, "mono", false}, {1, "tb", false}, {2, "sbs", false}, {0, "mono", true}, {2, "sbs", true}} {
		d, err := Open(context.Background(), Source{URL: spatialFixture(t, tc.stereo, tc.half)}, false)
		if err != nil {
			t.Fatal(err)
		}
		metadata := d.Metadata()
		d.Close()
		projection := "equirectangular"
		if tc.half {
			projection = "half-equirectangular"
		}
		if metadata.Spherical != projection || metadata.Stereo != tc.want {
			t.Fatalf("spatial metadata missing: %+v", metadata)
		}
	}
}

func fixture(t *testing.T, audio bool) string {
	t.Helper()
	binary, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("FFmpeg fixture encoder unavailable")
	}
	path := filepath.Join(t.TempDir(), "clip.mp4")
	args := []string{"-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "color=c=red:s=320x160:r=30:d=1"}
	if audio {
		args = append(args, "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000:duration=1", "-c:a", "aac")
	}
	args = append(args, "-c:v", "libx264", "-g", "30", "-pix_fmt", "yuv420p", "-y", path)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if output, err := exec.CommandContext(ctx, binary, args...).CombinedOutput(); err != nil {
		t.Fatalf("fixture: %v: %s", err, output)
	}
	return path
}
func TestClosingPlayerInterruptsBlockedNetworkOpen(t *testing.T) {
	entered := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(entered); <-r.Context().Done() }))
	defer server.Close()
	p := NewPlayer(nil)
	p.Open(Source{URL: server.URL + "/video.mp4"})
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		p.Close()
		t.Fatal("network input did not start")
	}
	closed := make(chan struct{})
	go func() { p.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("network decoder blocked application close")
	}
}
func TestDecoderFramesAndAccurateSeek(t *testing.T) {
	d, err := Open(context.Background(), Source{URL: fixture(t, false)}, false)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	metadata := d.Metadata()
	if metadata.Width != 320 || metadata.Height != 160 || math.Abs(metadata.Duration-1) > .05 {
		t.Fatalf("metadata: %+v", metadata)
	}
	previous := -1.0
	count := 0
	for {
		frame, err := d.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if frame.PTS() < previous {
			t.Fatal("timestamps went backward")
		}
		previous = frame.PTS()
		if count == 0 {
			img, err := frame.RGBA(80, 40)
			if err != nil {
				t.Fatal(err)
			}
			pixel := color.RGBAModel.Convert(img.At(40, 20)).(color.RGBA)
			if pixel.R < 200 || pixel.G > 35 || pixel.B > 35 {
				t.Fatalf("wrong decoded color: %+v", pixel)
			}
		}
		frame.Close()
		count++
	}
	if count != 30 {
		t.Fatalf("lost decoder frames: %d", count)
	}
	if err = d.Seek(.45); err != nil {
		t.Fatal(err)
	}
	frame, err := d.Next()
	if err != nil {
		t.Fatal(err)
	}
	defer frame.Close()
	if frame.PTS() < .45 || frame.PTS() > .49 {
		t.Fatalf("inaccurate seek: %v", frame.PTS())
	}
}

func TestAnalysisAVCDecoderReturnsFramesAndPreservesAccurateSeek(t *testing.T) {
	d, err := openContext(context.Background(), Source{URL: fixture(t, false)}, false, true)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if name := d.Metadata().Decoder; name != "h264" {
		t.Fatal("software analysis decoder allocated hardware:", name)
	}
	for _, at := range []float64{0, .45, .2} {
		if err := d.Seek(at); err != nil {
			t.Fatal(err)
		}
		frame, err := d.Next()
		if err != nil {
			t.Fatal(err)
		}
		if frame.Native() != nil || frame.PTS() < at || frame.PTS() > at+.04 {
			t.Fatalf("unexpected software frame at %.2f: native=%v pts=%v", at, frame.Native(), frame.PTS())
		}
		img, err := frame.RGBA(80, 40)
		frame.Close()
		if err != nil || img.RGBAAt(40, 20).R < 200 {
			t.Fatalf("software thumbnail failed: %v", err)
		}
	}
}
func waitState(t *testing.T, p *Player, predicate func(State) bool) State {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		state := p.Snapshot()
		if state.Error != "" {
			t.Fatal(state.Error)
		}
		if predicate(state) {
			return state
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("player timed out: %+v", p.Snapshot())
	return State{}
}
func TestPlayerAudioPauseSeekAndEnd(t *testing.T) {
	t.Setenv("SDL_AUDIO_DRIVER", "dummy")
	// SDL evaluates its audio driver when the first audio decoder opens.
	if os.Getenv("CI") == "true" {
		t.Setenv("SDL_AUDIO_DRIVER", "dummy")
	}
	p := NewPlayer(nil)
	defer p.Close()
	p.Open(Source{URL: fixture(t, true)})
	first := waitState(t, p, func(s State) bool { return !s.Loading && s.Position > .12 })
	p.Pause()
	paused := waitState(t, p, func(s State) bool { return !s.Playing })
	time.Sleep(80 * time.Millisecond)
	if after := p.Snapshot(); math.Abs(after.Position-paused.Position) > .01 {
		t.Fatal("paused clock kept moving")
	}
	p.Seek(.5)
	sought := waitState(t, p, func(s State) bool { return s.Generation > first.Generation && s.Position >= .5 && !s.Loading })
	if !sought.Playing {
		t.Fatal("seeking must resume playback")
	}
	p.SetRate(1.5)
	waitState(t, p, func(s State) bool { return s.Ended })
	p.Reset()
	waitState(t, p, func(s State) bool { return s.URL == "" })
	if f := p.Frame(); f != nil {
		f.Close()
		t.Fatal("reset retained previous media frame")
	}
}

func TestSeekPublishesLoadingUntilNewFrame(t *testing.T) {
	t.Setenv("SDL_AUDIO_DRIVER", "dummy")
	transitions := make(chan State, 128)
	var p *Player
	p = NewPlayer(func() {
		select {
		case transitions <- p.Snapshot():
		default:
		}
	})
	defer p.Close()
	p.Open(Source{URL: fixture(t, true)})
	waitState(t, p, func(s State) bool { return !s.Loading && s.Position > .1 })
	p.Pause()
	before := waitState(t, p, func(s State) bool { return !s.Playing })
	// An outstanding detector may retain the old texture for its whole job.
	// Seeking must publish the new picture before that reference is released.
	held := p.Frame()
	if held == nil {
		t.Fatal("missing pre-seek frame")
	}
	defer held.Close()
	p.Seek(.7)
	deadline := time.After(3 * time.Second)
	for {
		select {
		case state := <-transitions:
			if state.Generation <= before.Generation {
				continue
			}
			if !state.Loading || state.Position != .7 {
				t.Fatalf("new seek generation advertised an old frame as ready: %+v", state)
			}
			ready := waitState(t, p, func(s State) bool { return !s.Loading && s.Generation > before.Generation })
			frame := p.Frame()
			if frame == nil {
				t.Fatal("seek completed without a frame")
			}
			defer frame.Close()
			if !ready.Playing || frame.PTS() < .7 {
				t.Fatalf("seek completed with stale video: state=%+v frame=%v", ready, frame.PTS())
			}
			return
		case <-deadline:
			t.Fatal("seek did not publish its loading state")
		}
	}
}

func TestPlayerContinuesAcrossChunkedAudio(t *testing.T) {
	t.Setenv("SDL_AUDIO_DRIVER", "dummy")
	encoder, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("FFmpeg fixture encoder unavailable")
	}
	path := filepath.Join(t.TempDir(), "chunked.mp4")
	// Each fragment stores half a second of video before its audio. A player
	// that stops demuxing at its video decode-ahead limit starves the audio
	// clock before reaching the next audio chunk.
	args := []string{"-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "color=c=red:s=320x160:r=60:d=2", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000:duration=2", "-c:v", "libx264", "-preset", "ultrafast", "-bf", "0", "-c:a", "aac", "-movflags", "+frag_keyframe+empty_moov", "-frag_duration", "500000", "-y", path}
	if out, err := exec.Command(encoder, args...).CombinedOutput(); err != nil {
		t.Fatalf("chunked fixture: %v: %s", err, out)
	}
	p := NewPlayer(nil)
	defer p.Close()
	p.Open(Source{URL: path})
	waitState(t, p, func(s State) bool { return s.Position > 1.5 })
	waitState(t, p, func(s State) bool { return s.Ended })
}

func TestAudioPrefetchPreservesVideoAndClearsOnSeek(t *testing.T) {
	t.Setenv("SDL_AUDIO_DRIVER", "dummy")
	d, err := Open(context.Background(), Source{URL: fixture(t, true)}, true)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	// Read through EOF before decoding video, then seek with compressed
	// pictures still queued. Neither the old packets nor EOF may survive.
	for _, at := range []float64{0, .45, .1} {
		if at > 0 {
			if err := d.Seek(at); err != nil {
				t.Fatal(err)
			}
		}
		if err := d.bufferAudio(2, nil); err != nil {
			t.Fatal(err)
		}
		frame, err := d.Next()
		if err != nil {
			t.Fatal(err)
		}
		pts := frame.PTS()
		frame.Close()
		if pts < at || pts > at+1.0/30+.001 {
			t.Fatalf("prefetched seek %v returned %v", at, pts)
		}
	}
	count := 1
	for {
		frame, err := d.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		frame.Close()
		count++
	}
	if count != 27 {
		t.Fatalf("prefetch lost or duplicated video frames: %d", count)
	}
}

func TestPlaybackDecoderReordersConvertsAndSeeks(t *testing.T) {
	t.Setenv("SDL_AUDIO_DRIVER", "dummy")
	d, err := Open(context.Background(), Source{URL: fixture(t, true)}, true)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	var converter RGBAConverter
	defer converter.Close()
	// The default x264 fixture contains B pictures. Exercise the playback
	// decoder, including the final delayed pictures and hardware readback.
	for i := 0; i < 30; i++ {
		frame, err := d.Next()
		if err != nil {
			t.Fatalf("picture %d: %v", i, err)
		}
		if math.Abs(frame.PTS()-float64(i)/30) > .0001 {
			t.Errorf("picture %d reordered incorrectly: %v", i, frame.PTS())
		}
		img, err := converter.Convert(frame, 80, 40)
		if err != nil {
			t.Fatal(err)
		}
		pixel := img.RGBAAt(40, 20)
		if pixel.R < 200 || pixel.G > 35 || pixel.B > 35 {
			t.Errorf("wrong playback color: %+v", pixel)
		}
		frame.Close()
	}
	if frame, err := d.Next(); err != io.EOF {
		frame.Close()
		t.Fatalf("expected EOF, got %v", err)
	}
	if err := d.Seek(.45); err != nil {
		t.Fatal(err)
	}
	frame, err := d.Next()
	if err != nil {
		t.Fatal(err)
	}
	defer frame.Close()
	if frame.PTS() < .45 || frame.PTS() > .49 {
		t.Fatalf("inaccurate playback seek: %v", frame.PTS())
	}
	if _, err := converter.Convert(frame, 80, 40); err != nil {
		t.Fatal("converter failed after seeking", err)
	}
	t.Log("playback decoder:", d.Metadata().Decoder)
}

func TestCancellationStopsBufferedMedia(t *testing.T) {
	t.Setenv("SDL_AUDIO_DRIVER", "dummy")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d, err := Open(ctx, Source{URL: fixture(t, true)}, true)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := d.bufferAudio(2, nil); err != nil {
		t.Fatal(err)
	}
	frame, err := d.Next()
	if err != nil {
		t.Fatal(err)
	}
	frame.Close()
	// Both compressed packets and decoded hardware pictures are available;
	// cancellation must not depend on the next network/disk read.
	cancel()
	if frame, err := d.Next(); !errors.Is(err, context.Canceled) {
		frame.Close()
		t.Fatalf("buffered decode ignored cancellation: %v", err)
	}
	if err := d.Seek(.5); !errors.Is(err, context.Canceled) {
		t.Fatalf("seek ignored cancellation: %v", err)
	}
	if err := d.bufferAudio(2, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("prefetch ignored cancellation: %v", err)
	}
}

func TestSwitchingPlayerReplacesBlockedAndQueuedOpens(t *testing.T) {
	t.Setenv("SDL_AUDIO_DRIVER", "dummy")
	path := fixture(t, true)
	entered := make(chan struct{}, 16)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered <- struct{}{}
		<-r.Context().Done()
	}))
	defer server.Close()
	p := NewPlayer(nil)
	defer p.Close()
	p.Open(Source{URL: server.URL + "/first.mp4"})
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("network input did not start")
	}
	for i := 0; i < 8; i++ {
		p.Open(Source{URL: server.URL + "/obsolete.mp4"})
	}
	start := time.Now()
	p.Open(Source{URL: path})
	waitState(t, p, func(s State) bool { return s.URL == path && !s.Loading })
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("latest selection waited for obsolete network opens: %v", elapsed)
	}
}
