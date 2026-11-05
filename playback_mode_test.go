package main

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/foursmith/player/internal/library"
	"github.com/foursmith/player/internal/media"
	"github.com/foursmith/player/internal/projection"
	"github.com/foursmith/player/internal/video"
)

func modeTestApplication(t *testing.T) *application {
	t.Helper()
	a := &application{
		ctx:         context.Background(),
		preferences: defaultPreferences(),
		stored:      storedState{History: map[string]videoHistory{}},
		entries:     []library.Entry{{Name: "ordinary.mp4", Path: "current.mp4"}},
		selected:    0,
		mode:        projection.Flat,
		detectMode:  true,
		player:      media.NewPlayer(nil),
		stateWriter: newStateWriter(filepath.Join(t.TempDir(), "state.json"), nil),
	}
	t.Cleanup(a.player.Close)
	t.Cleanup(func() { a.stateWriter.Close() })
	return a
}

func TestVideoModeDetectionWaitsForCurrentFileAndHonorsManualSelection(t *testing.T) {
	a := modeTestApplication(t)
	state := media.State{URL: "previous.mp4", Metadata: media.Metadata{Width: 320, Height: 160, Spherical: "equirectangular"}}
	a.resolveVideoMode(state)
	if a.mode != projection.Flat || !a.detectMode {
		t.Fatal("stale metadata changed the selected file")
	}
	state.URL, state.Loading = "current.mp4", true
	a.resolveVideoMode(state)
	if a.mode != projection.Flat || !a.detectMode {
		t.Fatal("detection completed while metadata was loading")
	}
	state.Loading = false
	a.resolveVideoMode(state)
	if a.mode != projection.Mono360 || a.detectMode || a.modeManual {
		t.Fatal("current file's spatial metadata was ignored")
	}
	a.setMode(projection.Flat)
	a.resolveVideoMode(state)
	if a.mode != projection.Flat || !a.modeManual {
		t.Fatal("automatic detection overrode manual selection")
	}
	// A selection made before the metadata arrives has the same priority.
	a.detectMode = true
	a.setMode(projection.SBSFisheye)
	a.resolveVideoMode(state)
	if a.mode != projection.SBSFisheye {
		t.Fatal("late metadata overrode manual selection")
	}
}

func TestAmbiguousSpatialMetadataDoesNotFinishAsFlat(t *testing.T) {
	t.Setenv("FSVR_PROJECTION_MODEL", "off")
	a := modeTestApplication(t)
	// Keep sampling canceled so this handler test needs neither a real video
	// nor a native window. The decision to start analysis is synchronous.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	a.ctx = ctx
	a.resolveVideoMode(media.State{URL: "current.mp4", Metadata: media.Metadata{
		Width: 7680, Height: 3840, Spherical: "equirectangular", Stereo: "sbs",
	}})
	if !a.detectMode || a.modeDetection == nil {
		t.Fatal("ambiguous full-sphere SBS tags bypassed frame analysis")
	}
	a.cancelModeDetection()
}

func TestRedetectionClearsManualOverrideAndCancelsPreviousTask(t *testing.T) {
	a := modeTestApplication(t)
	a.setMode(projection.SBSFisheye)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a.modeDetection = &modeDetectionTask{source: "current.mp4", cancel: cancel}
	a.redetectMode()
	if ctx.Err() != context.Canceled || a.modeManual || !a.detectMode {
		t.Fatal("redetection retained a manual override or stale task")
	}
	if a.mode != projection.SBSFisheye {
		t.Fatal("redetection changed the view before receiving frame evidence")
	}
}

func TestFlatAndVRPlaybackParametersAreIndependent(t *testing.T) {
	a := &application{
		preferences: defaultPreferences(),
		mode:        projection.Flat,
		view:        projection.View{Yaw: 70, Pitch: 30, Forward: 40, Zoom: 1},
		flatView:    video.FlatView{X: .1, Zoom: 2},
	}
	for _, pip := range []bool{false, true} {
		p := a.params(pip)
		if p.SplitCount != 1 || p.Portrait || p.View != (projection.View{}) || p.FlatView != a.flatView {
			t.Fatalf("VR camera or split leaked into 2D: %+v", p)
		}
	}
	a.mode = projection.SBS180
	p := a.params(false)
	if p.SplitCount != a.preferences.SplitCount || p.View != a.view || p.FlatView != (video.FlatView{}) {
		t.Fatalf("VR preferences were lost: %+v", p)
	}
	if a.params(true).SplitCount != 1 {
		t.Fatal("VR picture-in-picture must remain single-view")
	}
	a.inference = &inferenceWorker{}
	if !a.canTrack(media.State{Playing: true}) {
		t.Fatal("VR face tracking was disabled")
	}
	a.mode = projection.Flat
	if a.canTrack(media.State{Playing: true}) {
		t.Fatal("flat video must not schedule VR face tracking")
	}
}

