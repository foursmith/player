package media

import (
	"bytes"
	"context"
	"errors"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"math"
	"unsafe"

	"golang.org/x/image/draw"
)

// ChapterArtwork reads the container's timed picture track. Album covers and
// frames from the main movie are deliberately not used as chapter artwork.
type ChapterArtwork struct {
	decoder *Decoder
	stream  *avStream
}

var ErrNoChapterArtwork = errors.New("video has no embedded chapter artwork")

func chapterArtworkStream(format *avFormat) *avStream {
	for _, stream := range unsafe.Slice(format.Streams, int(format.NbStreams)) {
		if stream.Codecpar.CodecType == avVideo && stream.Disposition&avTimedThumbnails != 0 {
			return stream
		}
	}
	return nil
}

func OpenChapterArtwork(ctx context.Context, source Source) (_ *ChapterArtwork, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := loadMedia(); err != nil {
		return nil, err
	}
	d := &Decoder{ctx: ctx, cancellation: newCancellation()}
	d.stop = context.AfterFunc(ctx, d.cancellation.cancel)
	defer func() {
		if err != nil {
			d.Close()
			if ctx.Err() != nil {
				err = ctx.Err()
			}
		}
	}()
	d.format = av.format.Alloc()
	if d.format == nil {
		return nil, errors.New("cannot allocate chapter artwork reader")
	}
	d.format.InterruptCallback = avInterrupt{interruptCallback, d.cancellation.token}
	var options unsafe.Pointer
	if source.Headers != "" {
		av.util.DictSet(&options, "headers", source.Headers, 0)
	}
	av.util.DictSet(&options, "rw_timeout", "10000000", 0)
	ret := av.format.Open(&d.format, source.URL, nil, &options)
	av.util.DictFree(&options)
	if ret < 0 {
		return nil, mediaError(ret)
	}
	if d.format.StartTime != avNoTimestamp {
		d.startTime = float64(d.format.StartTime) / 1e6
	}
	pictures := chapterArtworkStream(d.format)
	for _, stream := range unsafe.Slice(d.format.Streams, int(d.format.NbStreams)) {
		stream.Discard = avDiscardAll
	}
	if pictures == nil {
		return nil, ErrNoChapterArtwork
	}
	pictures.Discard = 0
	// Drop the synthetic attached-picture copy (timestamp zero); the actual
	// timed packets retain the chapter offsets, including initial empty edits.
	pictures.Disposition &^= avAttachedPic
	d.packet = av.codec.PacketAlloc()
	if d.packet == nil {
		return nil, errors.New("cannot allocate chapter picture packet")
	}
	return &ChapterArtwork{decoder: d, stream: pictures}, nil
}

func (p *ChapterArtwork) Image(at float64) (*image.RGBA, error) {
	d := p.decoder
	if err := d.ctx.Err(); err != nil {
		return nil, err
	}
	if p.stream.TimeBase.Num <= 0 || p.stream.TimeBase.Den <= 0 {
		return nil, errors.New("invalid chapter artwork clock")
	}
	target := int64(math.Round((at + d.startTime) / ratio(p.stream.TimeBase)))
	if ret := av.format.Seek(d.format, p.stream.Index, math.MinInt64, target, target, 0); ret < 0 {
		return nil, d.failure(ret)
	}
	for {
		if err := d.ctx.Err(); err != nil {
			return nil, err
		}
		av.codec.PacketUnref(d.packet)
		ret := av.format.Read(d.format, d.packet)
		if ret == avEOF {
			return nil, io.EOF
		}
		if ret < 0 {
			return nil, d.failure(ret)
		}
		if d.packet.StreamIndex != p.stream.Index {
			continue
		}
		// A chapter image describes a time range, so a seek can return its
		// earlier start. Never substitute a picture belonging to a later chapter.
		if d.packet.Pts != avNoTimestamp && d.packet.Pts > target+1 {
			return nil, ErrNoChapterArtwork
		}
		if d.packet.Data == nil || d.packet.Size <= 0 || d.packet.Size > 16<<20 {
			return nil, errors.New("invalid chapter image size")
		}
		data := unsafe.Slice(d.packet.Data, int(d.packet.Size))
		config, _, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		if config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > 32<<20 {
			return nil, errors.New("invalid chapter image dimensions")
		}
		img, _, err := image.Decode(bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		w, h := thumbnailSize(config.Width, config.Height)
		out := image.NewRGBA(image.Rect(0, 0, w, h))
		draw.ApproxBiLinear.Scale(out, out.Bounds(), img, img.Bounds(), draw.Src, nil)
		return out, nil
	}
}

func (p *ChapterArtwork) Close() { p.decoder.Close() }
