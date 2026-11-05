#ifndef FSVR_METAL_H
#define FSVR_METAL_H
#include <stdint.h>
typedef struct { float yaw,pitch,zoom,forward; int mode,split,portrait,rgba; float aspect; int full_range; float width,height,fov,pad; float kr,kb,yoffset,yscale,uvoffset,uvscale; } FsvrParameters;
void *fsvr_metal_create(uintptr_t window,char *error,int size);
int fsvr_metal_present(void *renderer,void *pixel_buffer,const unsigned char *rgba,int source_width,int source_height,FsvrParameters params,double width,double height,double scale);
int fsvr_metal_capture(void *renderer,void *pixel_buffer,const unsigned char *rgba,int source_width,int source_height,FsvrParameters params,int width,int height,unsigned char *output);
void fsvr_metal_close(void *renderer);
#endif
