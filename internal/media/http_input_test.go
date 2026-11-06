package media

import (
	"context"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// A seekable multi-GiB resource without an equally large fixture allocation.
type httpPattern struct{ position, size int64 }

func patternByte(position int64) byte { return byte(position ^ position>>12 ^ position>>32) }
func (p *httpPattern) Read(buffer []byte) (int, error) {
	if p.position >= p.size {
		return 0, io.EOF
	}
	n := int(min(int64(len(buffer)), p.size-p.position))
	for i := range n {
		buffer[i] = patternByte(p.position + int64(i))
	}
	p.position += int64(n)
	return n, nil
}
func (p *httpPattern) Seek(offset int64, whence int) (int64, error) {
	switch whence {
	case io.SeekCurrent:
		offset += p.position
	case io.SeekEnd:
		offset += p.size
	}
	if offset < 0 {
		return 0, os.ErrInvalid
	}
	p.position = offset
	return offset, nil
}

func openTestHTTP(t *testing.T, ctx context.Context, address string) *Decoder {
	t.Helper()
	if err := loadMedia(); err != nil {
		t.Fatal(err)
	}
	d := &Decoder{ctx: ctx, cancellation: newCancellation(), format: av.format.Alloc()}
	d.stop = context.AfterFunc(ctx, d.cancellation.cancel)
	t.Cleanup(d.Close)
	d.format.InterruptCallback = avInterrupt{interruptCallback, d.cancellation.token}
	if err := d.openHTTP(address); err != nil {
		t.Fatal(err)
	}
	return d
}

func readHTTPBytes(t *testing.T, source *avIO, buffer []byte) {
	t.Helper()
	for n := 0; n < len(buffer); {
		ret := av.io.Partial(source, &buffer[n], int32(len(buffer)-n))
		if ret <= 0 {
			t.Fatal("HTTP read failed", ret)
		}
		n += int(ret)
	}
}

func TestHTTPCursorsReuseResponsesAcrossDistantTracks(t *testing.T) {
	const size = int64(8 << 30)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "video/mp4")
		http.ServeContent(w, r, "tracks.mp4", time.Time{}, &httpPattern{size: size})
	}))
	t.Cleanup(server.Close)
	d := openTestHTTP(t, context.Background(), server.URL)
	buffer := make([]byte, 500)
	readAt := func(offset int64) {
		t.Helper()
		if ret := av.io.Seek(d.format.Pb, offset, io.SeekStart); ret != offset {
			t.Fatal("64-bit HTTP seek failed", offset, ret)
		}
		readHTTPBytes(t, d.format.Pb, buffer)
		for i, value := range buffer {
			if value != patternByte(offset+int64(i)) {
				t.Fatal("HTTP cursor returned incorrect bytes", offset+int64(i))
			}
		}
	}
	// Audio-like small reads skip over intervening video data in each region.
	// Alternating tracks must reuse responses, including small backward reads
	// from the retained native buffers, rather than open one request per packet.
	for i := range 160 {
		for _, base := range []int64{3 << 30, 7 << 30} {
			offset := base + int64(i)*(128<<10)
			readAt(offset)
			readAt(offset + 100)
		}
	}
	if got := requests.Load(); got > 6 {
		t.Fatalf("distant tracks opened %d HTTP requests", got)
	}
	t.Logf("640 alternating/backward reads used %d HTTP requests", requests.Load())
	input := d.cancellation.input
	if got := input.seek(0, avSeekSize); got != size {
		t.Fatal("resource size was truncated", got)
	}
	position := input.position
	if input.seek(math.MaxInt64, io.SeekCurrent) >= 0 || input.position != position || input.seek(-1, io.SeekStart) >= 0 {
		t.Fatal("invalid seek changed the input position")
	}
	if got := input.seek(-3, io.SeekEnd|avSeekForce); got != size-3 {
		t.Fatal("relative end seek failed", got)
	}
	if got := input.read(&buffer[0], int32(len(buffer))); got != 3 {
		t.Fatal("final partial read failed", got)
	}
	if got := input.read(&buffer[0], int32(len(buffer))); got != avEOF {
		t.Fatal("read beyond EOF succeeded", got)
	}
	d.Close()
	if input.buffered != nil || input.cursors[0] != nil || input.cursors[1] != nil {
		t.Fatal("decoder close retained HTTP contexts")
	}
}

func TestHTTPCursorCancellationInterruptsPendingRead(t *testing.T) {
	const blocked = int64(4 << 20)
	entered := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.Header.Get("Range"), "bytes="+strconv.FormatInt(blocked, 10)+"-") {
			entered <- struct{}{}
			<-r.Context().Done()
			return
		}
		w.Header().Set("Content-Type", "video/mp4")
		http.ServeContent(w, r, "blocked.mp4", time.Time{}, &httpPattern{size: 8 << 20})
	}))
	t.Cleanup(server.Close)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d := openTestHTTP(t, ctx, server.URL)
	input := d.cancellation.input
	input.seek(blocked, io.SeekStart)
	done := make(chan int32, 1)
	go func() {
		var buffer [32]byte
		done <- input.read(&buffer[0], int32(len(buffer)))
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("read did not reach the remote range")
	}
	cancel()
	select {
	case ret := <-done:
		if ret >= 0 {
			t.Fatal("cancelled read succeeded", ret)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancellation did not interrupt HTTP")
	}
}

func TestHTTPPlaybackPreservesDecodeSeekAndStreamingInput(t *testing.T) {
	t.Setenv("SDL_AUDIO_DRIVER", "dummy")
	file := fixture(t, true)
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	for _, ranges := range []bool{true, false} {
		t.Run(strconv.FormatBool(ranges), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if ranges {
					http.ServeFile(w, r, file)
				} else {
					w.Header().Set("Content-Length", strconv.Itoa(len(data)))
					w.Write(data)
				}
			}))
			defer server.Close()
			d, err := Open(context.Background(), Source{URL: server.URL + "/clip.mp4"}, true)
			if err != nil {
				t.Fatal(err)
			}
			defer d.Close()
			if (d.cancellation.input.buffered != nil) != ranges {
				t.Fatal("incorrect handling of HTTP seekability")
			}
			for i := range 30 {
				frame, err := d.Next()
				if err != nil {
					t.Fatal(err)
				}
				pts := frame.PTS()
				frame.Close()
				if math.Abs(pts-float64(i)/30) > .0001 {
					t.Fatal("HTTP read changed presentation order", pts)
				}
			}
			if frame, err := d.Next(); err != io.EOF {
				frame.Close()
				t.Fatal("HTTP input lost EOF", err)
			}
			if ranges {
				if err := d.Seek(.45); err != nil {
					t.Fatal(err)
				}
				frame, err := d.Next()
				if err != nil {
					t.Fatal(err)
				}
				defer frame.Close()
				if frame.PTS() < .45 || frame.PTS() > .49 {
					t.Fatal("HTTP input lost precise seeking", frame.PTS())
				}
			}
		})
	}
}
