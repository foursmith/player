package main

import (
	"context"
	"errors"
	"fmt"
	"github.com/egoist/mygo"
	"github.com/foursmith/player/internal/exporter"
	"github.com/foursmith/player/internal/media"
	"github.com/foursmith/player/internal/projection"
	"github.com/foursmith/player/internal/tracking"
	"github.com/foursmith/player/internal/video"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type exportTask struct {
	ctx     context.Context
	cancel  context.CancelFunc
	preview media.FrameSlot
}

func (a *application) cancelExport() {
	if task := a.exportTask; task != nil {
		task.cancel()
		task.preview.Close()
		a.exportTask = nil
	}
	a.exporting = false
}

func (a *application) exportLoop(format string) {
	if a.exporting || a.loopA == nil || a.loopB == nil || a.selected < 0 || a.selected >= len(a.entries) {
		return
	}
	start, end := *a.loopA, *a.loopB
	entry := a.entries[a.selected]
	params := a.params(false)
	ratio := video.PixelRatio(a.preferences.QualityID, mygo.Screen.DisplayMatching(a.window.Bounds()).ScaleFactor)
	width, height := int(float64(a.width)*ratio), int(float64(a.height)*ratio)
	cssWidth := float64(a.width)
	cues := a.cues
	if !a.preferences.SubtitlesEnabled {
		cues = nil
	}
	autoCenter := a.preferences.FaceAutoCenter && !a.manuallyPaused
	wasPlaying := a.player.Snapshot().Playing
	a.exporting = true
	a.exportFormat = format
	a.exportPosition = a.player.Snapshot().Position
	a.exportMessage = ""
	ctx, cancel := context.WithCancel(a.ctx)
	task := &exportTask{ctx: ctx, cancel: cancel}
	a.exportTask = task
	window := a.window
	width -= width % 2
	height -= height % 2
	fps := []int{24, 30, 60}[a.preferences.FrameRateID-1]
	bitrate := []int{2000000, 4000000, 8000000, 12000000}[a.preferences.QualityID]
	extension := format
	if format == "motion-photo" {
		extension = "jpg"
	}
	stem := strings.TrimSuffix(entry.Name, filepath.Ext(entry.Name))
	name := fmt.Sprintf("%s-AB-%s-%s.%s", stem, strings.ReplaceAll(fmt.Sprintf("%.1f", start), ".", "-"), strings.ReplaceAll(fmt.Sprintf("%.1f", end), ".", "-"), extension)
	run := func(path string) {
		defer cancel()
		defer task.preview.Close()
		models := &faceModels{}
		defer models.close()
		tracker := &tracking.Tracker{}
		view := params.View
		var trackingSurface video.Surface
		defer func() {
			if trackingSurface != nil {
				trackingSurface.Close()
			}
		}()
		aspect := projection.Viewports(float64(width), float64(height), params.SplitCount, params.Portrait)[0]
		viewAspect := aspect.Width / aspect.Height
		project := func(frame *media.Frame, p video.Parameters, at float64) (video.Parameters, error) {
			if autoCenter {
				now := (at - start) * 1000
				tracker.Advance(&view, p.Mode, viewAspect, now, 1/float64(fps))
				if request, ready := tracker.Begin(now, float64(fps), p.Mode, view, viewAspect); ready {
					var err error
					if trackingSurface == nil {
						trackingSurface, err = video.New(0)
						if err != nil {
							return p, err
						}
					}
					sample := p
					sample.SplitCount = 1
					sample.View = request.View
					sample.FOV = request.FOV
					began := time.Now()
					img, err := trackingSurface.Capture(frame, sample, 320, max(120, int(320/request.Aspect+.5)))
					if err != nil {
						return p, err
					}
					faces, err := models.detect(img, request.FullRange)
					if err != nil {
						return p, err
					}
					latency := time.Since(began).Seconds() * 1000
					tracker.Apply(request, faces, now+latency, latency, float64(fps), p.Mode, view, viewAspect)
				}
			}
			p.View = view
			return p, nil
		}
		lastProgress := -1
		err := exporter.Export(ctx, exporter.Options{Source: entry.Path, Cookies: entry.Cookies, Encoder: encoderPath(), CSSWidth: cssWidth, Cues: cues, Project: project, Frame: func(frame *media.Frame, p video.Parameters, at float64) { task.preview.Store(frame.Clone()) }, Destination: path, Format: format, Start: start, End: end, Width: width, Height: height, FPS: fps, BitRate: bitrate, Parameters: params, Progress: func(progress int) {
			if progress == lastProgress {
				return
			}
			lastProgress = progress
			capturedView := view
			window.Update(func() {
				if ctx.Err() != nil || a.exportTask != task {
					return
				}
				a.exportProgress = progress
				a.exportPosition = start + float64(progress)/100*(end-start)
				a.view = capturedView
			})
		}})
		window.Update(func() {
			if a.shutdown || a.exportTask != task {
				return
			}
			a.cancelExport()
			if wasPlaying && a.selected >= 0 && a.selected < len(a.entries) && a.entries[a.selected].Path == entry.Path {
				a.player.Play()
			}
			a.tracker.Reset()
			if err != nil && !errors.Is(err, context.Canceled) {
				a.notice = err.Error()
			} else if err == nil {
				a.exportMessage = a.t("export.saved", filepath.Base(path))
			}
		})
	}
	go func() {
		path, err := mygo.Dialog.Save(mygo.SaveDialogOptions{Parent: window, DefaultPath: name})
		window.Update(func() {
			if task.ctx.Err() != nil || a.exportTask != task {
				return
			}
			if err != nil || path == "" {
				a.cancelExport()
				if err != nil {
					a.notice = err.Error()
				}
				return
			}
			a.exportProgress = 0
			a.player.Pause()
			// Update is asynchronous: start only after this main-thread check.
			go run(path)
		})
	}()
}

func encoderPath() string {
	resource, _ := mygo.App.Path(mygo.PathResources)
	name := "ffmpeg"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	candidate := filepath.Join(resource, "bin", name)
	if _, err := os.Stat(candidate); err == nil {
		return candidate
	}
	return "ffmpeg"
}
