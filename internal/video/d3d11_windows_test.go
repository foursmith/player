package video

import (
	"context"
	"fmt"
	"image"
	"io"
	"math"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"unsafe"

	"github.com/foursmith/player/internal/media"
	"github.com/foursmith/player/internal/projection"
	"golang.org/x/sys/windows"
)

func gpuVideoFrame(t *testing.T, ten bool, options ...string) (*media.Frame, *media.Decoder) {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("FFmpeg fixture encoder unavailable")
	}
	file := filepath.Join(t.TempDir(), "gradient.mp4")
	args := []string{"-v", "error", "-f", "lavfi", "-i", "nullsrc=s=320x160:r=5:d=1,geq=lum='32+160*X/W':cb='64+128*Y/H':cr=160"}
	if ten {
		args = append(args, "-c:v", "libx265", "-x265-params", "log-level=error:pools=1", "-pix_fmt", "yuv420p10le")
	} else {
		args = append(args, "-c:v", "libx264", "-profile:v", "high", "-pix_fmt", "yuv420p", "-crf", "12")
	}
	args = append(args, "-colorspace", "bt709")
	args = append(args, options...)
	args = append(args, file)
	if out, err := exec.Command("ffmpeg", args...).CombinedOutput(); err != nil {
		t.Fatalf("fixture: %v: %s", err, out)
	}
	d, err := media.Open(context.Background(), media.Source{URL: file}, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(d.Close)
	frame, err := d.Next()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(frame.Close)
	if _, ok := frame.D3D11Texture(); !ok {
		t.Skip("fixture has no D3D11 decoder texture")
	}
	return frame, d
}

func compareProjection(t *testing.T, got, want *image.RGBA) {
	t.Helper()
	worst, bad := 0, 0
	for y := 2; y < got.Bounds().Dy()-2; y += 3 {
		for x := 2; x < got.Bounds().Dx()-2; x += 3 {
			g, w := got.RGBAAt(x, y), want.RGBAAt(x, y)
			for _, channel := range [][2]uint8{{g.R, w.R}, {g.G, w.G}, {g.B, w.B}} {
				d := int(math.Abs(float64(channel[0]) - float64(channel[1])))
				worst = max(worst, d)
				if d > 5 {
					bad++
				}
			}
		}
	}
	if bad > 3 {
		t.Fatalf("GPU projection/color mismatch: worst=%d, channels >5=%d", worst, bad)
	}
}

func TestD3D11HardwareProjectionMatchesReference(t *testing.T) {
	for _, ten := range []bool{false, true} {
		t.Run(fmt.Sprint("10bit=", ten), func(t *testing.T) {
			frame, _ := gpuVideoFrame(t, ten)
			r := &direct3d{}
			defer r.Close()
			for mode := projection.SBS180; mode <= projection.Flat; mode++ {
				t.Run(projection.IDs[mode], func(t *testing.T) {
					p := Parameters{Mode: mode, View: projection.View{Yaw: 23, Pitch: -11, Forward: 14, Zoom: 1.2}, SplitCount: 2, Portrait: true}
					got, err := r.Capture(frame, p, 96, 64)
					if err != nil {
						t.Fatal(err)
					}
					want, err := (&rgbaSurface{}).Capture(frame, p, 96, 64)
					if err != nil {
						t.Fatal(err)
					}
					compareProjection(t, got, want)
				})
			}
			if r.inputDesc.Format == 28 {
				t.Fatal("hardware projection allocated a CPU RGBA converter")
			}
		})
	}
}

func TestD3D11CompositionResizeAndReplay(t *testing.T) {
	hwnd, content := d3dTestWindow(t, true)
	r := &direct3d{window: hwnd}
	defer r.Close()
	frame, _ := gpuVideoFrame(t, false)
	p := Parameters{Mode: projection.SBS180, View: projection.DefaultView(), SplitCount: 2}
	for _, size := range [][2]float64{{96, 64}, {160, 100}, {96, 64}} {
		if err := r.Present(frame, p, size[0], size[1], 1.5); err != nil {
			t.Fatal(err)
		}
		if r.failedOwner != 0 {
			t.Fatal("native presentation fell back")
		}
	}
	// Model the actual MyGo hierarchy, not just a bare HWND. The video's
	// bottom tree belongs to the UI child; the parent must stay free, and the
	// UI must still be able to compose its top tree on that same child.
	var parentTarget, uiTarget, duplicate uintptr
	defer d3dFree(&parentTarget, &uiTarget, &duplicate)
	if err := d3dCheck("parent remains free", d3dCall(r.composition, 6, hwnd, 0, uintptr(unsafe.Pointer(&parentTarget)))); err != nil {
		t.Fatal("video was attached to the clipped parent tree:", err)
	}
	if err := d3dCheck("UI overlay tree", d3dCall(r.composition, 6, content, 1, uintptr(unsafe.Pointer(&uiTarget)))); err != nil {
		t.Fatal(err)
	}
	if err := d3dCheck("occupied content video tree", d3dCall(r.composition, 6, content, 0, uintptr(unsafe.Pointer(&duplicate)))); err == nil {
		t.Fatal("video did not occupy the content child's bottom composition tree")
	}
	d3dFree(&parentTarget, &uiTarget)
	r.Clear()
	if r.device != 0 || r.input != 0 || r.swap != 0 {
		t.Fatal("clear retained GPU video resources")
	}
	if err := r.Present(frame, p, 96, 64, 1); err != nil {
		t.Fatal(err)
	}
	if r.failedOwner != 0 {
		t.Fatal("replay did not restore native presentation")
	}
}

