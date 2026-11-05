#include "ffmpeg.h"
#include "video_decoder.h"
#include <libavformat/avformat.h>
#include <libavcodec/avcodec.h>
#include <libavutil/hwcontext.h>
#include <libavutil/opt.h>
#include <libavutil/pixdesc.h>
#include <libavutil/spherical.h>
#include <libavutil/stereo3d.h>
#include <libavfilter/avfilter.h>
#include <libavfilter/buffersrc.h>
#include <libavfilter/buffersink.h>
#include <libswscale/swscale.h>
#include <SDL3/SDL.h>
#include <stdatomic.h>
#include <math.h>
#include <stdio.h>
#include <stdlib.h>

typedef struct FsvrPacket {
    AVPacket *packet;
    struct FsvrPacket *next;
} FsvrPacket;
struct FsvrDecoder {
    FsvrCancel *cancel;
    AVFormatContext *format;
    AVCodecContext *video, *audio;
    FsvrVideoDecoder *video_pipeline;
    AVBufferRef *hardware;
    enum AVPixelFormat hardware_format;
    int video_stream, audio_stream, draining, audio_initialized;
    AVPacket *packet;
    FsvrPacket *video_head, *video_tail;
    size_t video_bytes;
    int input_eof, audio_eof;
    AVFrame *decoded, *sound, *filtered;
    AVFilterGraph *filter;
    AVFilterContext *source, *sink;
    SDL_AudioStream *output;
    double rate, audio_start, audio_samples, seek_target, start_time;
};
struct FsvrFrame { AVFrame *image; double pts; uint64_t serial; };
static atomic_uint_fast64_t frame_serial;
struct FsvrCancel {atomic_int value;};
FsvrCancel *fsvr_cancel_new(void){FsvrCancel *cancel=av_mallocz(sizeof(FsvrCancel));if(cancel)atomic_init(&cancel->value,0);return cancel;}
void fsvr_cancel_set(FsvrCancel *cancel){if(cancel)atomic_store(&cancel->value,1);}
void fsvr_cancel_free(FsvrCancel *cancel){av_free(cancel);}

