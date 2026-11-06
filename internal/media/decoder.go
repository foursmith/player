package media

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"runtime"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/foursmith/player/internal/native"
)

// Only integer tokens cross the callback boundary. The registry owns Go state
// until the format, codecs and asynchronous native pipeline have all closed.
type cancellation struct {
	value          atomic.Int32
	hardwareFormat atomic.Int32
	token          uintptr
	input          *httpInput
}

var callbackTokens atomic.Uint64
var callbacks sync.Map
var interruptCallback = purego.NewCallback(mediaInterrupt)
var hardwareCallback = purego.NewCallback(hardwarePixelFormat)

// Windows callback trampolines require a pointer-sized result. FFmpeg reads
// the low 32 bits as C int; the native round-trip tests cover that boundary.
func mediaInterrupt(token uintptr) uintptr {
	if value, ok := callbacks.Load(token); ok {
		return uintptr(value.(*cancellation).value.Load())
	}
	return 1
}

func hardwarePixelFormat(ctx *avCodecContext, formats *int32) uintptr {
	value, ok := callbacks.Load(ctx.Opaque)
	if !ok {
		return ^uintptr(0)
	}
	cancel := value.(*cancellation)
	for p := formats; *p != -1; p = (*int32)(unsafe.Add(unsafe.Pointer(p), 4)) {
		if *p == cancel.hardwareFormat.Load() {
			return uintptr(uint32(*p))
		}
	}
	// A rejected hardware session stays in software across subsequent seeks.
	cancel.hardwareFormat.Store(-1)
	for p := formats; *p != -1; p = (*int32)(unsafe.Add(unsafe.Pointer(p), 4)) {
		if desc := av.util.PixelDesc(*p); desc != nil && desc.Flags&avHWPixelFlag == 0 {
			return uintptr(uint32(*p))
		}
	}
	return ^uintptr(0)
}

func newCancellation() *cancellation {
	c := &cancellation{token: uintptr(callbackTokens.Add(1))}
	c.hardwareFormat.Store(-1)
	callbacks.Store(c.token, c)
	return c
}
func (c *cancellation) cancel() { c.value.Store(1) }
func (c *cancellation) close()  { callbacks.Delete(c.token) }

type videoPipeline interface {
	send(*avPacket) int32
	receive(*avFrame) int32
	reset() int32
	drain()
	close()
	detail() string
}

type Decoder struct {
	ctx                                                   context.Context
	cancellation                                          *cancellation
	stop                                                  func() bool
	format                                                *avFormat
	video, audio                                          *avCodecContext
	pipeline                                              videoPipeline
	hardware                                              *avBuffer
	hardwareName                                          string
	videoStream, audioStream                              int32
	packet                                                *avPacket
	packets                                               []*avPacket
	packetBytes                                           int
	decoded, sound, filtered                              *avFrame
	filter, source, sink                                  unsafe.Pointer
	output                                                unsafe.Pointer
	audioInitialized                                      bool
	prerollDiscard                                        bool
	draining, inputEOF, audioEOF                          bool
	rate, audioStart, audioSamples, seekTarget, startTime float64
	chapters                                              []Chapter
}
type Frame struct {
	ptr    *avFrame
	pts    float64
	serial uint64
}

var frameSerial atomic.Uint64

type Source struct {
	URL   string
	Start float64
}
type Metadata struct {
	Width, Height       int
	Duration, FrameRate float64
	Decoder             string
	Spherical, Stereo   string
	Chapters            []Chapter
	HasChapterArtwork   bool
}