// A hidden parent and full-size MyGoSurface child reproduce the native window
// topology without starting an app or development server.
func d3dTestWindow(t *testing.T, withContent bool) (parent, content uintptr) {
	t.Helper()
	// DestroyWindow must run on the creating thread, including test cleanup.
	runtime.LockOSThread()
	t.Cleanup(runtime.UnlockOSThread)
	user := windows.NewLazySystemDLL("user32.dll")
	create := user.NewProc("CreateWindowExW")
	class, _ := syscall.UTF16PtrFromString("STATIC")
	parent, _, err := create.Call(0x00200000, uintptr(unsafe.Pointer(class)), 0, 0x80000000, 0, 0, 160, 100, 0, 0, 0, 0)
	if parent == 0 {
		t.Fatal(err)
	}
	// Registered before the destroy cleanup so the class outlives its child.
	defer func() {
		t.Cleanup(func() { user.NewProc("DestroyWindow").Call(parent) })
	}()
	if !withContent {
		return parent, 0
	}
	class, _ = syscall.UTF16PtrFromString("MyGoSurface")
	instance, _, _ := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetModuleHandleW").Call(0)
	wc := struct {
		Size, Style                   uint32
		Procedure                     uintptr
		ClassExtra, WindowExtra       int32
		Instance, Icon, Cursor, Brush uintptr
		Menu, Name                    *uint16
		SmallIcon                     uintptr
	}{Procedure: user.NewProc("DefWindowProcW").Addr(), Instance: instance, Name: class}
	wc.Size = uint32(unsafe.Sizeof(wc))
	atom, _, err := user.NewProc("RegisterClassExW").Call(uintptr(unsafe.Pointer(&wc)))
	if atom == 0 {
		t.Fatal(err)
	}
	t.Cleanup(func() { user.NewProc("UnregisterClassW").Call(uintptr(unsafe.Pointer(class)), instance) })
	content, _, err = create.Call(0, uintptr(unsafe.Pointer(class)), 0, 0x50000000, 0, 0, 160, 100, parent, 0, instance, 0)
	if content == 0 {
		t.Fatal(err)
	}
	return parent, content
}

func TestD3D11MissingContentUsesPortableFallback(t *testing.T) {
	hwnd, _ := d3dTestWindow(t, false)
	frame, _ := gpuVideoFrame(t, false)
	r := &direct3d{window: hwnd}
	defer r.Close()
	if err := r.Present(frame, Parameters{Mode: projection.Flat, SplitCount: 1}, 96, 64, 1); err != nil {
		t.Fatal(err)
	}
	if r.failedOwner == 0 || r.fallback.bitmap == nil || r.swap != 0 {
		t.Fatal("missing content HWND left an invisible native layer instead of a drawable fallback")
	}
	// A new decoder may present natively after an earlier device fell back.
	// Its success must stop painting the old portable bitmap over the video.
	r.window, _ = d3dTestWindow(t, true)
	newFrame, _ := gpuVideoFrame(t, false)
	if err := r.Present(newFrame, Parameters{Mode: projection.Flat, SplitCount: 1}, 96, 64, 1); err != nil {
		t.Fatal(err)
	}
	if r.failedOwner != 0 || r.swap == 0 {
		t.Fatal("a new decoder did not recover native presentation")
	}
}

func TestD3D11CaptureWhileDecoderAdvances(t *testing.T) {
	frame, decoder := gpuVideoFrame(t, true)
	r := &direct3d{}
	defer r.Close()
	done := make(chan error, 1)
	go func() {
		for range 12 {
			_, err := r.Capture(frame, Parameters{Mode: projection.SBS180, View: projection.DefaultView(), SplitCount: 1}, 80, 40)
			if err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	var decodeError error
	for {
		next, err := decoder.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			decodeError = err
			break
		}
		next.Close()
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if decodeError != nil {
		t.Fatal(decodeError)
	}
}

func TestD3D11SoftwareDecodedVideoRemainsPlayable(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("FFmpeg fixture encoder unavailable")
	}
	file := filepath.Join(t.TempDir(), "rgb.nut")
	if out, err := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "color=c=red:s=80x40:r=5:d=1", "-c:v", "rawvideo", "-pix_fmt", "bgra", file).CombinedOutput(); err != nil {
		t.Fatalf("fixture: %v: %s", err, out)
	}
	d, err := media.Open(context.Background(), media.Source{URL: file}, false)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	frame, err := d.Next()
	if err != nil {
		t.Fatal(err)
	}
	defer frame.Close()
	if _, hardware := frame.D3D11Texture(); hardware {
		t.Fatal("raw RGB fixture unexpectedly uses hardware decode")
	}
	r := &direct3d{}
	defer r.Close()
	got, err := r.Capture(frame, Parameters{Mode: projection.Flat, SplitCount: 1}, 80, 40)
	if err != nil {
		t.Fatal(err)
	}
	want, err := frame.RGBA(80, 40)
	if err != nil {
		t.Fatal(err)
	}
	compareProjection(t, got, want)
}

func TestD3D11VideoColorRangeAndMatrices(t *testing.T) {
	for _, tc := range []struct {
		name    string
		ten     bool
		options []string
	}{
		{"full BT709 8bit", false, []string{"-color_range", "pc"}},
		{"limited BT601 8bit", false, []string{"-colorspace", "smpte170m"}},
		{"full BT709 10bit", true, []string{"-color_range", "pc"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			frame, _ := gpuVideoFrame(t, tc.ten, tc.options...)
			r := &direct3d{}
			defer r.Close()
			p := Parameters{Mode: projection.Flat, SplitCount: 1}
			got, err := r.Capture(frame, p, 80, 40)
			if err != nil {
				t.Fatal(err)
			}
			want, err := (&rgbaSurface{}).Capture(frame, p, 80, 40)
			if err != nil {
				t.Fatal(err)
			}
			compareProjection(t, got, want)
		})
	}
}
