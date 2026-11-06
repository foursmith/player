package media

import (
	"context"
	"fmt"
	"sort"
	"structs"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/foursmith/player/internal/apple"
	"github.com/foursmith/player/internal/native"
)

type cmTime struct {
	_     structs.HostLayout
	Value int64
	Scale int32
	Flags uint32
	Epoch int64
}
type sampleTiming struct {
	_                              structs.HostLayout
	Duration, Presentation, Decode cmTime
}
type outputCallback struct{ Callback, Context uintptr }

var coreMedia struct {
	H264         func(uintptr, uintptr, **byte, *uintptr, int32, *uintptr) int32
	HEVC         func(uintptr, uintptr, **byte, *uintptr, int32, uintptr, *uintptr) int32
	BlockCreate  func(uintptr, uintptr, uintptr, uintptr, uintptr, uintptr, uintptr, uint32, *uintptr) int32
	BlockCopy    func(*byte, uintptr, uintptr, uintptr) int32
	SampleCreate func(uintptr, uintptr, uintptr, int64, int64, *sampleTiming, int64, *uintptr, *uintptr) int32
}
var vt struct {
	Create     func(uintptr, uintptr, uintptr, uintptr, *outputCallback, *uintptr) int32
	Property   func(uintptr, uintptr, uintptr) int32
	Decode     func(uintptr, uintptr, uint32, uintptr, *uint32) int32
	Finish     func(uintptr) int32
	Wait       func(uintptr) int32
	Invalidate func(uintptr)
}
var vtFrames struct {
	Alloc       func(*avBuffer) *avBuffer
	Init        func(*avBuffer) int32
	Buffer      func(uintptr, uintptr, uintptr, uintptr, int32) *avBuffer
	Attachments func(uintptr, uintptr, *avFrame) int32
}
var vtKeys struct{ Hardware, Realtime, PixelFormat, Metal, Surface uintptr }

var loadVideoToolbox = sync.OnceValue(func() error {
	if err := apple.Load(); err != nil {
		return err
	}
	cm, err := apple.Framework("CoreMedia")
	if err != nil {
		return err
	}
	if err := native.Bind(cm, &coreMedia, "CMVideoFormatDescriptionCreateFromH264ParameterSets", "CMVideoFormatDescriptionCreateFromHEVCParameterSets", "CMBlockBufferCreateWithMemoryBlock", "CMBlockBufferReplaceDataBytes", "CMSampleBufferCreateReady"); err != nil {
		return err
	}
	toolbox, err := apple.Framework("VideoToolbox")
	if err != nil {
		return err
	}
	if err := native.Bind(toolbox, &vt, "VTDecompressionSessionCreate", "VTSessionSetProperty", "VTDecompressionSessionDecodeFrame", "VTDecompressionSessionFinishDelayedFrames", "VTDecompressionSessionWaitForAsynchronousFrames", "VTDecompressionSessionInvalidate"); err != nil {
		return err
	}
	cv, err := apple.Framework("CoreVideo")
	if err != nil {
		return err
	}
	for _, item := range []struct {
		handle uintptr
		dst    *uintptr
		name   string
	}{
		{toolbox, &vtKeys.Hardware, "kVTVideoDecoderSpecification_RequireHardwareAcceleratedVideoDecoder"},
		{toolbox, &vtKeys.Realtime, "kVTDecompressionPropertyKey_RealTime"},
		{cv, &vtKeys.PixelFormat, "kCVPixelBufferPixelFormatTypeKey"},
		{cv, &vtKeys.Metal, "kCVPixelBufferMetalCompatibilityKey"},
		{cv, &vtKeys.Surface, "kCVPixelBufferIOSurfacePropertiesKey"},
	} {
		if *item.dst, err = apple.Constant(item.handle, item.name); err != nil {
			return err
		}
	}
	h, err := native.Open(native.HostTarget().Library("avutil", 61))
	if err != nil {
		return err
	}
	return native.Bind(h, &vtFrames, "av_hwframe_ctx_alloc", "av_hwframe_ctx_init", "av_buffer_create", "av_vt_pixbuf_set_attachments")
})