func Version() string {
	if err := loadMedia(); err != nil {
		return "unavailable: " + err.Error()
	}
	return av.util.VersionInfo()
}
func Open(ctx context.Context, source Source, outputAudio bool) (*Decoder, error) {
	return openContext(ctx, source, outputAudio, false)
}
func openContext(ctx context.Context, source Source, outputAudio, analysis bool) (_ *Decoder, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := loadMedia(); err != nil {
		return nil, err
	}
	d := &Decoder{ctx: ctx, cancellation: newCancellation(), videoStream: -1, audioStream: -1, rate: 1, audioStart: math.NaN()}
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
		return nil, errors.New("cannot allocate media format")
	}
	d.format.InterruptCallback = avInterrupt{interruptCallback, d.cancellation.token}
	var options unsafe.Pointer
	av.util.DictSet(&options, "rw_timeout", "10000000", 0)
	if isHTTP(source.URL) {
		if err := d.openHTTP(source.URL); err != nil {
			av.util.DictFree(&options)
			return nil, err
		}
	}
	ret := av.format.Open(&d.format, source.URL, nil, &options)
	av.util.DictFree(&options)
	if ret < 0 {
		return nil, mediaError(ret)
	}
	if ret = av.format.FindInfo(d.format, nil); ret < 0 {
		return nil, mediaError(ret)
	}
	if d.format.StartTime != avNoTimestamp {
		d.startTime = float64(d.format.StartTime) / 1e6
	}
	d.chapters = readChapters(d.format, d.startTime)
	d.videoStream = av.format.Best(d.format, avVideo, -1, -1, nil, 0)
	if d.videoStream < 0 {
		return nil, mediaError(d.videoStream)
	}
	if outputAudio {
		d.audioStream = av.format.Best(d.format, avAudio, -1, -1, nil, 0)
	}
	// Silent readers must not fetch distant audio chunks during precise seeks.
	// Playback also skips subtitle and artwork tracks read by separate owners.
	for _, stream := range unsafe.Slice(d.format.Streams, int(d.format.NbStreams)) {
		if stream.Index != d.videoStream && stream.Index != d.audioStream {
			stream.Discard = avDiscardAll
		}
	}
	stream := d.stream(d.videoStream)
	hardware := !analysis || stream.Codecpar.CodecId != avH264
	if d.video, err = d.openCodec(d.videoStream, hardware); err != nil {
		return nil, err
	}
	if outputAudio {
		d.pipeline = newVideoPipeline(stream, d.hardware, ctx)
	}
	if runtime.GOOS == "darwin" && outputAudio && d.hardware != nil && d.pipeline == nil && (d.video.CodecId == avH264 || d.video.CodecId == avHEVC) {
		// Preserve the one-time software fallback for rejected native sessions.
		av.codec.Free(&d.video)
		av.util.BufferUnref(&d.hardware)
		d.cancellation.hardwareFormat.Store(-1)
		if d.video, err = d.openCodec(d.videoStream, false); err != nil {
			return nil, err
		}
	}
	if d.audioStream >= 0 {
		if d.audio, err = d.openCodec(d.audioStream, false); err != nil {
			return nil, err
		}
		if err = loadAudio(); err != nil {
			return nil, err
		}
		if !av.sdl.Init(0x10) {
			return nil, errors.New(av.sdl.Error())
		}
		d.audioInitialized = true
		// SDL_AUDIO_F32LE, stereo, 48kHz. All supported targets are little endian.
		spec := sdlSpec{0x8120, 2, 48000}
		d.output = av.sdl.Open(0xffffffff, &spec, 0, 0)
		if d.output == nil {
			return nil, errors.New(av.sdl.Error())
		}
		if err = d.audioFilter(); err != nil {
			return nil, err
		}
	}
	d.packet = av.codec.PacketAlloc()
	d.decoded, d.sound, d.filtered = av.util.FrameAlloc(), av.util.FrameAlloc(), av.util.FrameAlloc()
	if d.packet == nil || d.decoded == nil || d.sound == nil || d.filtered == nil {
		return nil, errors.New("cannot allocate media buffers")
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	return d, nil
}
func (d *Decoder) stream(index int32) *avStream {
	return unsafe.Slice(d.format.Streams, int(d.format.NbStreams))[index]
}
func (d *Decoder) openCodec(index int32, hardware bool) (*avCodecContext, error) {
	s := d.stream(index)
	codec := av.codec.FindDecoder(s.Codecpar.CodecId)
	if codec == nil {
		return nil, errors.New("FFmpeg decoder unavailable")
	}
	ctx := av.codec.Alloc(codec)
	if ctx == nil {
		return nil, errors.New("cannot allocate media codec")
	}
	fail := func(code int32) (*avCodecContext, error) { av.codec.Free(&ctx); return nil, mediaError(code) }
	if ret := av.codec.Parameters(ctx, s.Codecpar); ret < 0 {
		return fail(ret)
	}
	ctx.PktTimebase = s.TimeBase
	if hardware {
		name := "vaapi"
		switch runtime.GOOS {
		case "darwin":
			name = "videotoolbox"
		case "windows":
			name = "d3d11va"
		}
		deviceType := av.util.DeviceType(name)
		for i := int32(0); ; i++ {
			config := av.codec.Hardware(codec, i)
			if config == nil {
				break
			}
			if config.DeviceType != deviceType || config.Methods&1 == 0 {
				continue
			}
			if av.util.DeviceCreate(&d.hardware, deviceType, nil, nil, 0) < 0 {
				break
			}
			d.hardwareName = name
			d.cancellation.hardwareFormat.Store(config.PixFmt)
			ctx.HwDeviceCtx = av.util.BufferRef(d.hardware)
			ctx.Opaque = d.cancellation.token
			ctx.GetFormat = hardwareCallback
			break
		}
	}
	var options unsafe.Pointer
	if index == d.videoStream {
		// libavcodec defaults to one decoding thread. Let it choose a suitable
		// count for software playback, including driver/profile fallbacks.
		av.util.DictSet(&options, "threads", "0", 0)
		if d.hardwareName == "d3d11va" && ctx.HwDeviceCtx != nil {
			// Frame threads add a picture of delay per thread before first
			// output and after every flush. D3D11 already decodes on the GPU;
			// slice threads avoid that latency and still allow a software
			// get_format fallback to parallelize supported slices/WPP.
			av.util.DictSet(&options, "thread_type", "slice", 0)
		}
	}
	ret := av.codec.Open(ctx, codec, unsafe.Pointer(&options))
	av.util.DictFree(&options)
	if ret < 0 {
		if hardware && d.hardware != nil {
			// Drivers may advertise a codec but reject its profile, bit depth or
			// resolution when opening it. Release the rejected device before
			// retrying once with a fresh software-only codec context.
			av.codec.Free(&ctx)
			av.util.BufferUnref(&d.hardware)
			d.cancellation.hardwareFormat.Store(-1)
			d.hardwareName = ""
			return d.openCodec(index, false)
		}
		return fail(ret)
	}
	return ctx, nil
}
func (d *Decoder) failure(code int32) error {
	if err := d.ctx.Err(); err != nil {
		return err
	}
	return mediaError(code)
}
func (d *Decoder) videoFailure(code int32) error {
	if err := d.ctx.Err(); err != nil {
		return err
	}
	if d.pipeline != nil && d.pipeline.detail() != "" {
		return errors.New(d.pipeline.detail())
	}
	return mediaError(code)
}
func (d *Decoder) Metadata() Metadata {
	duration := 0.0
	if d.format.Duration != avNoTimestamp {
		duration = float64(d.format.Duration) / 1e6
	}
	fps := ratio(av.format.Rate(d.format, d.stream(d.videoStream), nil))
	if fps <= 0 {
		fps = 30
	}
	name := native.String(d.video.Codec.Name)
	if d.pipeline != nil {
		name = "videotoolbox-async"
	} else if d.hardware != nil && d.cancellation.hardwareFormat.Load() != -1 {
		name = d.hardwareName
	}
	return Metadata{Width: int(d.video.Width), Height: int(d.video.Height), Duration: duration, FrameRate: fps, Decoder: name, Spherical: d.spherical(), Stereo: d.stereo(), Chapters: d.chapters, HasChapterArtwork: chapterArtworkStream(d.format) != nil}
}

