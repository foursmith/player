package projection

import (
	"math"
	"testing"
)

func prediction(mode Mode) []float32 {
	p := []float32{.02, .02, .02, .02, .02, .02, .02}
	p[mode] = .88
	return p
}

func TestModelScoresJointlySelectEveryCompleteFormat(t *testing.T) {
	for mode := SBS180; mode <= Flat; mode++ {
		scores := &ModelScores{}
		scores.Add(prediction(mode))
		if _, _, ok := scores.Result(); ok {
			t.Fatal("a single frame completed recognition")
		}
		scores.Add(prediction(mode))
		if got, _, ok := scores.Result(); !ok || got != mode {
			t.Fatal(mode, got, ok)
		}
	}
}

func TestMonoModelPredictionsCannotCollapseStableBinocularEvidence(t *testing.T) {
	for _, geometry := range []struct {
		mode        Mode
		tb, fisheye bool
		aspect      float64
	}{{SBS180, false, false, 2}, {SBSFisheye, false, true, 2}, {TB360, true, false, 1}} {
		for _, mono := range []Mode{Mono360, Mono180, MonoFisheye} {
			detector, model := &FrameDetector{}, &ModelScores{}
			for range 3 {
				observeImage(detector, stereoFrame(geometry.tb, geometry.fisheye), geometry.aspect)
				model.Add(prediction(mono))
			}
			decision := detector.Resolve(Flat, model)
			mode := decision.Mode
			if mode != geometry.mode {
				t.Fatalf("binocular %v was collapsed by mono %v: %v", geometry.mode, mono, mode)
			}
		}
	}
}

func TestBinocularProtectionStillAllowsProjectionRefinementAndUncertainMono(t *testing.T) {
	detector, model := &FrameDetector{}, &ModelScores{}
	for range projectionConfirmationFrames {
		observeImage(detector, stereoFrame(false, false), 2)
		model.Add(prediction(SBSFisheye))
	}
	if mode := detector.Resolve(Flat, model).Mode; mode != SBSFisheye {
		t.Fatal("SBS fisheye refinement was blocked:", mode)
	}
	detector, model = &FrameDetector{}, &ModelScores{}
	observeImage(detector, stereoFrame(false, false), 2)
	model.Add(prediction(Mono180))
	model.Add(prediction(Mono180))
	if mode := detector.Resolve(Flat, model).Mode; mode != Mono180 {
		t.Fatal("one frame incorrectly locked a binocular layout:", mode)
	}
	detector = &FrameDetector{}
	for range 3 {
		observeImage(detector, textured(320, 160, 89), 2)
	}
	if mode := detector.Resolve(SBS180, model).Mode; mode != Mono180 {
		t.Fatal("a filename hint was mistaken for binocular image evidence:", mode)
	}
}

func TestModelScoresRejectUncertainProjectionAndInvalidOutputs(t *testing.T) {
	scores := &ModelScores{}
	if mode, _, ok := scores.Result(); ok || mode != Flat {
		t.Fatal("empty scores selected VR")
	}
	scores.Add(prediction(SBS180))
	scores.Add(prediction(SBSFisheye))
	scores.Add(prediction(SBSFisheye))
	if _, _, ok := scores.Result(); ok {
		t.Fatal("uncertain VR subtype was accepted")
	}
	before := scores.Probabilities()
	beforeVotes := scores.ConfidentVotes()
	for _, p := range [][]float32{
		{1},
		{1, 1, 1, 1, 1, 1, 1},
		{float32(math.NaN()), 0, 0, 0, 0, 0, 0},
		{float32(math.Inf(1)), 0, 0, 0, 0, 0, 0},
		{-.1, 1, .1, 0, 0, 0, 0},
	} {
		if scores.Add(p) || scores.Samples != 3 || scores.Probabilities() != before || scores.ConfidentVotes() != beforeVotes {
			t.Fatal("invalid output contaminated the multi-frame result", p)
		}
	}
}