type videoPicture struct {
	token  uintptr
	pts    int64
	image  uintptr
	status int32
	done   bool
}
type nativeVideoPipeline struct {
	ctx                                               context.Context
	parameters                                        *avParameters
	timeBase                                          avRational
	frames                                            *avBuffer
	session, format, attributes, specification, token uintptr
	mu                                                sync.Mutex
	pictures                                          []*videoPicture
	pending                                           map[uintptr]*videoPicture
	nextToken                                         uintptr
	lastDTS, nextPTS, randomAccessPTS                 int64
	draining                                          bool
	changed                                           chan struct{}
	err                                               string
}

var pipelineTokens atomic.Uint64
var videoPipelines sync.Map
var videoOutput = purego.NewCallback(func(token, source uintptr, status int32, flags uint32, image uintptr, pts, duration cmTime) {
	value, ok := videoPipelines.Load(token)
	if !ok {
		return
	}
	d := value.(*nativeVideoPipeline)
	d.mu.Lock()
	if picture := d.pending[source]; picture != nil {
		picture.status, picture.done = status, true
		if image != 0 {
			picture.image = apple.Core.Retain(image)
		}
	}
	d.mu.Unlock()
	select {
	case d.changed <- struct{}{}:
	default:
	}
})
var releaseVideoImage = purego.NewCallback(func(_ uintptr, image uintptr) { apple.Core.Release(image) })

func newVideoPipeline(stream *avStream, device *avBuffer, ctx context.Context) videoPipeline {
	p := stream.Codecpar
	if device == nil || (p.CodecId != avHEVC && p.CodecId != avH264) || loadVideoToolbox() != nil {
		return nil
	}
	d := &nativeVideoPipeline{ctx: ctx, parameters: p, timeBase: stream.TimeBase, lastDTS: avNoTimestamp, randomAccessPTS: avNoTimestamp, pending: make(map[uintptr]*videoPicture), changed: make(chan struct{}, 1), token: uintptr(pipelineTokens.Add(1))}
	videoPipelines.Store(d.token, d)
	if d.format = d.formatDescription(); d.format == 0 {
		d.close()
		return nil
	}
	d.specification = apple.Dictionary([]uintptr{vtKeys.Hardware}, []uintptr{apple.True})
	format, software := int32(apple.NV12Video), "nv12"
	if pixel := av.util.PixelDesc(p.Format); pixel != nil && pixel.Comp[0].Depth > 8 {
		format, software = int32(apple.P010Video), "p010le"
		if p.ColorRange == avColorFull {
			format = apple.P010Full
		}
	} else if p.ColorRange == avColorFull {
		format = apple.NV12Full
	}
	number, surface := apple.Number(format), apple.Dictionary(nil, nil)
	d.attributes = apple.Dictionary([]uintptr{vtKeys.PixelFormat, vtKeys.Metal, vtKeys.Surface}, []uintptr{number, apple.True, surface})
	apple.Core.Release(number)
	apple.Core.Release(surface)
	d.frames = vtFrames.Alloc(device)
	if d.frames == nil {
		d.close()
		return nil
	}
	frames := (*avHWFrames)(unsafe.Pointer(d.frames.Data))
	frames.Format, frames.SwFormat = av.util.PixelFormat("videotoolbox_vld"), av.util.PixelFormat(software)
	frames.Width, frames.Height = p.Width, p.Height
	if vtFrames.Init(d.frames) < 0 || d.createSession() < 0 {
		d.close()
		return nil
	}
	return d
}

