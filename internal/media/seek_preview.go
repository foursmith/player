package media

import (
	"context"
	"errors"
	"image"
	"io"
)

// SeekPreview owns a silent decoder, reused by one preview worker. It never
// shares a codec, audio device, seek state, or frame with the playing video.
type SeekPreview struct {
	decoder       *Decoder
	width, height int
	metadata      Metadata
	converter     RGBAConverter
	lastPTS       float64
}

func OpenSeekPreview(ctx context.Context, source string) (*SeekPreview, error) {
	d, err := openContext(ctx, Source{URL: source}, false, true)
	if err != nil {
		return nil, err
	}
	m := d.Metadata()
	w, h := thumbnailSize(m.Width, m.Height)
	return &SeekPreview{decoder: d, metadata: m, lastPTS: -1, width: w, height: h}, nil
}

// Image returns a quick keyframe unless precise scrubbing explicitly requests
// the target frame. Obsolete requests yield without closing the decoder.
func (p *SeekPreview) Image(at float64, precise bool, obsolete func() bool) (*image.RGBA, error) {
	if obsolete != nil && obsolete() {
		return nil, context.Canceled
	}
	d := p.decoder
	m := p.metadata
	at = max(0, min(at, max(0, m.Duration-1/max(1, m.FrameRate))))
	previous := p.lastPTS
	p.lastPTS = -1
	if precise && previous >= 0 && at > previous && at-previous <= 1 {
		// Continue the existing GOP for nearby forward targets instead of
		// flushing the codec and decoding its reference pictures again.
		d.seekTarget = at
	} else if err := d.seek(at, precise); err != nil {
		return nil, err
	}
	frame, err := d.next(obsolete)
	// Containers may round the final timestamp beyond the last frame.
	if precise && errors.Is(err, io.EOF) && at > 0 {
		if err = d.Seek(max(0, at-1/max(1, m.FrameRate))); err == nil {
			frame, err = d.next(obsolete)
		}
	}
	if errors.Is(err, errSeekPending) {
		return nil, context.Canceled
	}
	if err != nil {
		return nil, err
	}
	defer frame.Close()
	img, err := p.converter.Convert(frame, p.width, p.height)
	if err != nil {
		return nil, err
	}
	p.lastPTS = frame.PTS()
	// Cached previews must outlive the converter's reusable output buffers.
	out := image.NewRGBA(img.Bounds())
	copy(out.Pix, img.Pix)
	return out, nil
}

func (p *SeekPreview) Close() {
	p.converter.Close()
	p.decoder.Close()
}
