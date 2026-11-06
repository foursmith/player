package media

import (
	"context"
	"image"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/foursmith/player/library"
)

func TestPluginAuthenticationReachesPlaybackAndIndependentReaders(t *testing.T) {
	file := fixture(t, false)
	var authenticated, denied atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test" {
			denied.Add(1)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		authenticated.Add(1)
		http.ServeFile(w, r, file)
	}))
	defer server.Close()
	entry := library.Entry{Path: server.URL + "/video.mp4", Playback: &library.Playback{Resource: library.Resource{Headers: http.Header{"Authorization": []string{"Bearer test"}}}, Seekable: true}}
	source := FromEntry(entry)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	d, err := Open(ctx, source, false)
	if err != nil {
		t.Fatal("authenticated playback", err)
	}
	frame, err := d.Next()
	if err != nil {
		d.Close()
		t.Fatal(err)
	}
	frame.Close()
	d.Close()
	if _, err := Probe(ctx, source); err != nil {
		t.Fatal("authenticated metadata", err)
	}
	if _, err := Thumbnail(ctx, source); err != nil {
		t.Fatal("authenticated thumbnail", err)
	}
	if err := SampleFrames(ctx, source, func(*image.RGBA) bool { return true }, nil); err != nil {
		t.Fatal("authenticated projection sample", err)
	}
	preview, err := OpenSeekPreview(ctx, source)
	if err != nil {
		t.Fatal("authenticated seek preview", err)
	}
	preview.Close()
	if _, err := OpenChapterArtwork(ctx, source); err != ErrNoChapterArtwork {
		t.Fatal("authenticated artwork reader", err)
	}
	if denied.Load() != 0 || authenticated.Load() < 6 {
		t.Fatal("an independent reader dropped authentication", authenticated.Load(), denied.Load())
	}
}

func TestPluginHeadersSurviveSecondaryRangeCursor(t *testing.T) {
	const size = int64(4 << 30)
	var denied atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer ranges" {
			denied.Add(1)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		http.ServeContent(w, r, "video.mp4", time.Time{}, &httpPattern{size: size})
	}))
	defer server.Close()
	if err := loadMedia(); err != nil {
		t.Fatal(err)
	}
	d := &Decoder{ctx: context.Background(), cancellation: newCancellation(), format: av.format.Alloc()}
	d.stop = context.AfterFunc(d.ctx, d.cancellation.cancel)
	defer d.Close()
	d.format.InterruptCallback = avInterrupt{interruptCallback, d.cancellation.token}
	if err := d.openHTTP(server.URL, "Authorization: Bearer ranges\r\n"); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 16)
	for _, offset := range []int64{1024, 3 << 30, 2048} {
		if av.io.Seek(d.format.Pb, offset, io.SeekStart) != offset {
			t.Fatal("range seek failed")
		}
		readHTTPBytes(t, d.format.Pb, buffer)
		for i, value := range buffer {
			if value != patternByte(offset+int64(i)) {
				t.Fatal("range returned wrong bytes")
			}
		}
	}
	if denied.Load() != 0 {
		t.Fatal("secondary cursor dropped authentication")
	}
}

func TestLiveSourceDisablesSeekingAndResume(t *testing.T) {
	p := NewPlayer(nil)
	defer p.Close()
	file := fixture(t, false)
	entry := library.Entry{Path: file, Playback: &library.Playback{Live: true, Seekable: true}}
	source := FromEntry(entry)
	source.Start = .6
	p.Open(source)
	waitState(t, p, func(s State) bool { return !s.Loading && s.FrameID != 0 })
	p.Pause()
	before := waitState(t, p, func(s State) bool { return !s.Playing })
	p.Seek(.8)
	// A following command is a worker barrier without a wall-clock sleep.
	p.SetVolume(.43)
	after := waitState(t, p, func(s State) bool { return s.Volume == .43 })
	if !after.NoSeek || after.Generation != before.Generation || after.Position != before.Position || before.Position >= .6 {
		t.Fatal("live input was resumed or sought", before, after)
	}
	p.Reset()
	waitState(t, p, func(s State) bool { return s.URL == "" })
	if p.Snapshot().NoSeek {
		t.Fatal("live state leaked after reset")
	}
}

// Decoder generations also change on seeks; the opening identity stays exact.
func TestPlaybackIdentitySurvivesSeeksAndChangesOnSameURLOpen(t *testing.T) {
	p := NewPlayer(nil)
	defer p.Close()
	source := Source{URL: fixture(t, false)}
	first := p.Open(source)
	before := waitState(t, p, func(s State) bool { return s.PlaybackID == first && !s.Loading && s.FrameID != 0 })
	p.Seek(.2)
	after := waitState(t, p, func(s State) bool { return s.Generation > before.Generation && !s.Loading })
	if after.PlaybackID != first {
		t.Fatal("seek replaced playback identity", before, after)
	}
	second := p.Open(source)
	if second == first || second == 0 {
		t.Fatal("same-URL open reused playback identity")
	}
	waitState(t, p, func(s State) bool { return s.PlaybackID == second && !s.Loading && s.FrameID != 0 })
	p.Reset()
	waitState(t, p, func(s State) bool { return s.PlaybackID == 0 && s.URL == "" })
}
