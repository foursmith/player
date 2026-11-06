package media

import (
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewSeekInterruptsObsoletePreroll(t *testing.T) {
	t.Setenv("SDL_AUDIO_DRIVER", "dummy")
	file := fixture(t, true)
	states := make(chan State, 256)
	var p *Player
	p = NewPlayer(func() {
		select {
		case states <- p.Snapshot():
		default:
		}
	})
	defer p.Close()
	p.Open(Source{URL: file})
	waitState(t, p, func(s State) bool { return !s.Loading && s.Position > .1 })
	p.Pause()
	before := waitState(t, p, func(s State) bool { return !s.Playing })
	t.Logf("decoder: %s", before.Metadata.Decoder)

	entered, released := make(chan struct{}), make(chan struct{})
	var release sync.Once
	unblock := func() { release.Do(func() { close(released) }) }
	defer unblock()
	var intercept atomic.Bool
	intercept.Store(true)
	read := av.format.Read
	t.Cleanup(func() { av.format.Read = read })
	av.format.Read = func(format *avFormat, packet *avPacket) int32 {
		ret := read(format, packet)
		// Both native VideoToolbox and FFmpeg read video packets here. Hold
		// the obsolete seek inside its preroll, before it can reach .8s.
		if ret >= 0 && packet.StreamIndex == 0 && intercept.CompareAndSwap(true, false) {
			close(entered)
			<-released
		}
		return ret
	}
	p.Seek(.8)
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("seek did not begin decoding preroll")
	}
	p.Seek(.2)
	unblock()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case s := <-states:
			if s.Error != "" {
				t.Fatal(s.Error)
			}
			if s.Generation == before.Generation+1 && !s.Loading {
				t.Fatal("obsolete seek completed before processing the latest target")
			}
			if s.Generation >= before.Generation+2 && !s.Loading {
				// Check the first published picture, before playback can advance.
				if s.Position < .2 || s.Position > .2+1/before.Metadata.FrameRate {
					t.Fatalf("latest seek presented the wrong picture: %.6fs", s.Position)
				}
				frame := p.Frame()
				if frame == nil {
					t.Fatal("latest seek completed without a frame")
				}
				defer frame.Close()
				if frame.PTS() < .2 {
					t.Fatalf("latest seek retained a stale picture: %.6fs", frame.PTS())
				}
				return
			}
		case <-deadline:
			t.Fatalf("latest seek did not complete: %+v", p.Snapshot())
		}
	}
}

func TestFirstPictureDoesNotWaitForFutureAudio(t *testing.T) {
	t.Setenv("SDL_AUDIO_DRIVER", "dummy")
	encoder, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("FFmpeg fixture encoder unavailable")
	}
	file := filepath.Join(t.TempDir(), "video-before-audio.mp4")
	args := []string{"-v", "error", "-f", "lavfi", "-i", "testsrc2=s=320x160:r=60:d=2", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000:duration=2", "-c:v", "libx264", "-preset", "ultrafast", "-bf", "0", "-g", "120", "-c:a", "aac", "-movflags", "+frag_keyframe+empty_moov", "-frag_duration", "1000000", file}
	if out, err := exec.Command(encoder, args...).CombinedOutput(); err != nil {
		t.Fatalf("fixture: %v: %s", err, out)
	}
	if err := loadMedia(); err != nil {
		t.Fatal(err)
	}
	p := NewPlayer(nil)
	defer p.Close()
	entered, released := make(chan struct{}), make(chan struct{})
	var release sync.Once
	unblock := func() { release.Do(func() { close(released) }) }
	defer unblock()
	var intercepted atomic.Bool
	read := av.format.Read
	t.Cleanup(func() { av.format.Read = read })
	av.format.Read = func(format *avFormat, packet *avPacket) int32 {
		ret := read(format, packet)
		// The fragment stores one second of video before the first audio
		// packet. Hold that future read to model a slow network chunk.
		if ret >= 0 && packet.StreamIndex == 1 && intercepted.CompareAndSwap(false, true) {
			close(entered)
			<-released
		}
		return ret
	}
	p.Open(Source{URL: file})
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("player did not reach the future audio chunk")
	}
	state := p.Snapshot()
	frame := p.Frame()
	if frame == nil || state.Loading {
		if frame != nil {
			frame.Close()
		}
		t.Fatal("first picture was withheld while prefetching future audio")
	}
	frame.Close()
	unblock()
	waitState(t, p, func(s State) bool { return s.Position > 1.2 })
}

func TestVideoFramesDoNotNotifyUIStateChanges(t *testing.T) {
	t.Setenv("SDL_AUDIO_DRIVER", "dummy")
	var changes atomic.Int64
	p := NewPlayer(func() { changes.Add(1) })
	defer p.Close()
	p.Open(Source{URL: fixture(t, true)})
	first := waitState(t, p, func(s State) bool { return !s.Loading && s.Position > .12 })
	before := changes.Load()
	later := waitState(t, p, func(s State) bool { return s.Position > .45 })
	if later.FrameID == first.FrameID || later.FrameID == 0 {
		t.Fatal("video did not advance independently of UI state")
	}
	if got := changes.Load(); got != before {
		t.Fatalf("ordinary video frames rebuilt UI state: %d notifications", got-before)
	}
	p.Pause()
	paused := waitState(t, p, func(s State) bool { return !s.Playing && changes.Load() > before })
	frame := p.Frame()
	if frame == nil {
		t.Fatal("paused player lost its display frame")
	}
	defer frame.Close()
	if frame.ID() != paused.FrameID {
		t.Fatalf("snapshot refers to a different display frame: snapshot=%d frame=%d", paused.FrameID, frame.ID())
	}
}
