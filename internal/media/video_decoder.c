#include "video_decoder.h"
#include <libavutil/error.h>
#include <stdio.h>

#if defined(__APPLE__)
#include <VideoToolbox/VideoToolbox.h>
#include <libavutil/hwcontext.h>
#include <libavutil/hwcontext_videotoolbox.h>
#include <libavutil/pixdesc.h>
#include <pthread.h>
#include <time.h>

// Submitted pictures remain ordered by presentation timestamp, including
// B pictures. A small submission window keeps VideoToolbox busy without
// retaining an unbounded number of full-resolution decoded surfaces.
typedef struct VideoPicture {
    struct VideoPicture *next;
    int64_t pts;
    CVPixelBufferRef image;
    OSStatus status;
    int done;
} VideoPicture;

struct FsvrVideoDecoder {
    VTDecompressionSessionRef session;
    CMVideoFormatDescriptionRef format;
    CFDictionaryRef attributes, specification;
    AVBufferRef *frames;
    AVCodecParameters *parameters;
    AVRational time_base;
    pthread_mutex_t mutex;
    pthread_cond_t completed;
    VideoPicture *pictures;
    int count, draining;
    int64_t last_dts, next_pts, random_access_pts;
    int (*interrupted)(void *);
    void *opaque;
    char error[256];
};

static int video_error(FsvrVideoDecoder *d, const char *stage, OSStatus status) {
    snprintf(d->error, sizeof(d->error), "VideoToolbox %s failed (OSStatus %d)", stage, (int)status);
    return AVERROR_EXTERNAL;
}

const char *fsvr_video_error(FsvrVideoDecoder *d) { return d ? d->error : ""; }

// An HEVC CRA starts an open GOP. Its following RASL pictures have earlier
// presentation timestamps and may refer to pictures before the seek. Missing
// references are expected only for these non-displayable leading pictures;
// errors at or after the random-access picture remain fatal.
static int discard_leading_reference_error(FsvrVideoDecoder *d, OSStatus status, int64_t pts) {
    return d->parameters->codec_id == AV_CODEC_ID_HEVC &&
           status == kVTVideoDecoderReferenceMissingErr &&
           d->random_access_pts != AV_NOPTS_VALUE && pts < d->random_access_pts;
}

static CMVideoFormatDescriptionRef format_description(const AVCodecParameters *p) {
    const uint8_t *x = p->extradata, *sets[64];
    size_t sizes[64];
    int length = p->extradata_size, count = 0, at;
    CMVideoFormatDescriptionRef format = NULL;
    if (!x || length < 7 || x[0] != 1) return NULL;
    if (p->codec_id == AV_CODEC_ID_HEVC) {
        if (length < 23) return NULL;
        at = 23;
        for (int a = 0; a < x[22]; a++) {
            if (at + 3 > length) return NULL;
            int type = x[at++] & 63;
            int n = (x[at] << 8) | x[at+1]; at += 2;
            for (int i = 0; i < n; i++) {
                if (at + 2 > length) return NULL;
                int size = (x[at] << 8) | x[at+1]; at += 2;
                if (at + size > length) return NULL;
                if (type >= 32 && type <= 34) {
                    if (count == 64) return NULL;
                    sets[count] = x + at; sizes[count++] = size;
                }
                at += size;
            }
        }
        if (count) CMVideoFormatDescriptionCreateFromHEVCParameterSets(NULL, count, sets, sizes, (x[21] & 3) + 1, NULL, &format);
    } else if (p->codec_id == AV_CODEC_ID_H264) {
        at = 6;
        for (int group = 0, n = x[5] & 31; group < 2; group++) {
            for (int i = 0; i < n; i++) {
                if (at + 2 > length || count == 64) return NULL;
                int size = (x[at] << 8) | x[at+1]; at += 2;
                if (at + size > length) return NULL;
                sets[count] = x + at; sizes[count++] = size; at += size;
            }
            if (group == 0) { if (at >= length) return NULL; n = x[at++]; }
        }
        if (count) CMVideoFormatDescriptionCreateFromH264ParameterSets(NULL, count, sets, sizes, (x[4] & 3) + 1, &format);
    }
    return format;
}