func (d *nativeVideoPipeline) formatDescription() uintptr {
	if d.parameters.Extradata == nil || d.parameters.ExtradataSize < 7 {
		return 0
	}
	x := unsafe.Slice(d.parameters.Extradata, int(d.parameters.ExtradataSize))
	if x[0] != 1 {
		return 0
	}
	var sets []*byte
	var sizes []uintptr
	add := func(at *int) bool {
		if *at+2 > len(x) || len(sets) == 64 {
			return false
		}
		size := int(x[*at])<<8 | int(x[*at+1])
		*at += 2
		if size == 0 || *at+size > len(x) {
			return false
		}
		sets = append(sets, &x[*at])
		sizes = append(sizes, uintptr(size))
		*at += size
		return true
	}
	var format uintptr
	if d.parameters.CodecId == avHEVC {
		if len(x) < 23 {
			return 0
		}
		at := 23
		for a := 0; a < int(x[22]); a++ {
			if at+3 > len(x) {
				return 0
			}
			kind := x[at] & 63
			count := int(x[at+1])<<8 | int(x[at+2])
			at += 3
			for range count {
				if kind >= 32 && kind <= 34 {
					if !add(&at) {
						return 0
					}
				} else {
					if at+2 > len(x) {
						return 0
					}
					size := int(x[at])<<8 | int(x[at+1])
					at += 2 + size
					if at > len(x) {
						return 0
					}
				}
			}
		}
		if len(sets) > 0 {
			coreMedia.HEVC(0, uintptr(len(sets)), &sets[0], &sizes[0], int32(x[21]&3)+1, 0, &format)
		}
	} else {
		at, count := 6, int(x[5]&31)
		for group := 0; group < 2; group++ {
			for range count {
				if !add(&at) {
					return 0
				}
			}
			if group == 0 {
				if at >= len(x) {
					return 0
				}
				count = int(x[at])
				at++
			}
		}
		if len(sets) > 0 {
			coreMedia.H264(0, uintptr(len(sets)), &sets[0], &sizes[0], int32(x[4]&3)+1, &format)
		}
	}
	return format
}

func (d *nativeVideoPipeline) error(stage string, status int32) int32 {
	d.err = fmt.Sprintf("VideoToolbox %s failed (OSStatus %d)", stage, status)
	return avExternal
}
func (d *nativeVideoPipeline) detail() string { return d.err }
func (d *nativeVideoPipeline) createSession() int32 {
	callback := outputCallback{videoOutput, d.token}
	if status := vt.Create(0, d.format, d.specification, d.attributes, &callback, &d.session); status != 0 {
		return d.error("session creation", status)
	}
	vt.Property(d.session, vtKeys.Realtime, apple.True)
	return 0
}
func (d *nativeVideoPipeline) timestamp(value int64) cmTime {
	if value == avNoTimestamp {
		return cmTime{}
	}
	return cmTime{Value: value * int64(d.timeBase.Num), Scale: d.timeBase.Den, Flags: 1}
}

