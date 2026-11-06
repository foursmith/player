package main

import (
	"encoding/json"
	"fmt"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"
	"github.com/foursmith/player/internal/media"
	"github.com/foursmith/player/internal/projection"
	"github.com/foursmith/player/internal/tracking"
	"math"
	"strings"
)

func (a *application) captureTrackingLog(request tracking.Request) {
	if !a.recordingTracking {
		return
	}
	entry := struct {
		At      float64
		Request tracking.Request
		View    projection.View
		Tracker tracking.Tracker
	}{trackingNow(), request, a.view, a.tracker}
	b, err := json.Marshal(entry)
	if err != nil {
		return
	}
	if len(a.trackingLog) >= 6000 {
		copy(a.trackingLog, a.trackingLog[1:])
		a.trackingLog = a.trackingLog[:5999]
	}
	a.trackingLog = append(a.trackingLog, string(b))
}
func (a *application) toggleTrackingLog() {
	if a.recordingTracking {
		a.recordingTracking = false
		if err := mygo.Clipboard.Write(transfer.TextData(strings.Join(a.trackingLog, "\n"))); err != nil {
			a.notice = err.Error()
		}
		return
	}
	a.recordingTracking = true
	a.trackingLog = []string{fmt.Sprintf(`{"app":%q,"runtime":"native","version":%q}`, appInfo.Name, appInfo.Version)}
}
func (a *application) debug(c *ui.Context, state media.State) {
	width := min(float32(224), a.width*.38, a.height*.315)
	ui.Column(c.Key("tracking-monitor")).Absolute().Right(12).Top(12).Width(width).Gap(8).Children(func() {
		if a.trackingImage != nil && a.trackingSampleWidth > 0 {
			height := width * float32(a.trackingSampleHeight) / float32(a.trackingSampleWidth)
			ui.Box(c).Size(width, height).Radius(12).Clip().Border(1, c.Theme().Accent.Alpha(.18)).Children(func() {
				ui.Image(c, a.trackingImage).Fill()
				ui.Box(c).Absolute().Fill().IgnoreInput(true).Draw(func(p *ui.Painter, r ui.Rect) {
					for _, face := range a.tracker.State.Faces {
						color := white(.55)
						if a.tracker.State.Selected != nil && math.Abs(face.X-a.tracker.State.Selected.X) < .001 {
							color = darkPlayerTheme.Accent
						}
						box := ui.Rect{X: r.X + float32(face.X)*r.W, Y: r.Y + float32(face.Y)*r.H, W: float32(face.Width) * r.W, H: float32(face.Height) * r.H}
						p.Stroke(box, color, 1, 1.5)
						p.RichText(box.X, max(r.Y, box.Y-13), 0, ui.Span{Text: fmt.Sprintf("%.0f%%", face.Score*100), Size: 9, Color: color})
					}
				})
			})
		}
		panel(c, 12).FillWidth().Children(func() {
			ui.Row(c).FillWidth().Padding(8, 12).Gap(6).Children(func() {
				ui.Box(c).Size(6, 6).Radius(3).Background(c.Theme().Accent)
				smallText(c, a.t("debug.trackingMonitor"), .68).FontSize(9).Grow(1).NoWrap()
				label := a.t("debug.record")
				if a.recordingTracking {
					label = a.t("debug.copy")
				}
				if ui.Button(c, label).Height(22).FontSize(8).Background(ink(c, .05)).Border(0, ui.Transparent).Description(a.t("debug." + map[bool]string{true: "copyAndStop", false: "recordLog"}[a.recordingTracking])).Clicked() {
					a.toggleTrackingLog()
				}
			})
			ui.Divider(c).Background(ink(c, .07))
			detail := fmt.Sprintf("%dx%d · %.2f fps\n%s · FFmpeg %s\n%.3fs · %.2fx\n%s · P95 %.1fms\nyaw %.1f° · pitch %.1f°\nforward %.2f · faces %d", state.Metadata.Width, state.Metadata.Height, state.Metadata.FrameRate, state.Metadata.Decoder, media.Version(), state.Position, state.Rate, a.tracker.Activity, a.tracker.P95(), a.view.Yaw, a.view.Pitch, a.view.Forward, len(a.tracker.State.Faces))
			smallText(c, detail, .54).Font("monospace").FontSize(9).Padding(10, 12)
			if target := a.tracker.State.Target; target != nil {
				plan := tracking.CenteringPlan(*target, a.view, a.mode, a.trackingAspect(), a.tracker.State.Moving)
				ui.Divider(c).Background(ink(c, .07))
				smallText(c, fmt.Sprintf("↔ %+.1f°   ↕ %+.1f°   ◎ %+.1f", plan.Offset.Yaw, plan.Offset.Pitch, plan.Offset.Forward), .88).TextColor(c.Theme().Accent).Padding(8, 12)
				if plan.Blocked {
					smallText(c, a.t("debug.projectionBoundary"), .86).TextColor(c.Theme().Warning).Padding(8, 12)
				}
			}
		})
	})
}
