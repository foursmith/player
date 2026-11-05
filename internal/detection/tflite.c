#include "tflite.h"
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#ifdef _WIN32
#include <windows.h>
static void *open_library(const char *path){int n=MultiByteToWideChar(CP_UTF8,0,path,-1,NULL,0);if(n<=0)return NULL;wchar_t *wide=malloc(n*sizeof(wchar_t));if(!wide)return NULL;MultiByteToWideChar(CP_UTF8,0,path,-1,wide,n);HMODULE module=LoadLibraryW(wide);free(wide);return (void*)module;}
#define get_symbol(handle,name) ((void *)GetProcAddress((HMODULE)handle,name))
#define close_library(handle) FreeLibrary((HMODULE)handle)
#else
#include <dlfcn.h>
#define open_library(path) dlopen(path,RTLD_NOW|RTLD_LOCAL)
#define get_symbol(handle,name) dlsym(handle,name)
#define close_library(handle) dlclose(handle)
#endif

struct FsvrInference {
 void *library,*model,*options,*interpreter;
 unsigned char *bytes;
 void *(*model_create)(const void *,size_t);
 void (*model_delete)(void *);
 void *(*options_create)(void);
 void (*options_delete)(void *);
 void (*threads)(void *,int);
 void *(*interpreter_create)(const void *,const void *);
 void (*interpreter_delete)(void *);
 int (*allocate)(void *);
 int (*invoke)(void *);
 void *(*input)(void *,int);
 const void *(*output)(const void *,int);
 int (*output_count)(const void *);
 int (*dimensions)(const void *);
 int (*dimension)(const void *,int);
 size_t (*byte_size)(const void *);
 int (*copy_from)(void *,const void *,size_t);
 int (*copy_to)(const void *,void *,size_t);
};
#define LOAD(field,name) do { *(void **)(&engine->field)=get_symbol(engine->library,name);if(!engine->field){snprintf(error,size,"Missing LiteRT API: %s",name);goto failed;} }while(0)
FsvrInference *fsvr_inference_open(const char *library,const unsigned char *model,size_t length,char *error,int size){
 FsvrInference *engine=calloc(1,sizeof(*engine));if(!engine)return NULL;
 engine->library=open_library(library);if(!engine->library){snprintf(error,size,"Cannot load LiteRT runtime: %s",library);goto failed;}
 LOAD(model_create,"TfLiteModelCreate");LOAD(model_delete,"TfLiteModelDelete");LOAD(options_create,"TfLiteInterpreterOptionsCreate");LOAD(options_delete,"TfLiteInterpreterOptionsDelete");LOAD(threads,"TfLiteInterpreterOptionsSetNumThreads");LOAD(interpreter_create,"TfLiteInterpreterCreate");LOAD(interpreter_delete,"TfLiteInterpreterDelete");LOAD(allocate,"TfLiteInterpreterAllocateTensors");LOAD(invoke,"TfLiteInterpreterInvoke");LOAD(input,"TfLiteInterpreterGetInputTensor");LOAD(output,"TfLiteInterpreterGetOutputTensor");LOAD(output_count,"TfLiteInterpreterGetOutputTensorCount");LOAD(dimensions,"TfLiteTensorNumDims");LOAD(dimension,"TfLiteTensorDim");LOAD(byte_size,"TfLiteTensorByteSize");LOAD(copy_from,"TfLiteTensorCopyFromBuffer");LOAD(copy_to,"TfLiteTensorCopyToBuffer");
 engine->bytes=malloc(length);if(!engine->bytes)goto failed;memcpy(engine->bytes,model,length);
 engine->model=engine->model_create(engine->bytes,length);if(!engine->model){snprintf(error,size,"Invalid face model");goto failed;}
 engine->options=engine->options_create();if(!engine->options)goto failed;engine->threads(engine->options,2);
 engine->interpreter=engine->interpreter_create(engine->model,engine->options);if(!engine->interpreter||engine->allocate(engine->interpreter)!=0){snprintf(error,size,"Cannot allocate face model tensors");goto failed;}
 return engine;
failed:fsvr_inference_close(engine);return NULL;
}
void fsvr_inference_close(FsvrInference *e){if(!e)return;if(e->interpreter)e->interpreter_delete(e->interpreter);if(e->options)e->options_delete(e->options);if(e->model)e->model_delete(e->model);free(e->bytes);if(e->library)close_library(e->library);free(e);}
int fsvr_inference_size(FsvrInference *e,int d){void *input=e->input(e->interpreter,0);if(d>=e->dimensions(input))return 0;return e->dimension(input,d);}
int fsvr_inference_run(FsvrInference *e,const float *input,size_t n,char *error,int size){void *tensor=e->input(e->interpreter,0);if(e->byte_size(tensor)!=n*sizeof(float)){snprintf(error,size,"Face model input size mismatch");return -1;}if(e->copy_from(tensor,input,n*sizeof(float))||e->invoke(e->interpreter)){snprintf(error,size,"Face model inference failed");return -1;}return 0;}
int fsvr_inference_output_count(FsvrInference *e){return e->output_count(e->interpreter);}
size_t fsvr_inference_output(FsvrInference *e,int index,float *output,size_t capacity){const void *tensor=e->output(e->interpreter,index);size_t n=e->byte_size(tensor)/sizeof(float);if(output&&capacity>=n&&e->copy_to(tensor,output,n*sizeof(float)))return 0;return n;}
