package exporter

import (
	"bytes"
	"context"
	"fmt"
	"github.com/foursmith/player/internal/media"
	"github.com/foursmith/player/internal/subtitle"
	"github.com/foursmith/player/internal/video"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

type Options struct {
	Source, Destination, Format string
	Encoder                     string
	Start, End                  float64
	Width, Height, FPS, BitRate int
	Parameters                  video.Parameters
	Progress                    func(int)
	Project                     func(*media.Frame, video.Parameters, float64) (video.Parameters, error)
	Frame                       func(*media.Frame, video.Parameters, float64)
	Cues                        []subtitle.Cue
	CSSWidth                    float64
}

func Export(ctx context.Context, o Options) error {
	if o.End <= o.Start || o.End-o.Start > 60 {
		return fmt.Errorf("clip duration must be between 0 and 60 seconds")
	}
	if o.Width < 2 || o.Height < 2 || o.FPS < 1 {
		return fmt.Errorf("invalid output dimensions or frame rate")
	}
	decoder, err := media.OpenContext(ctx, media.Source{URL: o.Source}, false)
	if err != nil {
		return err
	}
	defer decoder.Close()
	if err = decoder.Seek(o.Start); err != nil {
		return err
	}
	surface, err := video.New(0)
	if err != nil {
		return err
	}
	defer surface.Close()
	dir, err := os.MkdirTemp("", "fsvr-export-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	format := o.Format
	if format == "motion-photo" {
		format = "mp4"
	}
	encoded := filepath.Join(dir, "clip."+format)
	args := []string{"-hide_banner", "-loglevel", "error", "-f", "rawvideo", "-pixel_format", "rgba", "-video_size", fmt.Sprintf("%dx%d", o.Width, o.Height), "-framerate", strconv.Itoa(o.FPS), "-i", "pipe:0", "-ss", fmt.Sprintf("%.6f", o.Start)}
	args = append(args, "-i", o.Source, "-map", "0:v:0", "-map", "1:a:0?", "-t", fmt.Sprintf("%.6f", o.End-o.Start), "-b:a", "128k")
	if len(o.Cues) > 0 && !subtitle.NativeComposition {
		path := filepath.Join(dir, "subtitles.ass")
		if err = os.WriteFile(path, []byte(subtitle.ASS(o.Cues, o.Start, o.End, o.Width, o.Height, o.CSSWidth)), 0600); err != nil {
			return err
		}
		args = append(args, "-vf", "ass=filename='"+strings.NewReplacer("\\", "\\\\", ":", "\\:", "'", "'\\''").Replace(path)+"'")
	}
	if format == "webm" {
		args = append(args, "-c:v", "libvpx-vp9", "-deadline", "realtime", "-cpu-used", "6", "-c:a", "libopus")
	} else {
		args = append(args, "-c:v", "libx264", "-preset", "veryfast", "-c:a", "aac", "-movflags", "+faststart")
	}
	args = append(args, "-b:v", strconv.Itoa(o.BitRate), "-pix_fmt", "yuv420p", "-y", encoded)
	encoder := o.Encoder
	if encoder == "" {
		encoder = "ffmpeg"
	}
	cmd := exec.CommandContext(ctx, encoder, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	input, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	if err = cmd.Start(); err != nil {
		return err
	}
	success := false
	defer func() {
		input.Close()
		if !success {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()
	current, err := decoder.Next()
	if err != nil {
		return err
	}
	defer func() { current.Close() }()
	var next *media.Frame
	defer func() { next.Close() }()
	var cover []byte
	count := int((o.End-o.Start)*float64(o.FPS) + .999999)
	for i := 0; i < count; i++ {
		if err = ctx.Err(); err != nil {
			return err
		}
		at := o.Start + float64(i)/float64(o.FPS)
		for {
			if next == nil {
				next, err = decoder.Next()
				if err == io.EOF {
					break
				}
				if err != nil {
					return err
				}
			}
			if next.PTS() > at {
				break
			}
			current.Close()
			current = next
			next = nil
		}
		params := o.Parameters
		if o.Project != nil {
			params, err = o.Project(current, params, at)
			if err != nil {
				return err
			}
		}
		img, err := surface.Capture(current, params, o.Width, o.Height)
		if err != nil {
			return err
		}
		if subtitle.NativeComposition {
			if err = subtitle.Compose(img, subtitle.Active(o.Cues, at), o.CSSWidth); err != nil {
				return err
			}
		}
		if _, err = input.Write(img.Pix); err != nil {
			input.Close()
			_ = cmd.Wait()
			success = true
			return fmt.Errorf("encoding failed: %w: %s", err, stderr.String())
		}
		if o.Frame != nil {
			o.Frame(current, params, at)
		}
		if o.Progress != nil {
			o.Progress((i + 1) * 100 / count)
		}
	}
	if err = input.Close(); err != nil {
		return err
	}
	if err = cmd.Wait(); err != nil {
		return fmt.Errorf("encoding failed: %w: %s", err, stderr.String())
	}
	success = true
	if o.Format == "motion-photo" {
		// Read the first encoded frame, including the subtitle filter.
		coverPath := filepath.Join(dir, "cover.jpg")
		if output, e := exec.CommandContext(ctx, encoder, "-hide_banner", "-loglevel", "error", "-i", encoded, "-frames:v", "1", "-q:v", "2", coverPath).CombinedOutput(); e != nil {
			return fmt.Errorf("motion photo cover: %w: %s", e, output)
		}
		cover, err = os.ReadFile(coverPath)
		if err != nil {
			return err
		}
		encodedVideo, err := os.ReadFile(encoded)
		if err != nil {
			return err
		}
		photo, err := MotionPhoto(cover, encodedVideo)
		if err != nil {
			return err
		}
		return os.WriteFile(o.Destination, photo, 0600)
	}
	source, err := os.Open(encoded)
	if err != nil {
		return err
	}
	defer source.Close()
	destination, err := os.OpenFile(o.Destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = io.Copy(destination, source)
	closeErr := destination.Close()
	if err != nil {
		return err
	}
	return closeErr
}
