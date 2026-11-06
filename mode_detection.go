package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"image"
	"log"
	"os"
	"time"

	"github.com/foursmith/player/internal/detection"
	"github.com/foursmith/player/internal/library"
	"github.com/foursmith/player/internal/media"
	"github.com/foursmith/player/internal/native"
	"github.com/foursmith/player/internal/projection"
)

type modeDetectionTask struct {
	cancel context.CancelFunc
	source string
	trace  *recognitionTrace
}

func (a *application) prepareVideoMode(entry library.Entry) float64 {
	a.mode = projection.Detect(entry.Name, "", "")
	a.detectMode, a.modeManual = true, false
	start := 0.0
	if (entry.DeviceID == "" || entry.ObjectID != "") && a.preferences.RecordPlaybackHistory {
		if history, ok := a.stored.History[library.Identity(entry)]; ok {
			if a.preferences.ResumePlaybackPosition {
				start = history.Position
			}
			if !a.recognitionTest {
				if history.ProjectionManual {
					a.mode, a.modeManual, a.detectMode = history.Projection, true, false
				} else if mode, ok := history.automaticProjection(); ok {
					a.mode = mode
				}
			}
		}
	}
	if a.recognitionTest {
		log.Printf("[recognition-test] open file=%q cache=bypassed initial=%s resume=%.3fs", entry.Path, projection.IDs[a.mode], start)
	}
	return start
}

func (a *application) cancelModeDetection() {
	if task := a.modeDetection; task != nil {
		task.trace.printf("cancellation requested")
		task.cancel()
		a.modeDetection = nil
	}
}

func (a *application) redetectMode() {
	if a.selected < 0 || a.selected >= len(a.entries) {
		return
	}
	a.cancelModeDetection()
	a.detectMode = true
	a.modeManual = false
	a.persist()
	a.resolveVideoMode(a.player.Snapshot())
}

func (a *application) setDetectedMode(mode projection.Mode) {
	a.cancelModeDetection()
	a.detectMode = false
	a.modeManual = false
	if mode != a.mode {
		a.applyMode(mode)
	} else {
		// Do not reset a view the user adjusted while sampling was in flight.
		a.persist()
	}
}

func (a *application) finishModeDetection(task *modeDetectionTask, mode projection.Mode) {
	if a.shutdown || a.modeDetection != task || !a.detectMode || a.selected < 0 || a.selected >= len(a.entries) || a.entries[a.selected].Path != task.source {
		task.trace.printf("result ignored: shutdown=%t active_task=%t detection_enabled=%t manual=%t", a.shutdown, a.modeDetection == task, a.detectMode, a.modeManual)
		return
	}
	a.modeDetection = nil
	task.cancel()
	if task.trace != nil {
		task.trace.printf("applied mode=%s elapsed=%.2fms", projection.IDs[mode], float64(time.Since(task.trace.started))/float64(time.Millisecond))
	}
	a.setDetectedMode(mode)
}

func (a *application) resolveVideoMode(state media.State) {
	if !a.detectMode || a.modeDetection != nil || state.Loading || state.Metadata.Width == 0 || a.selected < 0 || a.selected >= len(a.entries) {
		return
	}
	entry := a.entries[a.selected]
	if state.URL != entry.Path {
		return
	}
	aspect := float64(state.Metadata.Width) / float64(max(1, state.Metadata.Height))
	mode, conclusive := projection.MetadataMode(state.Metadata.Spherical, state.Metadata.Stereo, aspect)
	if conclusive && !a.recognitionTest {
		log.Printf("Video type: %s (spatial metadata)", projection.IDs[mode])
		a.setDetectedMode(mode)
		return
	}
	if state.Metadata.Spherical != "" {
		if conclusive {
			log.Printf("[recognition-test] metadata candidate=%s; inspecting frames for diagnostics", projection.IDs[mode])
		} else {
			log.Printf("Video type: ambiguous %s/%s metadata at %.2f:1; analyzing frames", state.Metadata.Spherical, state.Metadata.Stereo, aspect)
		}
	}
	ctx, cancel := context.WithTimeout(a.ctx, 12*time.Second)
	task := &modeDetectionTask{cancel: cancel, source: entry.Path}
	if a.recognitionTest {
		a.modeDetectionSequence++
		task.trace = &recognitionTrace{id: a.modeDetectionSequence, started: time.Now(), logger: log.Default()}
		task.trace.printf("start file=%q cache=bypassed timeout=12s", entry.Path)
	}
	a.modeDetection = task
	window := a.window
	modelPath := os.Getenv("FSVR_PROJECTION_MODEL")
	if !a.preferences.ProjectionAI {
		modelPath = "off"
	}
	go func() {
		defer cancel()
		decision := analyzeVideoMode(ctx, entry, state.Metadata, modelPath, task.trace)
		if ctx.Err() == context.Canceled {
			return
		}
		window.Update(func() { a.finishModeDetection(task, decision.Mode) })
	}()
}