// Only the player uses this yield signal; it keeps the decoder alive so the
// latest seek can flush it, rather than finishing an obsolete long GOP first.
var errSeekPending = errors.New("decode superseded by a pending seek")

func (d *Decoder) Next() (*Frame, error) { return d.next(nil) }

func (d *Decoder) next(interrupted func() bool) (*Frame, error) {
	for {
		if err := d.ctx.Err(); err != nil {
			return nil, err
		}
		if interrupted != nil && interrupted() {
			return nil, errSeekPending
		}
		var ret int32
		if d.pipeline != nil {
			ret = d.pipeline.receive(d.decoded)
		} else {
			ret = av.codec.Receive(d.video, d.decoded)
		}
		if ret >= 0 {
			pts := 0.0
			if d.decoded.BestEffortTimestamp != avNoTimestamp {
				pts = float64(d.decoded.BestEffortTimestamp)*ratio(d.stream(d.videoStream).TimeBase) - d.startTime
			}
			if pts+.0001 < d.seekTarget {
				av.util.FrameUnref(d.decoded)
				continue
			}
			frame := av.util.FrameAlloc()
			if frame == nil {
				return nil, errors.New("cannot allocate video frame")
			}
			av.util.FrameMove(frame, d.decoded)
			return &Frame{frame, pts, frameSerial.Add(1)}, nil
		}
		if ret == avEOF {
			return nil, io.EOF
		}
		if ret != avAgain {
			return nil, d.videoFailure(ret)
		}
		if d.draining {
			return nil, io.EOF
		}
		ret = d.readPacket()
		if ret == avEOF {
			d.draining = true
			if d.pipeline != nil {
				d.pipeline.drain()
			} else {
				av.codec.Send(d.video, nil)
			}
			if err := d.finishAudio(); err != nil {
				return nil, err
			}
			continue
		}
		if ret < 0 {
			return nil, d.failure(ret)
		}
		switch d.packet.StreamIndex {
		case d.videoStream:
			if d.pipeline != nil {
				ret = d.pipeline.send(d.packet)
			} else {
				if err := d.prepareVideoPacket(d.packet); err != nil {
					av.codec.PacketUnref(d.packet)
					return nil, err
				}
				ret = av.codec.Send(d.video, d.packet)
			}
			if ret < 0 && ret != avAgain {
				av.codec.PacketUnref(d.packet)
				return nil, d.videoFailure(ret)
			}
		case d.audioStream:
			ret = av.codec.Send(d.audio, d.packet)
			if ret >= 0 {
				if err := d.decodeSound(); err != nil {
					av.codec.PacketUnref(d.packet)
					return nil, err
				}
			}
		default:
			ret = 0
		}
		av.codec.PacketUnref(d.packet)
		if ret < 0 && ret != avAgain {
			return nil, d.failure(ret)
		}
	}
}

