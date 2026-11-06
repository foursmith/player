package media

import (
	"context"
	"errors"
	"unsafe"

	"github.com/foursmith/player/library"
)

func probeVideo(format *avFormat) *avStream {
	var best *avStream
	for _, stream := range unsafe.Slice(format.Streams, int(format.NbStreams)) {
		p := stream.Codecpar
		if p.CodecType != avVideo || stream.Disposition&avAttachedPic != 0 {
			continue
		}
		if best == nil || int64(p.Width)*int64(p.Height) > int64(best.Codecpar.Width)*int64(best.Codecpar.Height) {
			best = stream
		}
	}
	return best
}

// Probe reads container metadata without starting playback or decoding frames.
func Probe(ctx context.Context, source Source) (_ library.MediaInfo, err error) {
	if err := ctx.Err(); err != nil {
		return library.MediaInfo{}, err
	}
	if err := loadMedia(); err != nil {
		return library.MediaInfo{}, err
	}
	cancel := newCancellation()
	stop := context.AfterFunc(ctx, cancel.cancel)
	defer func() {
		stop()
		cancel.close()
		if ctx.Err() != nil {
			err = ctx.Err()
		}
	}()
	format := av.format.Alloc()
	if format == nil {
		return library.MediaInfo{}, errors.New("cannot allocate media probe")
	}
	defer av.format.Close(&format)
	format.InterruptCallback = avInterrupt{interruptCallback, cancel.token}
	var options unsafe.Pointer
	if source.Headers != "" {
		av.util.DictSet(&options, "headers", source.Headers, 0)
	}
	for _, option := range [][2]string{{"rw_timeout", "2000000"}, {"probesize", "131072"}, {"analyzeduration", "500000"}, {"max_probe_packets", "32"}} {
		av.util.DictSet(&options, option[0], option[1], 0)
	}
	ret := av.format.Open(&format, source.URL, nil, &options)
	av.util.DictFree(&options)
	if ret < 0 {
		return library.MediaInfo{}, mediaError(ret)
	}
	video := probeVideo(format)
	if video == nil || video.Codecpar.Width <= 0 || video.Codecpar.Height <= 0 || format.Duration == avNoTimestamp {
		if ret := av.format.FindInfo(format, nil); ret < 0 {
			return library.MediaInfo{}, mediaError(ret)
		}
		video = probeVideo(format)
	}
	if video == nil {
		return library.MediaInfo{}, errors.New("no video stream")
	}
	duration := 0.0
	if format.Duration != avNoTimestamp {
		duration = float64(format.Duration) / 1e6
	} else if video.Duration != avNoTimestamp {
		duration = float64(video.Duration) * ratio(video.TimeBase)
	}
	return (library.MediaInfo{Duration: duration, Width: int(video.Codecpar.Width), Height: int(video.Codecpar.Height), BitRate: format.BitRate}).Normalized(), nil
}
