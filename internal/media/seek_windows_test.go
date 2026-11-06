package media

import (
	"bytes"
	"context"
	"math"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestD3D11PrerollKeepsExactPicturesAndReferenceFrames(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("FFmpeg fixture encoder unavailable")
	}
	for _, codec := range []string{"h264", "hevc10"} {
		t.Run(codec, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "long-gop.mp4")
			args := []string{"-v", "error", "-f", "lavfi", "-i", "testsrc2=s=320x160:r=30:d=4"}
			if codec == "h264" {
				args = append(args, "-c:v", "libx264", "-preset", "fast", "-pix_fmt", "yuv420p", "-x264-params", "keyint=90:min-keyint=90:scenecut=0:bframes=3:b-adapt=0:open-gop=1")
			} else {
				args = append(args, "-c:v", "libx265", "-pix_fmt", "yuv420p10le", "-x265-params", "log-level=error:pools=1:frame-threads=1:keyint=90:min-keyint=90:scenecut=0:bframes=4:b-adapt=0:open-gop=1")
			}
			if out, err := exec.Command("ffmpeg", append(args, file)...).CombinedOutput(); err != nil {
				t.Fatalf("fixture: %v: %s", err, out)
			}
			fast, err := Open(context.Background(), Source{URL: file}, false)
			if err != nil {
				t.Fatal(err)
			}
			defer fast.Close()
			frame, err := fast.Next()
			if err != nil {
				t.Fatal(err)
			}
			_, hardware := frame.D3D11Texture()
			frame.Close()
			if !hardware {
				t.Skip("D3D11 hardware decoder unavailable")
			}
			reference, err := Open(context.Background(), Source{URL: file}, false)
			if err != nil {
				t.Fatal(err)
			}
			defer reference.Close()
			// Keep the same hardware codec and threading, disabling only the
			// preroll optimization on this reference decoder.
			reference.hardwareName = "reference"
			receive := av.codec.Receive
			defer func() { av.codec.Receive = receive }()
			fastDecoded, referenceDecoded := 0, 0
			av.codec.Receive = func(ctx *avCodecContext, out *avFrame) int32 {
				ret := receive(ctx, out)
				if ret >= 0 {
					if ctx == fast.video {
						fastDecoded++
					} else if ctx == reference.video {
						referenceDecoded++
					}
				}
				return ret
			}
			for _, at := range []float64{.73, 1.11, 2.39, 3.11, .35, 1.99} {
				if err := fast.Seek(at); err != nil {
					t.Fatal(err)
				}
				if err := reference.Seek(at); err != nil {
					t.Fatal(err)
				}
				for range 5 {
					got, err := fast.Next()
					if err != nil {
						t.Fatal(err)
					}
					want, err := reference.Next()
					if err != nil {
						got.Close()
						t.Fatal(err)
					}
					actual, e1 := got.RGBA(80, 40)
					expected, e2 := want.RGBA(80, 40)
					pts, referencePTS := got.PTS(), want.PTS()
					got.Close()
					want.Close()
					if e1 != nil || e2 != nil {
						t.Fatal(e1, e2)
					}
					if math.Abs(pts-referencePTS) > .000001 || !bytes.Equal(actual.Pix, expected.Pix) {
						t.Fatalf("seek %.3f changed decoded picture: got %.6f, reference %.6f", at, pts, referencePTS)
					}
				}
			}
			if fastDecoded >= referenceDecoded {
				t.Fatalf("preroll decoded every picture: optimized=%d reference=%d", fastDecoded, referenceDecoded)
			}
			t.Logf("same exact pictures: optimized preroll/output=%d, reference=%d", fastDecoded, referenceDecoded)
		})
	}
}
