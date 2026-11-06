package media

import (
	"fmt"
	"runtime"
	"sync"
	"unsafe"

	"github.com/foursmith/player/internal/native"
)

type utilAPI struct {
	Version       func() uint32
	VersionInfo   func() string
	Error         func(int32, *byte, uintptr) int32
	FrameAlloc    func() *avFrame
	FrameFree     func(**avFrame)
	FrameUnref    func(*avFrame)
	FrameMove     func(*avFrame, *avFrame)
	FrameClone    func(*avFrame) *avFrame
	BufferRef     func(*avBuffer) *avBuffer
	BufferUnref   func(**avBuffer)
	DictSet       func(*unsafe.Pointer, string, string, int32) int32
	DictFree      func(*unsafe.Pointer)
	DeviceType    func(string) int32
	DeviceCreate  func(**avBuffer, int32, *byte, unsafe.Pointer, int32) int32
	PixelDesc     func(int32) *avPixelDesc
	PixelFormat   func(string) int32
	TransferFrame func(*avFrame, *avFrame, int32) int32
	LayoutName    func(*avChannelLayout, *byte, uintptr) int32
	SampleName    func(int32) string
	Rescale       func(int64, avRational, avRational) int64
	Strdup        func(string) *byte
	OptionInt     func(unsafe.Pointer, string, int64, int32) int32
}

type codecAPI struct {
	Version        func() uint32
	FindDecoder    func(int32) *avCodec
	Alloc          func(*avCodec) *avCodecContext
	Parameters     func(*avCodecContext, *avParameters) int32
	Open           func(*avCodecContext, *avCodec, unsafe.Pointer) int32
	Free           func(**avCodecContext)
	Hardware       func(*avCodec, int32) *avHWConfig
	Receive        func(*avCodecContext, *avFrame) int32
	Send           func(*avCodecContext, *avPacket) int32
	Flush          func(*avCodecContext)
	PacketAlloc    func() *avPacket
	PacketFree     func(**avPacket)
	PacketUnref    func(*avPacket)
	PacketMove     func(*avPacket, *avPacket)
	PacketSideData func(*avSideData, int32, int32) *avSideData
}

type formatAPI struct {
	Version  func() uint32
	Alloc    func() *avFormat
	Open     func(**avFormat, string, unsafe.Pointer, *unsafe.Pointer) int32
	FindInfo func(*avFormat, unsafe.Pointer) int32
	Best     func(*avFormat, int32, int32, int32, unsafe.Pointer, int32) int32
	Read     func(*avFormat, *avPacket) int32
	Close    func(**avFormat)
	Seek     func(*avFormat, int32, int64, int64, int64, int32) int32
	Rate     func(*avFormat, *avStream, *avFrame) avRational
}

type filterAPI struct {
	Version   func() uint32
	Alloc     func() unsafe.Pointer
	Free      func(*unsafe.Pointer)
	Find      func(string) unsafe.Pointer
	Create    func(*unsafe.Pointer, unsafe.Pointer, string, *byte, unsafe.Pointer, unsafe.Pointer) int32
	AllocIO   func() *avFilterInOut
	FreeIO    func(**avFilterInOut)
	Parse     func(unsafe.Pointer, string, **avFilterInOut, **avFilterInOut, unsafe.Pointer) int32
	Configure func(unsafe.Pointer, unsafe.Pointer) int32
	Add       func(unsafe.Pointer, *avFrame, int32) int32
	Get       func(unsafe.Pointer, *avFrame) int32
}

type scaleAPI struct {
	Version      func() uint32
	Create       func(int32, int32, int32, int32, int32, int32, int32, unsafe.Pointer, unsafe.Pointer, unsafe.Pointer) unsafe.Pointer
	Scale        func(unsafe.Pointer, **byte, *int32, int32, int32, **byte, *int32) int32
	Free         func(unsafe.Pointer)
	Coefficients func(int32) *int32
	Colorspace   func(unsafe.Pointer, *int32, int32, *int32, int32, int32, int32, int32) int32
}

type sdlSpec struct{ Format, Channels, Frequency int32 }
type sdlAPI struct {
	Init    func(uint32) bool
	Quit    func(uint32)
	Error   func() string
	Open    func(uint32, *sdlSpec, uintptr, uintptr) unsafe.Pointer
	Destroy func(unsafe.Pointer)
	Put     func(unsafe.Pointer, *byte, int32) bool
	Queued  func(unsafe.Pointer) int32
	Clear   func(unsafe.Pointer) bool
	Pause   func(unsafe.Pointer) bool
	Resume  func(unsafe.Pointer) bool
	Gain    func(unsafe.Pointer, float32) bool
}