func TestConfidentBinocularFramesRescueFlatFallbackWithPartialCorrespondence(t *testing.T) {
	// Recorded model outputs from the failing 8K EQR clip: two clear EQR
	// frames and one uncertain close-up must not become a 2D result.
	frames := [][]float32{
		{.9991911, .00051391363, .00028505214, .00000009458151, .000002580357, .000007005206, .000000131131},
		{.98691326, .011508645, .0015293652, .00000015525457, .0000457825, .0000022019317, .00000049939587},
		{.1725203, .00918266, .6838065, .00018712375, .000044717683, .000027213846, .13423148},
	}
	for _, layout := range []Mode{SBS180, TB360} {
		detector, scores := &FrameDetector{}, &ModelScores{}
		for _, frame := range frames {
			p := append([]float32(nil), frame...)
			if layout == TB360 {
				p[SBS180], p[TB360] = p[TB360], p[SBS180]
			}
			if !scores.Add(p) {
				t.Fatal("recorded probabilities were rejected")
			}
			detector.Observe(FrameEvidence{Mode: Flat, Usable: true, StereoLayout: layout, Similarity: .85, StereoAgreement: .1875})
		}
		if _, _, confident := scores.Result(); confident {
			t.Fatal("the original mean threshold was loosened")
		}
		decision := detector.Resolve(Flat, scores)
		if decision.Mode != layout || decision.Reason != "model_frame_consensus" {
			t.Fatalf("confident binocular frames fell back to 2D: %+v", decision)
		}
		// Model semantics alone must not activate the supplementary path.
		withoutEyes := &FrameDetector{}
		for range 3 {
			withoutEyes.Observe(FrameEvidence{Mode: Flat, Usable: true})
		}
		if mode := withoutEyes.Resolve(Flat, scores).Mode; mode != Flat {
			t.Fatal("partial eye correspondence was not required:", mode)
		}
	}
}

func TestSupplementaryConsensusRejectsConflictsAndPreservesEstablishedProjection(t *testing.T) {
	conflicting := &ModelScores{}
	for _, mode := range []Mode{SBS180, SBSFisheye, SBSFisheye} {
		conflicting.Add(prediction(mode))
	}
	if _, _, _, ok := conflicting.BinocularConsensus(); ok {
		t.Fatal("a confident contradictory frame was discarded")
	}
	mono := &ModelScores{}
	mono.Add(prediction(Mono180))
	mono.Add(prediction(Mono180))
	if _, _, _, ok := mono.BinocularConsensus(); ok {
		t.Fatal("the supplementary path promoted an auxiliary mono class")
	}
	scores := &ModelScores{}
	scores.Add(prediction(SBSFisheye))
	scores.Add(prediction(SBSFisheye))
	scores.Add([]float32{.15, .15, .6, .025, .025, .025, .025})
	if _, _, _, ok := scores.BinocularConsensus(); !ok {
		t.Fatal("fixture did not produce confident-frame support")
	}
	detector := &FrameDetector{}
	for range 3 {
		observeImage(detector, stereoFrame(false, false), 2)
	}
	if decision := detector.Resolve(Flat, scores); decision.Mode != SBS180 || decision.Reason != "frame_consensus" {
		t.Fatalf("supplementary votes overwrote an established EQR projection: %+v", decision)
	}
}

func TestBinocularLayoutConsensusNeedsRepeatedMatchingEyesAndNoCompetingLayout(t *testing.T) {
	// EQR/FE ambiguity from a six-frame real-video run. The model confirms
	// SBS packing but cannot establish a lens projection by itself.
	probabilities := []float32{.5068811, .48818502, .004886644, .000009996064, .0000057554673, .00003037415, .0000010845008}
	for _, tc := range []struct {
		name  string
		modes []Mode
		want  Mode
	}{
		{"three matching views", []Mode{Flat, Flat, SBS180, Mono360, SBS180, SBS180}, SBS180},
		{"unmatched views abstain", []Mode{Mono360, Flat, SBS180, Flat, Flat, Flat, SBS180, Flat, SBS180}, SBS180},
		{"competing positive VR evidence", []Mode{Mono360, Mono360, SBS180, Flat, Flat, Flat, SBS180, Flat, SBS180}, Flat},
		{"only two matching views", []Mode{Flat, Flat, SBS180, Flat, SBS180, Flat}, Flat},
		{"competing binocular layout", []Mode{Flat, TB360, SBS180, Mono360, SBS180, SBS180}, Flat},
		{"no geometric eye evidence", []Mode{Flat, Flat, Flat, Flat, Flat, Flat}, Flat},
	} {
		t.Run(tc.name, func(t *testing.T) {
			detector, scores := &FrameDetector{}, &ModelScores{}
			for _, mode := range tc.modes {
				detector.Observe(FrameEvidence{Mode: mode, Usable: true})
				scores.Add(probabilities)
			}
			if _, _, confident := scores.Result(); confident {
				t.Fatal("uncertain subtypes unexpectedly became a complete format")
			}
			if layout, _, confident := scores.BinocularLayout(); !confident || layout != SBS180 {
				t.Fatal("the model did not confirm the combined SBS layout")
			}
			decision := detector.Resolve(Flat, scores)
			if decision.Mode != tc.want || tc.want != Flat && decision.Reason != "binocular_layout_consensus" {
				t.Fatalf("unexpected fusion: %+v", decision)
			}
		})
	}
}