func analyzeVideoMode(ctx context.Context, entry library.Entry, metadata media.Metadata, modelPath string, trace *recognitionTrace) projection.Decision {
	aspect := float64(metadata.Width) / float64(max(1, metadata.Height))
	fallback := projection.Detect(entry.Name, "", "")
	metadataMode, conclusive := projection.MetadataMode(metadata.Spherical, metadata.Stereo, aspect)
	trace.printf("metadata size=%dx%d duration=%.3fs fps=%.3f playback_decoder=%s spherical=%q stereo=%q conclusive=%t candidate=%s filename_fallback=%s", metadata.Width, metadata.Height, metadata.Duration, metadata.FrameRate, metadata.Decoder, metadata.Spherical, metadata.Stereo, conclusive, projection.IDs[metadataMode], projection.IDs[fallback])
	trace.printf("thresholds min_usable_frames=3 max_usable_frames=9 max_attempts=9 projection_refinement_frames=9 consensus=2/3 model_min_frames=2 model_min_score=0.8 model_min_margin=0.15 partial_stereo_agreement_min=0.125")
	classifier := projectionClassifier(modelPath, trace)
	if classifier != nil {
		defer classifier.Close()
	}
	scores, detector := &projection.ModelScores{}, &projection.FrameDetector{}
	attempt := 0
	var samplingTrace func(media.SampleTrace)
	if trace != nil {
		samplingTrace = func(info media.SampleTrace) {
			attempt = info.Attempt
			trace.printf("sample stage=%s attempt=%d requested=%.3fs pts=%.3fs strategy=%s duplicate_replaced=%t duplicate_pts=%.3fs size=%dx%d decoder=%s elapsed=%.2fms error=%v", info.Stage, info.Attempt, info.Requested, info.PTS, info.Strategy, info.ReplacedDuplicate, info.DuplicatePTS, info.Width, info.Height, info.Decoder, float64(info.Elapsed)/float64(time.Millisecond), info.Err)
		}
	}
	sampleErr := media.SampleFrames(ctx, entry.Path, func(img *image.RGBA) bool {
		evidence := projection.AnalyzeFrame(img, aspect)
		detector.Observe(evidence)
		trace.printf("frame attempt=%d usable=%t rejection=%q rule=%s luma=%.5f variance=%.5f bright=%.5f stereo_layout=%s stereo_similarity=%.5f stereo_agreement=%.5f wide_disparity=%t accepted_frames=%d", attempt, evidence.Usable, evidence.Rejection, projection.IDs[evidence.Mode], evidence.MeanLuma, evidence.Variance, evidence.BrightFraction, projection.IDs[evidence.StereoLayout], evidence.Similarity, evidence.StereoAgreement, evidence.WideDisparity, detector.Samples)
		if classifier != nil && evidence.Usable {
			started := time.Now()
			probabilities, err := classifier.Classify(img)
			if err == nil && !scores.Add(probabilities) {
				err = errors.New("invalid projection model probabilities")
			}
			if err != nil {
				log.Printf("Experimental projection inference failed: %v", err)
				trace.printf("model inference failed; falling back to image rules: %v", err)
				classifier.Close()
				classifier, scores = nil, &projection.ModelScores{}
			} else if trace != nil {
				trace.printf("model attempt=%d elapsed=%.2fms scores: %s", attempt, float64(time.Since(started))/float64(time.Millisecond), projectionValues(probabilities))
			}
		}
		done := detector.SamplingComplete(scores)
		if evidence.Usable && detector.Samples >= 3 && !done {
			trace.printf("sampling extended: ambiguous result after %d usable frames", detector.Samples)
		}
		return done
	}, samplingTrace)
	if ctx.Err() == context.Canceled {
		trace.printf("aborted: %v", ctx.Err())
		return projection.Decision{Mode: fallback, Reason: "canceled"}
	}
	if sampleErr != nil {
		log.Printf("Video type sampling failed: %v (%d usable frames)", sampleErr, detector.Samples)
	}
	decision := detector.Resolve(fallback, scores)
	if trace != nil {
		votes, p := detector.Votes(), scores.Probabilities()
		partial := detector.PartialStereoVotes()
		rule, stable := detector.Result()
		trace.printf("consensus frames=%d stable=%t rule=%s votes: %s", detector.Samples, stable, projection.IDs[rule], projectionValues(votes[:]))
		trace.printf("partial stereo votes: %s", projectionValues(partial[:]))
		predicted, best, confident := scores.Result()
		second := 0.0
		for mode, score := range p {
			if mode != int(predicted) {
				second = max(second, score)
			}
		}
		trace.printf("model aggregate samples=%d predicted=%s best=%.5f margin=%.5f confident=%t scores: %s", scores.Samples, projection.IDs[predicted], best, best-second, confident, projectionValues(p[:]))
		confidentVotes := scores.ConfidentVotes()
		consensusMode, supportMean, support, accepted := scores.BinocularConsensus()
		trace.printf("model frame consensus mode=%s support=%d/%d support_mean=%.5f eligible=%t votes: %s", projection.IDs[consensusMode], support, scores.Samples, supportMean, accepted, projectionValues(confidentVotes[:]))
		layout, layoutScore, layoutConfident := scores.BinocularLayout()
		trace.printf("model binocular layout=%s score=%.5f confident=%t", projection.IDs[layout], layoutScore, layoutConfident)
		trace.printf("fusion mode=%s reason=%s", projection.IDs[decision.Mode], decision.Reason)
	}
	if conclusive {
		decision = projection.Decision{Mode: metadataMode, Reason: "spatial_metadata"}
	}
	trace.printf("final mode=%s reason=%s sample_error=%v", projection.IDs[decision.Mode], decision.Reason, sampleErr)
	if scores.Samples > 0 {
		predicted, score, confident := scores.Result()
		log.Printf("Video type: %s (%d frames; AI=%s %.1f%%, confident=%t; reason=%s)", projection.IDs[decision.Mode], detector.Samples, projection.IDs[predicted], score*100, confident, decision.Reason)
	} else {
		log.Printf("Video type: %s (%d frames; image rules)", projection.IDs[decision.Mode], detector.Samples)
	}
	return decision
}

func projectionClassifier(path string, trace *recognitionTrace) *detection.ImageClassifier {
	if path == "off" {
		trace.printf("model disabled by settings or FSVR_PROJECTION_MODEL=off")
		return nil
	}
	var model []byte
	var err error
	if path == "" {
		model, err = assets.ReadFile("assets/models/projection.tflite")
	} else {
		model, err = os.ReadFile(path)
	}
	if err != nil {
		log.Printf("Experimental projection model: %v", err)
		trace.printf("model unavailable source=%q error=%v", path, err)
		return nil
	}
	started, runtime := time.Now(), native.InferenceLibrary()
	classifier, err := detection.NewImageClassifier(runtime, model, len(projection.IDs))
	if err != nil {
		log.Printf("Experimental projection model: %v", err)
	}
	if trace != nil {
		source := path
		if source == "" {
			source = "embedded:assets/models/projection.tflite"
		}
		trace.printf("model source=%q bytes=%d sha256=%x runtime=%q init=%.2fms error=%v", source, len(model), sha256.Sum256(model), runtime, float64(time.Since(started))/float64(time.Millisecond), err)
	}
	return classifier
}
