#ifndef FSVR_FFMPEG_H
#define FSVR_FFMPEG_H
#include <stdint.h>
#include <stddef.h>

typedef struct FsvrDecoder FsvrDecoder;
typedef struct FsvrFrame FsvrFrame;
typedef struct FsvrCancel FsvrCancel;
FsvrCancel *fsvr_cancel_new(void);
void fsvr_cancel_set(FsvrCancel *cancel);
void fsvr_cancel_free(FsvrCancel *cancel);
uint64_t fsvr_frame_serial(FsvrFrame *f);

FsvrDecoder *fsvr_open(const char *url, const char *cookies, FsvrCancel *cancel, int output_audio, char *error, int error_size);
void fsvr_close(FsvrDecoder *d);
void fsvr_interrupt(FsvrDecoder *d);
int fsvr_next(FsvrDecoder *d, FsvrFrame **frame, char *error, int error_size);
int fsvr_buffer_audio(FsvrDecoder *d, double seconds, char *error, int error_size);
int fsvr_seek(FsvrDecoder *d, double seconds, char *error, int error_size);
void fsvr_pause(FsvrDecoder *d, int paused);
void fsvr_volume(FsvrDecoder *d, float volume);
int fsvr_rate(FsvrDecoder *d, double rate, char *error, int error_size);
double fsvr_audio_clock(FsvrDecoder *d);
double fsvr_duration(FsvrDecoder *d);
double fsvr_fps(FsvrDecoder *d);
int fsvr_width(FsvrDecoder *d);
int fsvr_height(FsvrDecoder *d);
const char *fsvr_decoder_name(FsvrDecoder *d);
const char *fsvr_version(void);

FsvrFrame *fsvr_frame_ref(FsvrFrame *f);
void fsvr_frame_free(FsvrFrame *f);
double fsvr_frame_pts(FsvrFrame *f);
int fsvr_frame_width(FsvrFrame *f);
int fsvr_frame_height(FsvrFrame *f);
void *fsvr_frame_native(FsvrFrame *f);
int fsvr_frame_rgba(FsvrFrame *f, unsigned char *dst, int width, int height, int stride);
#endif