func TestPlaybackHistoryKeepsBothVideoTypes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	state := storedState{Preferences: defaultPreferences(), History: map[string]videoHistory{
		"flat": {Projection: projection.Flat, AutoProjection: "sbs_180_eqr", Position: 12, ProjectionManual: true},
		"vr":   {AutoProjection: "sbs_180_fe", Position: 34},
	}}
	if err := saveState(path, state); err != nil {
		t.Fatal(err)
	}
	got := loadState(path)
	for key, want := range state.History {
		if got.History[key] != want {
			t.Fatalf("%s history changed: %+v", key, got.History[key])
		}
	}
}

func TestManualTypesAreRememberedAndAutomaticTypesAreRechecked(t *testing.T) {
	for _, mode := range []projection.Mode{projection.Flat, projection.SBS180, projection.SBSFisheye} {
		for _, manual := range []bool{false, true} {
			a := modeTestApplication(t)
			entry := a.entries[0]
			history := videoHistory{Projection: projection.Mono360, AutoProjection: projection.IDs[mode], Position: 12.5}
			if manual {
				history.Projection, history.ProjectionManual = mode, true
			}
			a.stored.History[library.Identity(entry)] = history
			if start := a.prepareVideoMode(entry); start != 12.5 || a.mode != mode || a.modeManual != manual || a.detectMode == manual {
				t.Fatalf("history was not restored with the expected detection state: start=%v mode=%v manual=%v detect=%v", start, a.mode, a.modeManual, a.detectMode)
			}
		}
	}
}

func TestAutomaticCacheIsIndependentOfManualChoiceAndPendingDetection(t *testing.T) {
	a := modeTestApplication(t)
	entry := a.entries[0]
	key := library.Identity(entry)
	state := media.State{URL: entry.Path, Position: 12.5}
	a.mode, a.detectMode = projection.SBS180, false
	a.recordPlaybackHistory(entry, state)
	if got := a.stored.History[key]; got.AutoProjection != "sbs_180_eqr" || got.ProjectionManual {
		t.Fatalf("automatic result was not cached separately: %+v", got)
	}

	a.setMode(projection.Flat)
	a.recordPlaybackHistory(entry, state)
	if got := a.stored.History[key]; got.Projection != projection.Flat || !got.ProjectionManual || got.AutoProjection != "sbs_180_eqr" {
		t.Fatalf("manual choice overwrote the automatic cache: %+v", got)
	}
	a.prepareVideoMode(entry)
	if a.mode != projection.Flat || !a.modeManual || a.detectMode {
		t.Fatal("automatic cache overrode the remembered manual choice")
	}

	a.redetectMode()
	a.recordPlaybackHistory(entry, state)
	if got := a.stored.History[key]; got.ProjectionManual || got.AutoProjection != "sbs_180_eqr" {
		t.Fatalf("pending detection replaced the last automatic result: %+v", got)
	}
	a.prepareVideoMode(entry)
	if a.mode != projection.SBS180 || a.modeManual || !a.detectMode {
		t.Fatal("automatic cache was not applied immediately with rechecking enabled")
	}

	a.setDetectedMode(projection.SBSFisheye)
	a.recordPlaybackHistory(entry, state)
	if got := a.stored.History[key]; got.AutoProjection != "sbs_180_fe" || got.ProjectionManual {
		t.Fatalf("completed recheck did not update the automatic cache: %+v", got)
	}
}

func TestFrameDetectionIgnoresReplacedAndManuallyCanceledTasks(t *testing.T) {
	a := modeTestApplication(t)
	old := &modeDetectionTask{source: "current.mp4", cancel: func() {}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	current := &modeDetectionTask{source: "current.mp4", cancel: cancel}
	a.modeDetection = current
	a.finishModeDetection(old, projection.SBS180)
	if a.mode != projection.Flat || a.modeDetection != current {
		t.Fatal("replaced task affected the current video")
	}
	a.entries[0].Path = "new.mp4"
	a.finishModeDetection(current, projection.SBS180)
	if a.mode != projection.Flat {
		t.Fatal("old file's result changed the new file")
	}
	a.entries[0].Path = "current.mp4"
	a.setMode(projection.Flat)
	if ctx.Err() != context.Canceled {
		t.Fatal("manual selection did not stop frame sampling")
	}
	a.finishModeDetection(current, projection.SBS180)
	if a.mode != projection.Flat || !a.modeManual || a.modeDetection != nil {
		t.Fatal("late frame detection overrode manual selection")
	}
}

func TestSameDetectedModePreservesViewChangesDuringSampling(t *testing.T) {
	a := modeTestApplication(t)
	a.flatView = video.FlatView{X: .1, Zoom: 2}
	a.setDetectedMode(projection.Flat)
	if a.flatView != (video.FlatView{X: .1, Zoom: 2}) || a.modeManual || a.detectMode {
		t.Fatal("automatic confirmation reset the view or was stored as manual")
	}
}
