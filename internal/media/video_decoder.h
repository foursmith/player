#ifndef FSVR_VIDEO_DECODER_H
#define FSVR_VIDEO_DECODER_H
#include <libavformat/avformat.h>
#include <libavutil/frame.h>

typedef struct FsvrVideoDecoder FsvrVideoDecoder;
FsvrVideoDecoder *fsvr_video_open(AVStream *stream, AVBufferRef *device, int (*interrupted)(void *), void *opaque);
int fsvr_video_send(FsvrVideoDecoder *decoder, const AVPacket *packet);
int fsvr_video_receive(FsvrVideoDecoder *decoder, AVFrame *frame);
int fsvr_video_reset(FsvrVideoDecoder *decoder);
void fsvr_video_drain(FsvrVideoDecoder *decoder);
void fsvr_video_close(FsvrVideoDecoder *decoder);
#endif
