#ifndef FSVR_TFLITE_H
#define FSVR_TFLITE_H
#include <stddef.h>
typedef struct FsvrInference FsvrInference;
FsvrInference *fsvr_inference_open(const char *library,const unsigned char *model,size_t length,char *error,int size);
void fsvr_inference_close(FsvrInference *engine);
int fsvr_inference_size(FsvrInference *engine,int dimension);
int fsvr_inference_run(FsvrInference *engine,const float *input,size_t input_size,char *error,int size);
int fsvr_inference_output_count(FsvrInference *engine);
size_t fsvr_inference_output(FsvrInference *engine,int index,float *output,size_t capacity);
#endif