func (d *Decoder) prepareVideoPacket(packet *avPacket) error {
	// Decode every reference picture, but let FFmpeg skip non-reference
	// preroll pictures that can never be displayed. Use presentation time,
	// not decode time: reordered B pictures at the target must remain intact.
	// Missing timestamps and the final 50ms use normal decoding as well.
	discard := d.hardwareName == "d3d11va" && d.cancellation.hardwareFormat.Load() != -1 &&
		(d.video.CodecId == avH264 || d.video.CodecId == avHEVC) &&
		d.seekTarget > 0 && packet.Pts != avNoTimestamp &&
		float64(packet.Pts)*ratio(d.stream(d.videoStream).TimeBase)-d.startTime < d.seekTarget-.05
	return d.setPrerollDiscard(discard)
}

func (d *Decoder) setPrerollDiscard(discard bool) error {
	if discard == d.prerollDiscard {
		return nil
	}
	value := int64(0) // AVDISCARD_DEFAULT
	if discard {
		value = 8 // AVDISCARD_NONREF: reference B pictures are still decoded.
	}
	if ret := av.util.OptionInt(unsafe.Pointer(d.video), "skip_frame", value, 0); ret < 0 {
		return d.failure(ret)
	}
	d.prerollDiscard = discard
	return nil
}
func (d *Decoder) clearPackets() {
	for _, packet := range d.packets {
		av.codec.PacketFree(&packet)
	}
	d.packets = nil
	d.packetBytes = 0
}
func (d *Decoder) readPacket() int32 {
	if len(d.packets) != 0 {
		packet := d.packets[0]
		d.packets[0] = nil
		d.packets = d.packets[1:]
		d.packetBytes -= int(packet.Size)
		av.codec.PacketMove(d.packet, packet)
		av.codec.PacketFree(&packet)
		return 0
	}
	if d.inputEOF {
		return avEOF
	}
	ret := av.format.Read(d.format, d.packet)
	if ret == avEOF {
		d.inputEOF = true
	}
	return ret
}
func (d *Decoder) Seek(seconds float64) error { return d.seek(seconds, true) }

