package media

import (
	"context"
	"math"
	"os/exec"
	"path/filepath"
	"testing"
	"unsafe"

	"github.com/foursmith/player/internal/native"
)

func TestRejectedHardwareCodecFallsBackToSoftware(t *testing.T) {
	file := fixture(t, false)
	if err := loadMedia(); err != nil {
		t.Fatal(err)
	}
	// Give the decoder a real reference-counted buffer, while deterministically
	// rejecting the hardware codec before FFmpeg tries to use a driver. The
	// retry must create and decode with a genuine software FFmpeg context.
	handle, err := native.Open(native.HostTarget().Library("avutil", 61))
	if err != nil {
		t.Fatal(err)
	}
	var buffer struct{ Alloc func(uintptr) *avBuffer }
	if err := native.Bind(handle, &buffer, "av_buffer_alloc"); err != nil {
		t.Fatal(err)
	}
	hardware, device, open := av.codec.Hardware, av.util.DeviceCreate, av.codec.Open
	t.Cleanup(func() { av.codec.Hardware, av.util.DeviceCreate, av.codec.Open = hardware, device, open })
	config := avHWConfig{DeviceType: av.util.DeviceType("d3d11va"), Methods: 1, PixFmt: 42}
	// Match the device selected on each host without requiring a physical GPU.
	deviceType := av.util.DeviceType
	av.util.DeviceType = func(string) int32 { return config.DeviceType }
	t.Cleanup(func() { av.util.DeviceType = deviceType })
	av.codec.Hardware = func(_ *avCodec, index int32) *avHWConfig {
		if index == 0 {
			return &config
		}
		return nil
	}
	av.util.DeviceCreate = func(out **avBuffer, _ int32, _ *byte, _ unsafe.Pointer, _ int32) int32 {
		*out = buffer.Alloc(64)
		if *out == nil {
			return avNoMemory
		}
		return 0
	}
	attempts := 0
	av.codec.Open = func(ctx *avCodecContext, codec *avCodec, options unsafe.Pointer) int32 {
		attempts++
		if ctx.HwDeviceCtx != nil {
			return avExternal
		}
		return open(ctx, codec, options)
	}
	d, err := Open(context.Background(), Source{URL: file}, false)
	if err != nil {
		t.Fatal("hardware rejection prevented playback", err)
	}
	defer d.Close()
	if attempts != 2 || d.hardware != nil || d.Metadata().Decoder != "h264" {
		t.Fatal("software retry retained hardware state", attempts, d.Metadata())
	}
	if err := d.Seek(.45); err != nil {
		t.Fatal(err)
	}
	frame, err := d.Next()
	if err != nil {
		t.Fatal(err)
	}
	defer frame.Close()
	img, err := frame.RGBA(80, 40)
	if err != nil || frame.PTS() < .45 || frame.PTS() > .49 || img.RGBAAt(40, 20).R < 200 {
		t.Fatal("software fallback could not seek and convert video", err)
	}
}

func TestPlaybackHEVC10BitUnicodePathAndSeek(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("FFmpeg fixture encoder unavailable")
	}
	file := filepath.Join(t.TempDir(), "片段 10-bit #1.mp4")
	if output, err := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "color=c=red:s=320x160:r=12:d=1",
		"-c:v", "libx265", "-x265-params", "log-level=error:pools=1", "-pix_fmt", "yuv420p10le",
		"-colorspace", "bt709", "-color_primaries", "bt709", "-color_trc", "bt709", file).CombinedOutput(); err != nil {
		t.Fatalf("HEVC fixture: %v: %s", err, output)
	}
	d, err := Open(context.Background(), Source{URL: file}, false)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if metadata := d.Metadata(); metadata.Width != 320 || metadata.Height != 160 || math.Abs(metadata.FrameRate-12) > .001 {
		t.Fatal("invalid HEVC metadata", metadata)
	}
	var converter RGBAConverter
	defer converter.Close()
	for _, at := range []float64{0, .5, .25} {
		if err := d.Seek(at); err != nil {
			t.Fatal(err)
		}
		// Reuse the readback and conversion buffers across frames and seeks.
		for range 2 {
			frame, err := d.Next()
			if err != nil {
				t.Fatal(err)
			}
			img, err := converter.Convert(frame, 160, 80)
			pts := frame.PTS()
			frame.Close()
			if err != nil || pts < at || pts > at+2.0/12 {
				t.Fatal("10-bit conversion or accurate seek failed", pts, err)
			}
			pixel := img.RGBAAt(80, 40)
			if pixel.R < 200 || pixel.G > 40 || pixel.B > 40 {
				t.Fatal("10-bit video produced incorrect pixels", pixel)
			}
		}
	}
	t.Log("HEVC Main10 decoder:", d.Metadata().Decoder)
}