func TestSamplingExtendsUncertainAndConflictingProjectionEvidence(t *testing.T) {
	for _, tc := range []struct {
		name             string
		rule, prediction Mode
		completeAtThree  bool
	}{
		{"clear stereo", SBS180, SBS180, true},
		{"clear 2D", Flat, Flat, true},
		{"visible fisheye", SBSFisheye, SBS180, true},
		{"conflicting subtype", SBS180, SBSFisheye, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			detector, scores := &FrameDetector{}, &ModelScores{}
			for i := range projectionConfirmationFrames {
				detector.Observe(FrameEvidence{Mode: tc.rule, Usable: true})
				scores.Add(prediction(tc.prediction))
				if i < 2 && detector.SamplingComplete(scores) {
					t.Fatal("sampling completed before three usable frames")
				}
				if i == 2 && detector.SamplingComplete(scores) != tc.completeAtThree {
					t.Fatal("unexpected three-frame stopping decision")
				}
				if tc.rule == SBS180 && tc.prediction == SBSFisheye {
					decision := detector.Resolve(Flat, scores)
					if i >= 1 && i < projectionConfirmationFrames-1 && (decision.Mode != SBS180 || decision.Reason != "projection_conflict") {
						t.Fatalf("subtype changed before confirmation: %+v", decision)
					}
					if i == projectionConfirmationFrames-1 && (decision.Mode != SBSFisheye || !detector.SamplingComplete(scores)) {
						t.Fatalf("consistent confirmation views could not refine the projection: %+v", decision)
					}
				}
			}
		})
	}
	detector, scores := &FrameDetector{}, &ModelScores{}
	for i := range 9 {
		detector.Observe(FrameEvidence{Mode: Flat, Usable: true})
		scores.Add([]float32{1.0 / 7, 1.0 / 7, 1.0 / 7, 1.0 / 7, 1.0 / 7, 1.0 / 7, 1.0 / 7})
		if complete := detector.SamplingComplete(scores); complete != (i == 8) {
			t.Fatalf("uncertain result stopped at %d frames: %t", i+1, complete)
		}
	}
	if !detector.SamplingComplete(nil) {
		t.Fatal("disabled model prevented the rule-only path from stopping")
	}
}

func TestTransientFisheyeModelVotesWaitForMoreViews(t *testing.T) {
	detector, scores := &FrameDetector{}, &ModelScores{}
	for _, mode := range []Mode{Flat, SBS180, Flat} {
		detector.Observe(FrameEvidence{Mode: mode, Usable: true, StereoLayout: SBS180, Similarity: .85, StereoAgreement: .1875})
		scores.Add(prediction(SBSFisheye))
	}
	if detector.SamplingComplete(scores) {
		t.Fatal("three model-only fisheye votes skipped projection confirmation")
	}
	if decision := detector.Resolve(Flat, scores); decision.Mode != Flat || decision.Reason != "projection_conflict" {
		t.Fatalf("transient model-only fisheye changed the projection: %+v", decision)
	}
	for range 3 {
		detector.Observe(FrameEvidence{Mode: SBS180, Usable: true})
		scores.Add(prediction(SBS180))
	}
	if decision := detector.Resolve(Flat, scores); decision.Mode != SBS180 || !detector.SamplingComplete(scores) {
		t.Fatalf("additional EQR views failed to resolve the conflict: %+v", decision)
	}
}