// Inexact seeking is reserved for analysis thumbnails; playback decodes through
// the exact requested timestamp, including open-GOP and reordered pictures.
func (d *Decoder) seek(seconds float64, precise bool) error {
	if err := d.ctx.Err(); err != nil {
		return err
	}
	target := int64((seconds + d.startTime) * 1e6)
	if ret := av.format.Seek(d.format, -1, math.MinInt64, target, target, 0); ret < 0 {
		return d.failure(ret)
	}
	d.clearPackets()
	av.codec.PacketUnref(d.packet)
	if err := d.setPrerollDiscard(false); err != nil {
		return err
	}
	av.codec.Flush(d.video)
	if d.pipeline != nil {
		if ret := d.pipeline.reset(); ret < 0 {
			return d.videoFailure(ret)
		}
	}
	if d.audio != nil {
		av.codec.Flush(d.audio)
	}
	if d.output != nil {
		av.sdl.Clear(d.output)
	}
	d.audioSamples, d.audioStart, d.seekTarget = 0, math.NaN(), 0
	if precise {
		d.seekTarget = seconds
	}
	d.draining, d.inputEOF, d.audioEOF = false, false, false
	return d.audioFilter()
}
func (d *Decoder) SetRate(rate float64) error { d.rate = rate; return d.audioFilter() }
func (d *Decoder) Pause(paused bool) {
	if d.output != nil {
		if paused {
			av.sdl.Pause(d.output)
		} else {
			av.sdl.Resume(d.output)
		}
	}
}
func (d *Decoder) Volume(volume float64) {
	if d.output != nil {
		av.sdl.Gain(d.output, float32(volume))
	}
}
func (d *Decoder) Clock() float64 {
	if d.output == nil || math.IsNaN(d.audioStart) || d.audioSamples <= 0 {
		return math.NaN()
	}
	queued := av.sdl.Queued(d.output)
	if d.inputEOF && queued <= 0 {
		return math.NaN()
	}
	return d.audioStart + (d.audioSamples-math.Max(0, float64(queued)/8))/48000*d.rate
}
func (d *Decoder) bufferedAudioEnd() float64 {
	if d.output == nil || math.IsNaN(d.audioStart) {
		return math.NaN()
	}
	return d.audioStart + d.audioSamples/48000*d.rate
}
func (d *Decoder) Close() {
	if d.cancellation == nil {
		return
	}
	d.stop()
	if d.output != nil {
		av.sdl.Destroy(d.output)
		d.output = nil
	}
	if d.audioInitialized {
		av.sdl.Quit(0x10)
		d.audioInitialized = false
	}
	if d.pipeline != nil {
		d.pipeline.close()
		d.pipeline = nil
	}
	d.clearPackets()
	av.filter.Free(&d.filter)
	av.util.FrameFree(&d.decoded)
	av.util.FrameFree(&d.sound)
	av.util.FrameFree(&d.filtered)
	av.codec.PacketFree(&d.packet)
	av.codec.Free(&d.video)
	av.codec.Free(&d.audio)
	av.util.BufferUnref(&d.hardware)
	av.format.Close(&d.format)
	if d.cancellation.input != nil {
		d.cancellation.input.close()
	}
	d.cancellation.close()
	d.cancellation = nil
}
func (f *Frame) Clone() *Frame {
	if f == nil || f.ptr == nil {
		return nil
	}
	ptr := av.util.FrameClone(f.ptr)
	if ptr == nil {
		return nil
	}
	return &Frame{ptr, f.pts, f.serial}
}
func (f *Frame) Close() {
	if f != nil && f.ptr != nil {
		av.util.FrameFree(&f.ptr)
	}
}
func (f *Frame) PTS() float64     { return f.pts }
func (f *Frame) ID() uint64       { return f.serial }
func (f *Frame) Size() (int, int) { return int(f.ptr.Width), int(f.ptr.Height) }
func (f *Frame) Native() unsafe.Pointer {
	if runtime.GOOS == "darwin" && f.ptr.Format == av.util.PixelFormat("videotoolbox_vld") {
		return unsafe.Pointer(f.ptr.Data[3])
	}
	return nil
}
func (d *Decoder) sideData(kind int32) *avSideData {
	p := d.stream(d.videoStream).Codecpar
	return av.codec.PacketSideData(p.CodedSideData, p.NbCodedSideData, kind)
}
func (d *Decoder) spherical() string {
	sd := d.sideData(avSphericalData)
	if sd == nil || sd.Size < unsafe.Sizeof(avSpherical{}) {
		return ""
	}
	mapping := (*avSpherical)(unsafe.Pointer(sd.Data))
	switch mapping.Projection {
	case avEquirectangular:
		return "equirectangular"
	case avSphericalTile:
		if mapping.BoundTop == 0 && mapping.BoundBottom == 0 && absDiff(mapping.BoundLeft, 0x40000000) <= 16 && absDiff(mapping.BoundRight, 0x40000000) <= 16 {
			return "half-equirectangular"
		}
		return "unsupported"
	case avHalfEquirectangular:
		return "half-equirectangular"
	case avFisheye:
		return "fisheye"
	default:
		return "unsupported"
	}
}
func absDiff(a, b uint32) uint32 {
	if a > b {
		return a - b
	}
	return b - a
}
func (d *Decoder) stereo() string {
	sd := d.sideData(avStereoData)
	if sd == nil || sd.Size < unsafe.Sizeof(avStereo{}) {
		return ""
	}
	switch (*avStereo)(unsafe.Pointer(sd.Data)).Type {
	case avMono:
		return "mono"
	case avSBS:
		return "sbs"
	case avTB:
		return "tb"
	default:
		return "unsupported"
	}
}

