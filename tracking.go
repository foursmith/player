package main

import (
	"image"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/foursmith/player/internal/detection"
	"github.com/foursmith/player/internal/media"
	"github.com/foursmith/player/internal/projection"
	"github.com/foursmith/player/internal/tracking"
	"github.com/foursmith/player/internal/video"
)

type inferenceJob struct {
	request       tracking.Request
	frame         *media.Frame
	params        video.Parameters
	generation    uint64
	width, height int
	fps           float64
}
type inferenceWorker struct {
	jobs   chan inferenceJob
	done   chan struct{}
	closed chan struct{}
}

func newInferenceWorker(a *application) *inferenceWorker {
	w := &inferenceWorker{jobs: make(chan inferenceJob, 1), done: make(chan struct{}), closed: make(chan struct{})}
	go w.run(a)
	return w
}
func (w *inferenceWorker) close() { close(w.done); <-w.closed }
func runtimeLibrary() string {
	if path := os.Getenv("FSVR_TFLITE_LIBRARY"); path != "" {
		return path
	}
	name := "libtensorflowlite_c.2.17.1.dylib"
	if runtime.GOOS == "windows" {
		name = "tensorflowlite_c.dll"
	} else if runtime.GOOS == "linux" {
		name = "libtensorflowlite_c.so"
	}
	resourceDir, _ := mygo.App.Path(mygo.PathResources)
	executable, _ := os.Executable()
	for _, dir := range []string{filepath.Join(resourceDir, "lib"), filepath.Join(filepath.Dir(executable), "lib"), ".cache/tflite"} {
		path := filepath.Join(dir, name)
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return name
}
func (w *inferenceWorker) run(a *application) {
	defer close(w.closed)
	var surface video.Surface
	models := &faceModels{}
	defer func() {
		if surface != nil {
			surface.Close()
		}
		models.close()
		for {
			select {
			case job := <-w.jobs:
				job.frame.Close()
			default:
				return
			}
		}
	}()
	for {
		select {
		case <-w.done:
			return
		case job := <-w.jobs:
			if state := a.player.Snapshot(); state.Generation != job.generation || state.Loading {
				job.frame.Close()
				continue
			}
			started := time.Now()
			var err error
			var faces []detection.Face
			var img *image.RGBA
			if surface == nil {
				surface, err = video.New(0)
			}
			if err == nil {
				params := job.params
				params.SplitCount = 1
				params.View = job.request.View
				params.FOV = job.request.FOV
				img, err = surface.Capture(job.frame, params, job.width, job.height)
			}
			if err == nil {
				faces, err = models.detect(img, job.request.FullRange)
			}
			job.frame.Close()
			latency := time.Since(started).Seconds() * 1000
			a.window.Update(func() {
				state := a.player.Snapshot()
				if a.shutdown || state.Generation != job.generation || job.request.Epoch != a.tracker.Epoch {
					return
				}
				a.tracker.Busy = false
				if err != nil {
					a.notice = err.Error()
					a.tracker.NextAt = trackingNow() + 3000
					return
				}
				if !a.canTrack(state) {
					return
				}
				a.tracker.Apply(job.request, faces, trackingNow(), latency, job.fps, a.mode, a.view, a.trackingAspect())
				a.captureTrackingLog(job.request)
				if a.debugOpen {
					a.trackingSampleWidth, a.trackingSampleHeight = job.width, job.height
					if a.trackingImage == nil {
						a.trackingImage = ui.NewBitmap(img)
					} else {
						a.trackingImage.Update(img)
					}
				}
			})
		}
	}
}
func trackingNow() float64 { return float64(time.Since(oceanStarted).Microseconds()) / 1000 }
func (a *application) trackingAspect() float64 {
	if a.pipWindow != nil {
		b := a.pipWindow.ContentBounds()
		return float64(b.Width) / float64(max(1, b.Height))
	}
	viewports := projection.Viewports(float64(a.width), float64(a.height), a.preferences.SplitCount, a.preferences.PreferredSplitAspect == "9:16")
	return viewports[0].Width / viewports[0].Height
}

func (a *application) canTrack(state media.State) bool {
	return a.mode.IsVR() && !a.exporting && a.inference != nil && state.Playing && !state.Loading && a.preferences.FaceAutoCenter && !a.manuallyPaused && !a.dragging && !a.scrubbing && !time.Now().Before(a.manualUntil)
}

func (a *application) updateTracking(c *ui.Context, state media.State) {
	now := trackingNow()
	delta := (now - a.lastTrackingAt) / 1000
	a.lastTrackingAt = now
	if !a.canTrack(state) {
		return
	}
	aspect := a.trackingAspect()
	movement := a.tracker.Advance(&a.view, a.mode, aspect, now, delta)
	if a.tracker.State.Moving || movement.Stopped {
		c.After(time.Second / 60)
	}
	fps := float64([]int{24, 30, 60}[a.preferences.FrameRateID-1])
	request, ready := a.tracker.Begin(now, fps, a.mode, a.view, aspect)
	if !ready {
		if !a.tracker.Busy && !a.tracker.State.Moving {
			remaining := a.tracker.NextAt - now
			if remaining > 0 {
				c.After(time.Duration(remaining * float64(time.Millisecond)))
			}
		}
		return
	}
	frame := a.player.Frame()
	if frame == nil {
		a.tracker.Busy = false
		return
	}
	height := max(120, int(320/request.Aspect+.5))
	job := inferenceJob{request: request, frame: frame, params: a.params(false), generation: state.Generation, width: 320, height: height, fps: fps}
	select {
	case a.inference.jobs <- job:
	default:
		frame.Close()
		a.tracker.Busy = false
	}
}