// Only HEVC RASL pictures before the random-access point can lack references.
func (d *nativeVideoPipeline) discard(status int32, pts int64) bool {
	return d.parameters.CodecId == avHEVC && status == -17694 && d.randomAccessPTS != avNoTimestamp && pts < d.randomAccessPTS
}
func (d *nativeVideoPipeline) send(packet *avPacket) int32 {
	pts := packet.Pts
	if pts == avNoTimestamp {
		pts = packet.Dts
	}
	if pts == avNoTimestamp {
		pts = d.nextPTS
	}
	if d.randomAccessPTS == avNoTimestamp && packet.Flags&avPacketKey != 0 {
		d.randomAccessPTS = pts
	}
	d.nextPTS = pts + max(1, packet.Duration)
	var block, sample uintptr
	status := coreMedia.BlockCreate(0, 0, uintptr(packet.Size), 0, 0, 0, uintptr(packet.Size), 0, &block)
	if status == 0 {
		status = coreMedia.BlockCopy(packet.Data, block, 0, uintptr(packet.Size))
	}
	size := uintptr(packet.Size)
	timing := sampleTiming{Duration: d.timestamp(packet.Duration), Presentation: d.timestamp(pts), Decode: d.timestamp(packet.Dts)}
	if status == 0 {
		status = coreMedia.SampleCreate(0, block, d.format, 1, 1, &timing, 1, &size, &sample)
	}
	if block != 0 {
		apple.Core.Release(block)
	}
	if status != 0 {
		return d.error("sample creation", status)
	}
	d.mu.Lock()
	d.nextToken++
	picture := &videoPicture{token: d.nextToken, pts: pts}
	i := sort.Search(len(d.pictures), func(i int) bool { return d.pictures[i].pts > pts })
	d.pictures = append(d.pictures, nil)
	copy(d.pictures[i+1:], d.pictures[i:])
	d.pictures[i] = picture
	d.pending[picture.token] = picture
	d.lastDTS = packet.Dts
	d.mu.Unlock()
	status = vt.Decode(d.session, sample, 1, picture.token, nil)
	apple.Core.Release(sample)
	if status != 0 {
		// VideoToolbox guarantees no output callback after an immediate error.
		d.mu.Lock()
		picture.status, picture.done = status, true
		d.mu.Unlock()
		if d.discard(status, pts) {
			return 0
		}
		return d.error("frame submission", status)
	}
	return 0
}
func (d *nativeVideoPipeline) receive(frame *avFrame) int32 {
	for {
		if d.ctx.Err() != nil {
			return avExit
		}
		d.mu.Lock()
		if len(d.pictures) == 0 {
			draining := d.draining
			d.mu.Unlock()
			if draining {
				return avEOF
			}
			return avAgain
		}
		picture := d.pictures[0]
		// DTS bounds the PTS of pictures not submitted yet, with a bounded window.
		if !d.draining && len(d.pictures) < 32 && (len(d.pictures) < 8 || d.lastDTS != avNoTimestamp && picture.pts > d.lastDTS) {
			d.mu.Unlock()
			return avAgain
		}
		if !picture.done {
			d.mu.Unlock()
			select {
			case <-d.ctx.Done():
				return avExit
			case <-d.changed:
			}
			continue
		}
		d.pictures[0] = nil
		d.pictures = d.pictures[1:]
		delete(d.pending, picture.token)
		d.mu.Unlock()
		if picture.status != 0 {
			if picture.image != 0 {
				apple.Core.Release(picture.image)
			}
			if d.discard(picture.status, picture.pts) {
				continue
			}
			return d.error("output callback", picture.status)
		}
		if picture.image == 0 {
			continue
		}
		p := d.parameters
		frame.Format = av.util.PixelFormat("videotoolbox_vld")
		frame.Width, frame.Height = p.Width, p.Height
		frame.Pts, frame.BestEffortTimestamp = picture.pts, picture.pts
		frame.ColorRange, frame.Colorspace, frame.ColorPrimaries, frame.ColorTrc, frame.ChromaLocation = p.ColorRange, p.ColorSpace, p.ColorPrimaries, p.ColorTrc, p.ChromaLocation
		frame.HwFramesCtx = av.util.BufferRef(d.frames)
		if frame.HwFramesCtx == nil {
			apple.Core.Release(picture.image)
			av.util.FrameUnref(frame)
			return avNoMemory
		}
		frame.Buf[0] = vtFrames.Buffer(picture.image, 0, releaseVideoImage, 0, 0)
		if frame.Buf[0] == nil {
			apple.Core.Release(picture.image)
			av.util.FrameUnref(frame)
			return avNoMemory
		}
		frame.Data[3] = (*byte)(native.Pointer(picture.image))
		ret := vtFrames.Attachments(0, picture.image, frame)
		if ret < 0 {
			av.util.FrameUnref(frame)
		}
		return ret
	}
}
func (d *nativeVideoPipeline) drain() {
	d.mu.Lock()
	d.draining = true
	d.mu.Unlock()
	vt.Finish(d.session)
}
func (d *nativeVideoPipeline) clear() {
	if d.session != 0 {
		vt.Wait(d.session)
		vt.Invalidate(d.session)
		apple.Core.Release(d.session)
		d.session = 0
	}
	d.mu.Lock()
	for _, p := range d.pictures {
		if p.image != 0 {
			apple.Core.Release(p.image)
		}
	}
	d.pictures = nil
	clear(d.pending)
	d.draining = false
	d.lastDTS, d.nextPTS, d.randomAccessPTS = avNoTimestamp, 0, avNoTimestamp
	d.mu.Unlock()
}
func (d *nativeVideoPipeline) reset() int32 { d.clear(); d.err = ""; return d.createSession() }
func (d *nativeVideoPipeline) close() {
	d.clear()
	videoPipelines.Delete(d.token)
	for _, ref := range []uintptr{d.format, d.attributes, d.specification} {
		if ref != 0 {
			apple.Core.Release(ref)
		}
	}
	av.util.BufferUnref(&d.frames)
}