func (d *Decoder) audioFilter() error {
	av.filter.Free(&d.filter)
	d.source, d.sink = nil, nil
	if d.audio == nil {
		return nil
	}
	d.filter = av.filter.Alloc()
	if d.filter == nil {
		return errors.New("cannot allocate audio filter")
	}
	var layout [128]byte
	av.util.LayoutName(&d.audio.ChLayout, &layout[0], uintptr(len(layout)))
	args := fmt.Sprintf("time_base=1/%d:sample_rate=%d:sample_fmt=%s:channel_layout=%s", d.audio.SampleRate, d.audio.SampleRate, av.util.SampleName(d.audio.SampleFmt), native.String(&layout[0]))
	argBytes := append([]byte(args), 0)
	if ret := av.filter.Create(&d.source, av.filter.Find("abuffer"), "in", &argBytes[0], nil, d.filter); ret < 0 {
		return d.failure(ret)
	}
	if ret := av.filter.Create(&d.sink, av.filter.Find("abuffersink"), "out", nil, nil, d.filter); ret < 0 {
		return d.failure(ret)
	}
	inputs, outputs := av.filter.AllocIO(), av.filter.AllocIO()
	defer av.filter.FreeIO(&inputs)
	defer av.filter.FreeIO(&outputs)
	if inputs == nil || outputs == nil {
		return errors.New("cannot allocate audio filter endpoints")
	}
	outputs.Name, outputs.FilterCtx = av.util.Strdup("in"), d.source
	inputs.Name, inputs.FilterCtx = av.util.Strdup("out"), d.sink
	filters := fmt.Sprintf("atempo=%.8f,aformat=sample_fmts=flt:sample_rates=48000:channel_layouts=stereo", d.rate)
	if ret := av.filter.Parse(d.filter, filters, &inputs, &outputs, nil); ret < 0 {
		return d.failure(ret)
	}
	if ret := av.filter.Configure(d.filter, nil); ret < 0 {
		return d.failure(ret)
	}
	return nil
}
func (d *Decoder) filteredAudio() error {
	for {
		ret := av.filter.Get(d.sink, d.filtered)
		if ret < 0 {
			if ret == avAgain || ret == avEOF {
				return nil
			}
			return d.failure(ret)
		}
		ok := av.sdl.Put(d.output, d.filtered.Data[0], d.filtered.NbSamples*8)
		d.audioSamples += float64(d.filtered.NbSamples)
		av.util.FrameUnref(d.filtered)
		if !ok {
			return errors.New(av.sdl.Error())
		}
	}
}
func (d *Decoder) decodeSound() error {
	if d.audio == nil {
		return nil
	}
	for {
		ret := av.codec.Receive(d.audio, d.sound)
		if ret < 0 {
			if ret == avAgain || ret == avEOF {
				return nil
			}
			return d.failure(ret)
		}
		pts := d.seekTarget
		if d.sound.BestEffortTimestamp != avNoTimestamp {
			pts = float64(d.sound.BestEffortTimestamp)*ratio(d.stream(d.audioStream).TimeBase) - d.startTime
		}
		end := pts + float64(d.sound.NbSamples)/float64(d.sound.SampleRate)
		if end < d.seekTarget {
			av.util.FrameUnref(d.sound)
			continue
		}
		if math.IsNaN(d.audioStart) {
			d.audioStart = pts
		}
		d.sound.Pts = av.util.Rescale(d.sound.BestEffortTimestamp, d.stream(d.audioStream).TimeBase, avRational{1, d.sound.SampleRate})
		ret = av.filter.Add(d.source, d.sound, avKeepRef)
		av.util.FrameUnref(d.sound)
		if ret < 0 {
			return d.failure(ret)
		}
		if err := d.filteredAudio(); err != nil {
			return err
		}
	}
}
func (d *Decoder) finishAudio() error {
	if d.audio == nil || d.audioEOF {
		return nil
	}
	d.audioEOF = true
	if ret := av.codec.Send(d.audio, nil); ret < 0 && ret != avEOF {
		return d.failure(ret)
	}
	if err := d.decodeSound(); err != nil {
		return err
	}
	if ret := av.filter.Add(d.source, nil, 0); ret < 0 && ret != avEOF {
		return d.failure(ret)
	}
	return d.filteredAudio()
}