static int fail(int code, char *error, int size) {
    if (error && size > 0) av_strerror(code, error, size);
    return code;
}
static int video_fail(FsvrDecoder *d, int code, char *error, int size) {
    const char *detail = fsvr_video_error(d->video_pipeline);
    if (detail[0]) {
        if (error && size > 0) snprintf(error, size, "%s", detail);
        return code;
    }
    return fail(code, error, size);
}
static int interrupt_cb(void *opaque) {
    FsvrDecoder *d=opaque;return d->cancel&&atomic_load(&d->cancel->value);
}
static int probe_interrupt(void *opaque) {
    FsvrCancel *cancel = opaque;
    return cancel && atomic_load(&cancel->value);
}
static AVStream *probe_video(AVFormatContext *format) {
    AVStream *best = NULL;
    for (unsigned i = 0; i < format->nb_streams; i++) {
        AVStream *stream = format->streams[i];
        if (stream->codecpar->codec_type != AVMEDIA_TYPE_VIDEO || (stream->disposition & AV_DISPOSITION_ATTACHED_PIC)) continue;
        if (!best || (int64_t)stream->codecpar->width * stream->codecpar->height > (int64_t)best->codecpar->width * best->codecpar->height) best = stream;
    }
    return best;
}
int fsvr_probe(const char *url, FsvrCancel *cancel, FsvrInfo *info, char *error, int error_size) {
    AVFormatContext *format = avformat_alloc_context();
    if (!format) return fail(AVERROR(ENOMEM), error, error_size);
    format->interrupt_callback = (AVIOInterruptCB){probe_interrupt, cancel};
    AVDictionary *options = NULL;
    av_dict_set(&options, "rw_timeout", "2000000", 0);
    av_dict_set(&options, "probesize", "131072", 0);
    av_dict_set(&options, "analyzeduration", "500000", 0);
    av_dict_set(&options, "max_probe_packets", "32", 0);
    int ret = avformat_open_input(&format, url, NULL, &options);
    av_dict_free(&options);
    if (ret < 0) goto done;
    AVStream *video = probe_video(format);
    // MP4/MKV headers usually contain everything. Avoid opening decoders or
    // reading packets (especially 8K frames and malformed cover-art streams).
    if (!video || video->codecpar->width <= 0 || video->codecpar->height <= 0 || format->duration == AV_NOPTS_VALUE) {
        ret = avformat_find_stream_info(format, NULL);
        if (ret < 0) goto done;
        video = probe_video(format);
    }
    if (!video) { ret = AVERROR_STREAM_NOT_FOUND; goto done; }
    info->width = video->codecpar->width;
    info->height = video->codecpar->height;
    info->duration = format->duration != AV_NOPTS_VALUE ? (double)format->duration / AV_TIME_BASE :
        video->duration != AV_NOPTS_VALUE ? video->duration * av_q2d(video->time_base) : 0;
    info->bit_rate = format->bit_rate;
    ret = 0;
done:
    avformat_close_input(&format);
    return ret < 0 ? fail(ret, error, error_size) : ret;
}
static enum AVPixelFormat hw_format(AVCodecContext *ctx, const enum AVPixelFormat *formats) {
    FsvrDecoder *d = ctx->opaque;
    for (const enum AVPixelFormat *p = formats; *p != AV_PIX_FMT_NONE; p++)
        if (*p == d->hardware_format) return *p;
    // FFmpeg calls us again without the rejected hardware format after an
    // initialization failure. Keep software selected across subsequent seeks.
    d->hardware_format = AV_PIX_FMT_NONE;
    for (const enum AVPixelFormat *p = formats; *p != AV_PIX_FMT_NONE; p++) {
        const AVPixFmtDescriptor *desc = av_pix_fmt_desc_get(*p);
        if (desc && !(desc->flags & AV_PIX_FMT_FLAG_HWACCEL)) return *p;
    }
    return AV_PIX_FMT_NONE;
}
static int codec_open(FsvrDecoder *d, int index, AVCodecContext **out, int hardware) {
    AVStream *s = d->format->streams[index];
    const AVCodec *codec = avcodec_find_decoder(s->codecpar->codec_id);
    if (!codec) return AVERROR_DECODER_NOT_FOUND;
    AVCodecContext *ctx = avcodec_alloc_context3(codec);
    if (!ctx) return AVERROR(ENOMEM);
    int ret = avcodec_parameters_to_context(ctx, s->codecpar);
    if (ret < 0) { avcodec_free_context(&ctx); return ret; }
    ctx->pkt_timebase = s->time_base;
    if (hardware) {
#if defined(__APPLE__)
        enum AVHWDeviceType type = AV_HWDEVICE_TYPE_VIDEOTOOLBOX;
#elif defined(_WIN32)
        enum AVHWDeviceType type = AV_HWDEVICE_TYPE_D3D11VA;
#else
        enum AVHWDeviceType type = AV_HWDEVICE_TYPE_VAAPI;
#endif
        for (int i = 0;; i++) {
            const AVCodecHWConfig *config = avcodec_get_hw_config(codec, i);
            if (!config) break;
            if (config->device_type != type || !(config->methods & AV_CODEC_HW_CONFIG_METHOD_HW_DEVICE_CTX)) continue;
            if (av_hwdevice_ctx_create(&d->hardware, type, NULL, NULL, 0) < 0) break;
            d->hardware_format = config->pix_fmt;
            ctx->hw_device_ctx = av_buffer_ref(d->hardware);
            ctx->opaque = d;
            ctx->get_format = hw_format;
            break;
        }
    }
    ret = avcodec_open2(ctx, codec, NULL);
    if (ret < 0) { avcodec_free_context(&ctx); return ret; }
    *out = ctx;
    return 0;
}
static int audio_filter(FsvrDecoder *d) {
    avfilter_graph_free(&d->filter);
    d->source = d->sink = NULL;
    if (!d->audio) return 0;
    d->filter = avfilter_graph_alloc();
    if (!d->filter) return AVERROR(ENOMEM);
    char layout[128], args[512], filters[256];
    av_channel_layout_describe(&d->audio->ch_layout, layout, sizeof(layout));
    snprintf(args, sizeof(args), "time_base=1/%d:sample_rate=%d:sample_fmt=%s:channel_layout=%s", d->audio->sample_rate, d->audio->sample_rate, av_get_sample_fmt_name(d->audio->sample_fmt), layout);
    int ret = avfilter_graph_create_filter(&d->source, avfilter_get_by_name("abuffer"), "in", args, NULL, d->filter);
    if (ret < 0) return ret;
    ret = avfilter_graph_create_filter(&d->sink, avfilter_get_by_name("abuffersink"), "out", NULL, NULL, d->filter);
    if (ret < 0) return ret;
    // atempo preserves pitch, matching HTMLVideoElement.preservesPitch.
    snprintf(filters, sizeof(filters), "atempo=%.8f,aformat=sample_fmts=flt:sample_rates=48000:channel_layouts=stereo", d->rate);
    AVFilterInOut *inputs = avfilter_inout_alloc(), *outputs = avfilter_inout_alloc();
    outputs->name = av_strdup("in"); outputs->filter_ctx = d->source;
    inputs->name = av_strdup("out"); inputs->filter_ctx = d->sink;
    ret = avfilter_graph_parse_ptr(d->filter, filters, &inputs, &outputs, NULL);
    avfilter_inout_free(&inputs); avfilter_inout_free(&outputs);
    if (ret < 0) return ret;
    return avfilter_graph_config(d->filter, NULL);
}
FsvrDecoder *fsvr_open(const char *url, FsvrCancel *cancel, int output_audio, int analysis, char *error, int error_size) {
    FsvrDecoder *d = av_mallocz(sizeof(*d));
    if (!d) { fail(AVERROR(ENOMEM), error, error_size); return NULL; }
    d->video_stream = d->audio_stream = -1;
    d->hardware_format = AV_PIX_FMT_NONE;
    d->cancel=cancel;
    d->rate = 1; d->audio_start = NAN;
    d->format = avformat_alloc_context();
    if (!d->format) { fail(AVERROR(ENOMEM), error, error_size); fsvr_close(d); return NULL; }
    d->format->interrupt_callback = (AVIOInterruptCB){interrupt_cb, d};
    AVDictionary *options = NULL;
    av_dict_set(&options, "rw_timeout", "10000000", 0);
    int ret = avformat_open_input(&d->format, url, NULL, &options);
    av_dict_free(&options);
    if (ret < 0) goto failed;
    if ((ret = avformat_find_stream_info(d->format, NULL)) < 0) goto failed;
    d->start_time=d->format->start_time==AV_NOPTS_VALUE?0:(double)d->format->start_time/AV_TIME_BASE;
    d->video_stream = av_find_best_stream(d->format, AVMEDIA_TYPE_VIDEO, -1, -1, NULL, 0);
    if (d->video_stream < 0) { ret = d->video_stream; goto failed; }
    // AVC analysis needs only a few pictures and may exceed VideoToolbox's
    // supported profiles/dimensions. Keep HEVC and other formats accelerated.
    int hardware = !analysis || d->format->streams[d->video_stream]->codecpar->codec_id != AV_CODEC_ID_H264;
    if ((ret = codec_open(d, d->video_stream, &d->video, hardware)) < 0) goto failed;
    if (output_audio) d->video_pipeline = fsvr_video_open(d->format->streams[d->video_stream], d->hardware, interrupt_cb, d);
#if defined(__APPLE__)
    if (output_audio && d->hardware && !d->video_pipeline &&
        (d->video->codec_id == AV_CODEC_ID_H264 || d->video->codec_id == AV_CODEC_ID_HEVC)) {
        // A rejected native session (e.g. 8K AVC) will not become supported
        // by retrying VideoToolbox through libavcodec. Open software once.
        avcodec_free_context(&d->video);
        av_buffer_unref(&d->hardware);
        d->hardware_format = AV_PIX_FMT_NONE;
        if ((ret = codec_open(d, d->video_stream, &d->video, 0)) < 0) goto failed;
    }
#endif
    d->audio_stream = output_audio ? av_find_best_stream(d->format, AVMEDIA_TYPE_AUDIO, -1, -1, NULL, 0) : -1;
    if (d->audio_stream >= 0) {
        if ((ret = codec_open(d, d->audio_stream, &d->audio, 0)) < 0) goto failed;
        if (!SDL_InitSubSystem(SDL_INIT_AUDIO)) { snprintf(error, error_size, "%s", SDL_GetError()); goto failed_no_error; }
        d->audio_initialized = 1;
        SDL_AudioSpec spec = {SDL_AUDIO_F32, 2, 48000};
        d->output = SDL_OpenAudioDeviceStream(SDL_AUDIO_DEVICE_DEFAULT_PLAYBACK, &spec, NULL, NULL);
        if (!d->output) { snprintf(error, error_size, "%s", SDL_GetError()); goto failed_no_error; }
        if ((ret = audio_filter(d)) < 0) goto failed;
    }
    d->packet = av_packet_alloc(); d->decoded = av_frame_alloc();
    d->sound = av_frame_alloc(); d->filtered = av_frame_alloc();
    if (!d->packet || !d->decoded || !d->sound || !d->filtered) { ret = AVERROR(ENOMEM); goto failed; }
    return d;
failed:
    fail(ret, error, error_size);
failed_no_error:
    fsvr_close(d); return NULL;
}
static void clear_packets(FsvrDecoder *d) {
    while (d->video_head) {
        FsvrPacket *node = d->video_head;
        d->video_head = node->next;
        av_packet_free(&node->packet);
        av_free(node);
    }
    d->video_tail = NULL;
    d->video_bytes = 0;
}
static int read_packet(FsvrDecoder *d) {
    if (d->video_head) {
        FsvrPacket *node = d->video_head;
        d->video_head = node->next;
        if (!d->video_head) d->video_tail = NULL;
        d->video_bytes -= node->packet->size;
        av_packet_move_ref(d->packet, node->packet);
        av_packet_free(&node->packet);
        av_free(node);
        return 0;
    }
    if (d->input_eof) return AVERROR_EOF;
    int ret = av_read_frame(d->format, d->packet);
    if (ret == AVERROR_EOF) d->input_eof = 1;
    return ret;
}
void fsvr_close(FsvrDecoder *d) {
    if (!d) return;
    if (d->output) SDL_DestroyAudioStream(d->output);
    if (d->audio_initialized) SDL_QuitSubSystem(SDL_INIT_AUDIO);
    fsvr_video_close(d->video_pipeline);
    clear_packets(d);
    avfilter_graph_free(&d->filter);
    av_frame_free(&d->decoded); av_frame_free(&d->sound); av_frame_free(&d->filtered);
    av_packet_free(&d->packet);
    avcodec_free_context(&d->video); avcodec_free_context(&d->audio);
    av_buffer_unref(&d->hardware); avformat_close_input(&d->format); av_free(d);
}
static int sound(FsvrDecoder *d) {
    if (!d->audio) return 0;
    int ret;
    while ((ret = avcodec_receive_frame(d->audio, d->sound)) >= 0) {
        double pts = d->sound->best_effort_timestamp==AV_NOPTS_VALUE?d->seek_target:d->sound->best_effort_timestamp * av_q2d(d->format->streams[d->audio_stream]->time_base)-d->start_time;
        double end = pts + (double)d->sound->nb_samples / d->sound->sample_rate;
        if (end < d->seek_target) { av_frame_unref(d->sound); continue; }
        if (isnan(d->audio_start)) d->audio_start = pts;
        d->sound->pts = av_rescale_q(d->sound->best_effort_timestamp, d->format->streams[d->audio_stream]->time_base, (AVRational){1, d->sound->sample_rate});
        ret = av_buffersrc_add_frame_flags(d->source, d->sound, AV_BUFFERSRC_FLAG_KEEP_REF);
        av_frame_unref(d->sound);
        if (ret < 0) return ret;
        while ((ret = av_buffersink_get_frame(d->sink, d->filtered)) >= 0) {
            if (!SDL_PutAudioStreamData(d->output, d->filtered->data[0], d->filtered->nb_samples * 8)) return AVERROR_EXTERNAL;
            d->audio_samples += d->filtered->nb_samples;
            av_frame_unref(d->filtered);
        }
        if (ret != AVERROR(EAGAIN) && ret != AVERROR_EOF) return ret;
    }
    return ret == AVERROR(EAGAIN) || ret == AVERROR_EOF ? 0 : ret;
}
static int finish_audio(FsvrDecoder *d) {
    if (!d->audio || d->audio_eof) return 0;
    d->audio_eof = 1;
    int ret = avcodec_send_packet(d->audio, NULL);
    if (ret < 0 && ret != AVERROR_EOF) return ret;
    if ((ret = sound(d)) < 0) return ret;
    ret = av_buffersrc_add_frame_flags(d->source, NULL, 0);
    if (ret < 0 && ret != AVERROR_EOF) return ret;
    while ((ret = av_buffersink_get_frame(d->sink, d->filtered)) >= 0) {
        if (!SDL_PutAudioStreamData(d->output, d->filtered->data[0], d->filtered->nb_samples * 8)) return AVERROR_EXTERNAL;
        d->audio_samples += d->filtered->nb_samples;
        av_frame_unref(d->filtered);
    }
    return ret == AVERROR(EAGAIN) || ret == AVERROR_EOF ? 0 : ret;
}
int fsvr_buffer_audio(FsvrDecoder *d, double seconds, char *error, int error_size) {
    if (!d->output || d->input_eof) return 0;
    // Audio can follow half-second (or longer) runs of video packets in a
    // valid MP4. Retain compressed packets, not future 8K decoded pictures,
    // while reading far enough ahead to keep the audio clock supplied.
    const size_t limit = 32u * 1024u * 1024u;
    while (d->video_bytes < limit && SDL_GetAudioStreamQueued(d->output) < seconds * 48000 * 8) {
        if (interrupt_cb(d)) return fail(AVERROR_EXIT, error, error_size);
        int ret = av_read_frame(d->format, d->packet);
        if (ret == AVERROR_EOF) {
            d->input_eof = 1;
            ret = finish_audio(d);
            return ret < 0 ? fail(ret, error, error_size) : 0;
        }
        if (ret < 0) return fail(ret, error, error_size);
        if (d->packet->stream_index == d->video_stream) {
            FsvrPacket *node = av_mallocz(sizeof(*node));
            if (!node) { av_packet_unref(d->packet); return fail(AVERROR(ENOMEM), error, error_size); }
            node->packet = av_packet_alloc();
            if (!node->packet) { av_free(node); av_packet_unref(d->packet); return fail(AVERROR(ENOMEM), error, error_size); }
            d->video_bytes += d->packet->size;
            av_packet_move_ref(node->packet, d->packet);
            if (d->video_tail) d->video_tail->next = node;
            else d->video_head = node;
            d->video_tail = node;
        } else {
            if (d->packet->stream_index == d->audio_stream) {
                ret = avcodec_send_packet(d->audio, d->packet);
                if (ret >= 0) ret = sound(d);
            }
            av_packet_unref(d->packet);
            if (ret < 0) return fail(ret, error, error_size);
        }
    }
    return 0;
}
int fsvr_next(FsvrDecoder *d, FsvrFrame **out, char *error, int error_size) {
    *out = NULL;
    for (;;) {
        if (interrupt_cb(d)) return fail(AVERROR_EXIT, error, error_size);
        int ret = d->video_pipeline ? fsvr_video_receive(d->video_pipeline, d->decoded) : avcodec_receive_frame(d->video, d->decoded);
        if (ret >= 0) {
            int64_t stamp = d->decoded->best_effort_timestamp;
            double pts = stamp == AV_NOPTS_VALUE ? 0 : stamp * av_q2d(d->format->streams[d->video_stream]->time_base)-d->start_time;
            if (pts + .0001 < d->seek_target) { av_frame_unref(d->decoded); continue; }
            FsvrFrame *f = av_mallocz(sizeof(*f));
            if (!f) return fail(AVERROR(ENOMEM), error, error_size);
            f->image = av_frame_alloc();
            if (!f->image) { av_free(f); return fail(AVERROR(ENOMEM), error, error_size); }
            av_frame_move_ref(f->image, d->decoded); f->pts = pts; f->serial=atomic_fetch_add(&frame_serial,1)+1; *out = f; return 1;
        }
        if (ret == AVERROR_EOF) return 0;
        if (ret != AVERROR(EAGAIN)) return video_fail(d, ret, error, error_size);
        if (d->draining) return 0;
        ret = read_packet(d);
        if (ret == AVERROR_EOF) {
            d->draining = 1;
            if (d->video_pipeline) fsvr_video_drain(d->video_pipeline);
            else avcodec_send_packet(d->video, NULL);
            int flush = finish_audio(d);
            if (flush < 0) return fail(flush, error, error_size);
            continue;
        }
        if (ret < 0) return fail(ret, error, error_size);
        if (d->packet->stream_index == d->video_stream) {
            ret = d->video_pipeline ? fsvr_video_send(d->video_pipeline, d->packet) : avcodec_send_packet(d->video, d->packet);
            if (ret < 0 && ret != AVERROR(EAGAIN)) { av_packet_unref(d->packet); return video_fail(d, ret, error, error_size); }
        }
        else if (d->packet->stream_index == d->audio_stream) { ret = avcodec_send_packet(d->audio, d->packet); if (ret >= 0) ret = sound(d); }
        else ret = 0;
        av_packet_unref(d->packet);
        if (ret < 0 && ret != AVERROR(EAGAIN)) return fail(ret, error, error_size);
    }
}
int fsvr_seek(FsvrDecoder *d, double seconds, int precise, char *error, int error_size) {
    int64_t target=(int64_t)((seconds+d->start_time)*AV_TIME_BASE);
    int ret = avformat_seek_file(d->format, -1, INT64_MIN, target, target, 0);
    if (ret < 0) return fail(ret, error, error_size);
    clear_packets(d);
    av_packet_unref(d->packet);
    avcodec_flush_buffers(d->video);
    if (d->video_pipeline && (ret = fsvr_video_reset(d->video_pipeline)) < 0) return video_fail(d, ret, error, error_size);
    if (d->audio) avcodec_flush_buffers(d->audio);
    if (d->output) SDL_ClearAudioStream(d->output);
    d->audio_samples = 0; d->audio_start = NAN; d->seek_target = precise ? seconds : 0; d->draining = 0;
    d->input_eof = d->audio_eof = 0;
    ret = audio_filter(d);
    return ret < 0 ? fail(ret, error, error_size) : 0;
}
void fsvr_pause(FsvrDecoder *d, int paused) { if (d->output) { if (paused) SDL_PauseAudioStreamDevice(d->output); else SDL_ResumeAudioStreamDevice(d->output); } }
void fsvr_volume(FsvrDecoder *d, float volume) { if (d->output) SDL_SetAudioStreamGain(d->output, volume); }
int fsvr_rate(FsvrDecoder *d, double rate, char *error, int size) { d->rate = rate; int ret = audio_filter(d); return ret < 0 ? fail(ret, error, size) : 0; }
double fsvr_audio_clock(FsvrDecoder *d) {
    if (!d->output || isnan(d->audio_start) || d->audio_samples<=0) return NAN;
    int queued = SDL_GetAudioStreamQueued(d->output);
    if(d->input_eof&&queued<=0)return NAN;
    return d->audio_start + (d->audio_samples - (queued > 0 ? queued / 8.0 : 0)) / 48000.0 * d->rate;
}
double fsvr_audio_end(FsvrDecoder *d) {
    if (!d->output || isnan(d->audio_start)) return NAN;
    return d->audio_start + d->audio_samples / 48000.0 * d->rate;
}
double fsvr_duration(FsvrDecoder *d) { return d->format->duration == AV_NOPTS_VALUE ? 0 : (double)d->format->duration / AV_TIME_BASE; }
double fsvr_fps(FsvrDecoder *d) { AVRational fps = av_guess_frame_rate(d->format, d->format->streams[d->video_stream], NULL); return fps.num && fps.den ? av_q2d(fps) : 30; }
int fsvr_width(FsvrDecoder *d) { return d->video->width; }
int fsvr_height(FsvrDecoder *d) { return d->video->height; }
const char *fsvr_spherical(FsvrDecoder *d) {
    AVCodecParameters *p=d->format->streams[d->video_stream]->codecpar;
    const AVPacketSideData *sd=av_packet_side_data_get(p->coded_side_data,p->nb_coded_side_data,AV_PKT_DATA_SPHERICAL);
    if(!sd||sd->size<sizeof(AVSphericalMapping))return "";
    const AVSphericalMapping *map=(const AVSphericalMapping *)sd->data;
    switch(map->projection){
        case AV_SPHERICAL_EQUIRECTANGULAR:return "equirectangular";
        case AV_SPHERICAL_EQUIRECTANGULAR_TILE:
            // A centered, full-height half-sphere is the supported 180 EQR
            // layout; arbitrary spherical tiles need their own UV transform.
            if(!map->bound_top&&!map->bound_bottom&&
               llabs((int64_t)map->bound_left-0x40000000LL)<=16&&
               llabs((int64_t)map->bound_right-0x40000000LL)<=16)return "half-equirectangular";
            return "unsupported";
        case AV_SPHERICAL_HALF_EQUIRECTANGULAR:return "half-equirectangular";
        case AV_SPHERICAL_FISHEYE:return "fisheye";
        default:return "unsupported";
    }
}
const char *fsvr_stereo(FsvrDecoder *d) {
    AVCodecParameters *p=d->format->streams[d->video_stream]->codecpar;
    const AVPacketSideData *sd=av_packet_side_data_get(p->coded_side_data,p->nb_coded_side_data,AV_PKT_DATA_STEREO3D);
    if(!sd||sd->size<sizeof(AVStereo3D))return "";
    const AVStereo3D *stereo=(const AVStereo3D *)sd->data;
    switch(stereo->type){case AV_STEREO3D_2D:return "mono";case AV_STEREO3D_SIDEBYSIDE:return "sbs";case AV_STEREO3D_TOPBOTTOM:return "tb";default:return "unsupported";}
}
const char *fsvr_decoder_name(FsvrDecoder *d) { return d->video_pipeline ? "videotoolbox-async" : d->hardware && d->hardware_format != AV_PIX_FMT_NONE ? av_hwdevice_get_type_name(((AVHWDeviceContext *)d->hardware->data)->type) : d->video->codec->name; }
const char *fsvr_version(void) { return av_version_info(); }
FsvrFrame *fsvr_frame_ref(FsvrFrame *f) { FsvrFrame *r = av_mallocz(sizeof(*r)); if (r) { r->image = av_frame_clone(f->image); r->pts = f->pts; r->serial=f->serial; if (!r->image) { av_free(r); return NULL; } } return r; }
uint64_t fsvr_frame_serial(FsvrFrame *f){return f->serial;}
void fsvr_frame_free(FsvrFrame *f) { if (f) { av_frame_free(&f->image); av_free(f); } }
double fsvr_frame_pts(FsvrFrame *f) { return f->pts; }
int fsvr_frame_width(FsvrFrame *f) { return f->image->width; }
int fsvr_frame_height(FsvrFrame *f) { return f->image->height; }
void *fsvr_frame_native(FsvrFrame *f) {
#if defined(__APPLE__)
    if (f->image->format == AV_PIX_FMT_VIDEOTOOLBOX) return f->image->data[3];
#endif
    return NULL;
}
int fsvr_frame_rgba(FsvrFrame *f, unsigned char *dst, int width, int height, int stride) {
    AVFrame *software = NULL, *src = f->image;
    int ret;
    if (src->hw_frames_ctx) {
        software = av_frame_alloc();
        ret = av_hwframe_transfer_data(software, src, 0);
        if (ret < 0) { av_frame_free(&software); return ret; }
        src = software;
    }
    struct SwsContext *sws = sws_getContext(src->width, src->height, src->format, width, height, AV_PIX_FMT_RGBA, SWS_BILINEAR, NULL, NULL, NULL);
    if (!sws) { av_frame_free(&software); return AVERROR(ENOMEM); }
    uint8_t *planes[4] = {dst}; int strides[4] = {stride};
    ret = sws_scale(sws, (const uint8_t *const *)src->data, src->linesize, 0, src->height, planes, strides);
    sws_freeContext(sws); av_frame_free(&software); return ret;
}
