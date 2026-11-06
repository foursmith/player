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

	entered, released := make(chan struct{}), make(chan struct{})
	var release sync.Once
	unblock := func() { release.Do(func() { close(released) }) }
	defer unblock()
	var intercept atomic.Bool
	intercept.Store(true)
	receive := av.codec.Receive
	t.Cleanup(func() { av.codec.Receive = receive })
	av.codec.Receive = func(ctx *avCodecContext, out *avFrame) int32 {
		ret := receive(ctx, out)
		if ret >= 0 && ctx.Width > 0 && intercept.CompareAndSwap(true, false) {
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
	waitState(t, p, func(s State) bool { return s.Generation >= before.Generation+2 && !s.Loading })
	for len(states) > 0 {
		s := <-states
		if s.Generation == before.Generation+1 && !s.Loading {
			t.Fatal("obsolete seek completed before processing the latest target")
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