static void decoded(void *opaque, void *source, OSStatus status, VTDecodeInfoFlags flags, CVImageBufferRef image, CMTime pts, CMTime duration) {
    FsvrVideoDecoder *d = opaque;
    VideoPicture *picture = source;
    pthread_mutex_lock(&d->mutex);
    picture->status = status;
    picture->image = image ? CVPixelBufferRetain(image) : NULL;
    picture->done = 1;
    pthread_cond_broadcast(&d->completed);
    pthread_mutex_unlock(&d->mutex);
}

static int create_session(FsvrVideoDecoder *d) {
    VTDecompressionOutputCallbackRecord callback = {decoded, d};
    OSStatus status = VTDecompressionSessionCreate(NULL, d->format, d->specification, d->attributes, &callback, &d->session);
    if (status) return video_error(d, "session creation", status);
    VTSessionSetProperty(d->session, kVTDecompressionPropertyKey_RealTime, kCFBooleanTrue);
    return 0;
}

FsvrVideoDecoder *fsvr_video_open(AVStream *stream, AVBufferRef *device, int (*interrupted)(void *), void *opaque) {
    if (!device || (stream->codecpar->codec_id != AV_CODEC_ID_HEVC && stream->codecpar->codec_id != AV_CODEC_ID_H264)) return NULL;
    CMVideoFormatDescriptionRef format = format_description(stream->codecpar);
    if (!format) return NULL;
    FsvrVideoDecoder *d = av_mallocz(sizeof(*d));
    if (!d) { CFRelease(format); return NULL; }
    pthread_mutex_init(&d->mutex, NULL);
    pthread_cond_init(&d->completed, NULL);
    d->format = format;
    d->parameters = avcodec_parameters_alloc();
    if (!d->parameters || avcodec_parameters_copy(d->parameters, stream->codecpar) < 0) goto failed;
    d->time_base = stream->time_base;
    d->last_dts = AV_NOPTS_VALUE;
    d->random_access_pts = AV_NOPTS_VALUE;
    d->interrupted = interrupted; d->opaque = opaque;
    const void *spec_keys[] = {kVTVideoDecoderSpecification_RequireHardwareAcceleratedVideoDecoder};
    const void *spec_values[] = {kCFBooleanTrue};
    d->specification = CFDictionaryCreate(NULL, spec_keys, spec_values, 1, &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
    const AVPixFmtDescriptor *pixel = av_pix_fmt_desc_get(stream->codecpar->format);
    int ten = pixel && pixel->comp[0].depth > 8;
    int full = stream->codecpar->color_range == AVCOL_RANGE_JPEG;
    int32_t type = ten ? (full ? kCVPixelFormatType_420YpCbCr10BiPlanarFullRange : kCVPixelFormatType_420YpCbCr10BiPlanarVideoRange)
                       : (full ? kCVPixelFormatType_420YpCbCr8BiPlanarFullRange : kCVPixelFormatType_420YpCbCr8BiPlanarVideoRange);
    CFNumberRef number = CFNumberCreate(NULL, kCFNumberSInt32Type, &type);
    CFDictionaryRef surface = CFDictionaryCreate(NULL, NULL, NULL, 0, &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
    const void *keys[] = {kCVPixelBufferPixelFormatTypeKey, kCVPixelBufferMetalCompatibilityKey, kCVPixelBufferIOSurfacePropertiesKey};
    const void *values[] = {number, kCFBooleanTrue, surface};
    d->attributes = CFDictionaryCreate(NULL, keys, values, 3, &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
    CFRelease(number); CFRelease(surface);
    d->frames = av_hwframe_ctx_alloc(device);
    if (!d->frames) goto failed;
    AVHWFramesContext *frames = (AVHWFramesContext *)d->frames->data;
    frames->format = AV_PIX_FMT_VIDEOTOOLBOX;
    frames->sw_format = ten ? AV_PIX_FMT_P010LE : AV_PIX_FMT_NV12;
    frames->width = stream->codecpar->width; frames->height = stream->codecpar->height;
    if (av_hwframe_ctx_init(d->frames) < 0 || create_session(d) < 0) goto failed;
    return d;
failed:
    fsvr_video_close(d); return NULL;
}

static CMTime timestamp(FsvrVideoDecoder *d, int64_t value) {
    return value == AV_NOPTS_VALUE ? kCMTimeInvalid : CMTimeMake(value * d->time_base.num, d->time_base.den);
}

int fsvr_video_send(FsvrVideoDecoder *d, const AVPacket *packet) {
    VideoPicture *picture = av_mallocz(sizeof(*picture));
    if (!picture) return AVERROR(ENOMEM);
    picture->pts = packet->pts != AV_NOPTS_VALUE ? packet->pts : (packet->dts != AV_NOPTS_VALUE ? packet->dts : d->next_pts);
    if (d->random_access_pts == AV_NOPTS_VALUE && (packet->flags & AV_PKT_FLAG_KEY)) d->random_access_pts = picture->pts;
    d->next_pts = picture->pts + FFMAX(1, packet->duration);
    CMBlockBufferRef block = NULL;
    CMSampleBufferRef sample = NULL;
    OSStatus status = CMBlockBufferCreateWithMemoryBlock(NULL, NULL, packet->size, NULL, NULL, 0, packet->size, 0, &block);
    if (!status) status = CMBlockBufferReplaceDataBytes(packet->data, block, 0, packet->size);
    CMSampleTimingInfo timing = {timestamp(d, packet->duration), timestamp(d, picture->pts), timestamp(d, packet->dts)};
    size_t size = packet->size;
    if (!status) status = CMSampleBufferCreateReady(NULL, block, d->format, 1, 1, &timing, 1, &size, &sample);
    if (block) CFRelease(block);
    if (status) { av_free(picture); return video_error(d, "sample creation", status); }
    pthread_mutex_lock(&d->mutex);
    VideoPicture **at = &d->pictures;
    while (*at && (*at)->pts <= picture->pts) at = &(*at)->next;
    picture->next = *at; *at = picture; d->count++;
    d->last_dts = packet->dts;
    pthread_mutex_unlock(&d->mutex);
    status = VTDecompressionSessionDecodeFrame(d->session, sample, kVTDecodeFrame_EnableAsynchronousDecompression, picture, NULL);
    CFRelease(sample);
    if (status) {
        // On an immediate error VideoToolbox guarantees no output callback.
        pthread_mutex_lock(&d->mutex);
        picture->status = status; picture->done = 1;
        pthread_cond_broadcast(&d->completed);
        pthread_mutex_unlock(&d->mutex);
    }
    if (discard_leading_reference_error(d, status, picture->pts)) return 0;
    return status ? video_error(d, "frame submission", status) : 0;
}

static void release_image(void *opaque, uint8_t *image) { CVPixelBufferRelease((CVPixelBufferRef)image); }

int fsvr_video_receive(FsvrVideoDecoder *d, AVFrame *frame) {
    pthread_mutex_lock(&d->mutex);
    for (;;) {
        if (d->interrupted(d->opaque)) { pthread_mutex_unlock(&d->mutex); return AVERROR_EXIT; }
        VideoPicture *picture = d->pictures;
        if (!picture) { int ret = d->draining ? AVERROR_EOF : AVERROR(EAGAIN); pthread_mutex_unlock(&d->mutex); return ret; }
        // DTS bounds the PTS of packets not submitted yet. Wait until the
        // submission window crosses this picture before presenting it.
        if (!d->draining && d->count < 32 && (d->count < 8 || (d->last_dts != AV_NOPTS_VALUE && picture->pts > d->last_dts))) {
            pthread_mutex_unlock(&d->mutex); return AVERROR(EAGAIN);
        }
        if (!picture->done) {
            struct timespec until;
            clock_gettime(CLOCK_REALTIME, &until);
            until.tv_nsec += 20000000;
            if (until.tv_nsec >= 1000000000) { until.tv_sec++; until.tv_nsec -= 1000000000; }
            pthread_cond_timedwait(&d->completed, &d->mutex, &until);
            continue;
        }
        d->pictures = picture->next; d->count--;
        CVPixelBufferRef image = picture->image;
        OSStatus status = picture->status;
        int64_t pts = picture->pts;
        av_free(picture);
        if (status) {
            if (image) CVPixelBufferRelease(image);
            if (discard_leading_reference_error(d, status, pts)) continue;
            pthread_mutex_unlock(&d->mutex);
            return video_error(d, "output callback", status);
        }
        if (!image) continue;
        pthread_mutex_unlock(&d->mutex);
        frame->format = AV_PIX_FMT_VIDEOTOOLBOX;
        frame->width = d->parameters->width; frame->height = d->parameters->height;
        frame->pts = frame->best_effort_timestamp = pts;
        frame->color_range = d->parameters->color_range;
        frame->colorspace = d->parameters->color_space;
        frame->color_primaries = d->parameters->color_primaries;
        frame->color_trc = d->parameters->color_trc;
        frame->chroma_location = d->parameters->chroma_location;
        frame->hw_frames_ctx = av_buffer_ref(d->frames);
        if (!frame->hw_frames_ctx) { CVPixelBufferRelease(image); av_frame_unref(frame); return AVERROR(ENOMEM); }
        frame->buf[0] = av_buffer_create((uint8_t *)image, 0, release_image, NULL, 0);
        if (!frame->buf[0]) { CVPixelBufferRelease(image); av_frame_unref(frame); return AVERROR(ENOMEM); }
        frame->data[3] = (uint8_t *)image;
        int ret = av_vt_pixbuf_set_attachments(NULL, image, frame);
        if (ret < 0) av_frame_unref(frame);
        return ret < 0 ? ret : 0;
    }
}

void fsvr_video_drain(FsvrVideoDecoder *d) {
    d->draining = 1;
    VTDecompressionSessionFinishDelayedFrames(d->session);
}

static void clear(FsvrVideoDecoder *d) {
    if (d->session) {
        VTDecompressionSessionWaitForAsynchronousFrames(d->session);
        VTDecompressionSessionInvalidate(d->session);
        CFRelease(d->session); d->session = NULL;
    }
    while (d->pictures) {
        VideoPicture *picture = d->pictures; d->pictures = picture->next;
        if (picture->image) CVPixelBufferRelease(picture->image);
        av_free(picture);
    }
    d->count = d->draining = 0;
    d->last_dts = AV_NOPTS_VALUE; d->next_pts = 0;
    d->random_access_pts = AV_NOPTS_VALUE;
}

int fsvr_video_reset(FsvrVideoDecoder *d) { clear(d); d->error[0] = 0; return create_session(d); }
void fsvr_video_close(FsvrVideoDecoder *d) {
    if (!d) return;
    clear(d);
    if (d->format) CFRelease(d->format);
    if (d->attributes) CFRelease(d->attributes);
    if (d->specification) CFRelease(d->specification);
    av_buffer_unref(&d->frames);
    avcodec_parameters_free(&d->parameters);
    pthread_cond_destroy(&d->completed); pthread_mutex_destroy(&d->mutex);
    av_free(d);
}
#else
FsvrVideoDecoder *fsvr_video_open(AVStream *stream, AVBufferRef *device, int (*interrupted)(void *), void *opaque) { return NULL; }
int fsvr_video_send(FsvrVideoDecoder *decoder, const AVPacket *packet) { return AVERROR(ENOSYS); }
int fsvr_video_receive(FsvrVideoDecoder *decoder, AVFrame *frame) { return AVERROR(ENOSYS); }
int fsvr_video_reset(FsvrVideoDecoder *decoder) { return AVERROR(ENOSYS); }
const char *fsvr_video_error(FsvrVideoDecoder *decoder) { return ""; }
void fsvr_video_drain(FsvrVideoDecoder *decoder) {}
void fsvr_video_close(FsvrVideoDecoder *decoder) {}
#endif