// Demux ahead while retaining compressed video packets, bounded at 32 MiB.
// This supplies audio through long interleaving gaps without retaining 8K frames.
func (d *Decoder) bufferAudio(seconds float64, interrupted func() bool) error {
	if err := d.ctx.Err(); err != nil {
		return err
	}
	if d.output == nil || d.inputEOF {
		return nil
	}
	for d.packetBytes < 32*1024*1024 && float64(av.sdl.Queued(d.output)) < seconds*48000*8 {
		if err := d.ctx.Err(); err != nil {
			return err
		}
		if interrupted != nil && interrupted() {
			return errSeekPending
		}
		ret := av.format.Read(d.format, d.packet)
		if ret == avEOF {
			d.inputEOF = true
			return d.finishAudio()
		}
		if ret < 0 {
			return d.failure(ret)
		}
		if d.packet.StreamIndex == d.videoStream {
			packet := av.codec.PacketAlloc()
			if packet == nil {
				av.codec.PacketUnref(d.packet)
				return errors.New("cannot allocate buffered video packet")
			}
			d.packetBytes += int(d.packet.Size)
			av.codec.PacketMove(packet, d.packet)
			d.packets = append(d.packets, packet)
		} else {
			if d.packet.StreamIndex == d.audioStream {
				ret = av.codec.Send(d.audio, d.packet)
				if ret >= 0 {
					if err := d.decodeSound(); err != nil {
						av.codec.PacketUnref(d.packet)
						return err
					}
				}
			}
			av.codec.PacketUnref(d.packet)
			if ret < 0 {
				return d.failure(ret)
			}
		}
	}
	return nil
}