var av struct {
	util   utilAPI
	codec  codecAPI
	format formatAPI
	filter filterAPI
	scale  scaleAPI
	sdl    sdlAPI
}

func bindLibrary(name string, major int, api any, names ...string) error {
	handle, err := native.Open(native.HostTarget().Library(name, major))
	if err != nil {
		return err
	}
	return native.Bind(handle, api, names...)
}

var loadMedia = sync.OnceValue(func() error {
	if err := bindLibrary("avutil", 61, &av.util, "avutil_version", "av_version_info", "av_strerror", "av_frame_alloc", "av_frame_free", "av_frame_unref", "av_frame_move_ref", "av_frame_clone", "av_buffer_ref", "av_buffer_unref", "av_dict_set", "av_dict_free", "av_hwdevice_find_type_by_name", "av_hwdevice_ctx_create", "av_pix_fmt_desc_get", "av_get_pix_fmt", "av_hwframe_transfer_data", "av_channel_layout_describe", "av_get_sample_fmt_name", "av_rescale_q", "av_strdup", "av_opt_set_int"); err != nil {
		return err
	}
	if err := bindLibrary("avcodec", 63, &av.codec, "avcodec_version", "avcodec_find_decoder", "avcodec_alloc_context3", "avcodec_parameters_to_context", "avcodec_open2", "avcodec_free_context", "avcodec_get_hw_config", "avcodec_receive_frame", "avcodec_send_packet", "avcodec_flush_buffers", "av_packet_alloc", "av_packet_free", "av_packet_unref", "av_packet_move_ref", "av_packet_side_data_get"); err != nil {
		return err
	}
	if err := bindLibrary("avformat", 63, &av.format, "avformat_version", "avformat_alloc_context", "avformat_open_input", "avformat_find_stream_info", "av_find_best_stream", "av_read_frame", "avformat_close_input", "avformat_seek_file", "av_guess_frame_rate"); err != nil {
		return err
	}
	if err := bindLibrary("avfilter", 12, &av.filter, "avfilter_version", "avfilter_graph_alloc", "avfilter_graph_free", "avfilter_get_by_name", "avfilter_graph_create_filter", "avfilter_inout_alloc", "avfilter_inout_free", "avfilter_graph_parse_ptr", "avfilter_graph_config", "av_buffersrc_add_frame_flags", "av_buffersink_get_frame"); err != nil {
		return err
	}
	if err := bindLibrary("swscale", 10, &av.scale, "swscale_version", "sws_getContext", "sws_scale", "sws_freeContext", "sws_getCoefficients", "sws_setColorspaceDetails"); err != nil {
		return err
	}
	for _, lib := range []struct {
		name    string
		version func() uint32
		major   uint32
	}{{"avutil", av.util.Version, 61}, {"avcodec", av.codec.Version, 63}, {"avformat", av.format.Version, 63}, {"avfilter", av.filter.Version, 12}, {"swscale", av.scale.Version, 10}} {
		if version := lib.version(); version>>16 != lib.major {
			return fmt.Errorf("%s ABI %d is incompatible with FFmpeg 9 (expected %d)", lib.name, version>>16, lib.major)
		}
	}
	return nil
})

var loadAudio = sync.OnceValue(func() error {
	handle, err := native.Open(native.HostTarget().AudioLibrary())
	if err != nil {
		return err
	}
	return native.Bind(handle, &av.sdl, "SDL_InitSubSystem", "SDL_QuitSubSystem", "SDL_GetError", "SDL_OpenAudioDeviceStream", "SDL_DestroyAudioStream", "SDL_PutAudioStreamData", "SDL_GetAudioStreamQueued", "SDL_ClearAudioStream", "SDL_PauseAudioStreamDevice", "SDL_ResumeAudioStreamDevice", "SDL_SetAudioStreamGain")
})

func mediaError(code int32) error {
	var message [1024]byte
	av.util.Error(code, &message[0], uintptr(len(message)))
	return fmt.Errorf("FFmpeg: %s", native.String(&message[0]))
}

func ratio(q avRational) float64 {
	if q.Den == 0 {
		return 0
	}
	return float64(q.Num) / float64(q.Den)
}

// EAGAIN is an OS errno, unlike FFmpeg's portable tagged error constants.
var avAgain = func() int32 {
	if runtime.GOOS == "darwin" {
		return -35
	}
	return -11
}()
