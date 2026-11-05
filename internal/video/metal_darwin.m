#import <Cocoa/Cocoa.h>
#import <Metal/Metal.h>
#import <QuartzCore/CAMetalLayer.h>
#import <QuartzCore/CATransaction.h>
#import <CoreVideo/CoreVideo.h>
#include "metal.h"

static NSString *shaderSource = @"\
#include <metal_stdlib>\n\
using namespace metal;\n\
struct Params { float yaw,pitch,zoom,forward; int mode,split,portrait,rgba; float aspect; int full_range; float width,height,fov,pad; float kr,kb,yoffset,yscale,uvoffset,uvscale; };\n\
struct Vertex { float4 position [[position]]; float2 uv; };\n\
vertex Vertex vertexMain(uint id [[vertex_id]]) { float2 uv=float2((id<<1)&2,id&2); return {float4(uv*2-1,0,1),float2(uv.x,1-uv.y)}; }\n\
float3 rotateRay(float3 p,float yaw,float pitch) { float sy=sin(yaw),cy=cos(yaw),sx=sin(pitch),cx=cos(pitch);float y=cx*p.y-sx*p.z,z=sx*p.y+cx*p.z;return float3(cy*p.x+sy*z,y,-sy*p.x+cy*z); }\n\
fragment float4 fragmentMain(Vertex in [[stage_in]],constant Params &p [[buffer(0)]],texture2d<float> ytex [[texture(0)]],texture2d<float> uvtex [[texture(1)]]) {\n\
 constexpr sampler s(coord::normalized,address::clamp_to_edge,filter::linear);\n\
 float2 outSize=float2(p.width,p.height);float count=outSize.x>outSize.y?min(float(p.split),max(1.0,round(outSize.x/(outSize.y*(p.portrait!=0?9.0/16.0:1.0))))):1;\n\
 float2 uv=float2(fract(in.uv.x*count),in.uv.y);float aspect=outSize.x/count/outSize.y;\n\
 float t=tan(p.fov*0.01745329252/2)/max(.01,p.zoom);float yaw=p.yaw*0.01745329252,pitch=p.pitch*0.01745329252;\n\
 float3 ray=rotateRay(normalize(float3((uv.x*2-1)*t*aspect,(1-uv.y*2)*t,-1)),yaw,pitch);float3 origin=rotateRay(float3(0,0,-p.forward),yaw,pitch);\n\
 float2 source;bool visible=true;\n\
 if(p.mode==6){float hit=(-65-origin.z)/ray.z;float3 q=origin+ray*hit;source=float2(.5+q.x/(60*p.aspect),.5-(q.y-10)/60);visible=ray.z<0&&hit>0&&all(source>=0)&&all(source<=1);}\n\
 else {float b=dot(origin,ray),disc=b*b-dot(origin,origin)+10000;float hit=-b+sqrt(max(0.0,disc));float3 q=(origin+ray*hit)/100;visible=disc>=0&&hit>0;bool isHalf=p.mode==0||p.mode==1||p.mode==3||p.mode==4;if(isHalf&&q.z>0)visible=false;\n\
 if(p.mode==1||p.mode==4){float r=1-acos(clamp(q.z,-1.0,1.0))/M_PI_F,phi=atan2(q.y,q.x);source=float2(.5+r*cos(phi),.5-r*sin(phi));if(p.mode==1)source.x*=.5;}\n\
 else {float a=atan2(-q.x,-q.z);source=float2(.5-a/(2*M_PI_F),acos(clamp(q.y,-1.0,1.0))/M_PI_F);if(p.mode==0)source.x=.25-a/(2*M_PI_F);else if(p.mode==3)source.x=.5-a/M_PI_F;else if(p.mode==5)source.y*=.5;}}\n\
 if(!visible)return float4(.0784314,.0705882,.0588235,1);\n\
 if(p.rgba!=0)return float4(ytex.sample(s,source).rgb,1);\n\
 float y=(ytex.sample(s,source).r-p.yoffset)*p.yscale;float2 cbcr=(uvtex.sample(s,source).rg-float2(p.uvoffset))*p.uvscale;\n\
 float kg=1-p.kr-p.kb;\n\
 float3 rgb=float3(y+2*(1-p.kr)*cbcr.y,y-2*p.kb*(1-p.kb)/kg*cbcr.x-2*p.kr*(1-p.kr)/kg*cbcr.y,y+2*(1-p.kb)*cbcr.x);return float4(clamp(rgb,0.0,1.0),1);\n\
}";

@interface FsvrMetal : NSObject
@property(nonatomic,strong) id<MTLDevice> device;
@property(nonatomic,strong) id<MTLCommandQueue> queue;
@property(nonatomic,strong) id<MTLRenderPipelineState> pipeline;
@property(nonatomic,strong) CAMetalLayer *layer;
@property(nonatomic,weak) NSView *view;
@property(nonatomic) CVMetalTextureCacheRef cache;
@end
@implementation FsvrMetal
- (void)dealloc { if(_cache)CFRelease(_cache); }
@end

void *fsvr_metal_create(uintptr_t window,char *error,int size) {
 @autoreleasepool {
    FsvrMetal *r=[FsvrMetal new];r.device=MTLCreateSystemDefaultDevice();r.queue=[r.device newCommandQueue];
    if(!r.device||!r.queue){snprintf(error,size,"Metal device unavailable");return NULL;}
    NSError *err=nil;id<MTLLibrary> lib=[r.device newLibraryWithSource:shaderSource options:nil error:&err];
    if(!lib){snprintf(error,size,"%s",err.localizedDescription.UTF8String);return NULL;}
    MTLRenderPipelineDescriptor *pd=[MTLRenderPipelineDescriptor new];pd.vertexFunction=[lib newFunctionWithName:@"vertexMain"];pd.fragmentFunction=[lib newFunctionWithName:@"fragmentMain"];pd.colorAttachments[0].pixelFormat=MTLPixelFormatBGRA8Unorm;
    r.pipeline=[r.device newRenderPipelineStateWithDescriptor:pd error:&err];
    if(!r.pipeline){snprintf(error,size,"%s",err.localizedDescription.UTF8String);return NULL;}
    CVMetalTextureCacheRef cache=NULL;CVReturn result=CVMetalTextureCacheCreate(NULL,NULL,r.device,NULL,&cache);r.cache=cache;
    if(result!=kCVReturnSuccess){snprintf(error,size,"CVMetalTextureCacheCreate: %d",result);return NULL;}
    if(window){NSWindow *w=(__bridge NSWindow *)(void *)window;r.view=w.contentView;[r.view setWantsLayer:YES];r.layer=[CAMetalLayer layer];r.layer.device=r.device;r.layer.pixelFormat=MTLPixelFormatBGRA8Unorm;r.layer.framebufferOnly=YES;r.layer.opaque=YES;r.layer.autoresizingMask=kCALayerWidthSizable|kCALayerHeightSizable;r.layer.frame=r.view.bounds;[r.view.layer insertSublayer:r.layer atIndex:0];}
    return (__bridge_retained void *)r;
 }
}
static int render(FsvrMetal *r,CVPixelBufferRef buffer,const unsigned char *rgba,int sw,int sh,FsvrParameters params,id<MTLTexture> output,id<CAMetalDrawable> drawable,unsigned char *readback) {
 CVMetalTextureRef luma=NULL,chroma=NULL;id<MTLTexture> yt=nil,ct=nil;
 if(buffer){
    OSType format=CVPixelBufferGetPixelFormatType(buffer);params.full_range=format==kCVPixelFormatType_420YpCbCr8BiPlanarFullRange||format==kCVPixelFormatType_420YpCbCr10BiPlanarFullRange;
    BOOL ten=format==kCVPixelFormatType_420YpCbCr10BiPlanarVideoRange||format==kCVPixelFormatType_420YpCbCr10BiPlanarFullRange;
    params.kr=.2126;params.kb=.0722;
    CFTypeRef matrix=CVBufferCopyAttachment(buffer,kCVImageBufferYCbCrMatrixKey,NULL);
    if(matrix){if(CFEqual(matrix,kCVImageBufferYCbCrMatrix_ITU_R_601_4)){params.kr=.299;params.kb=.114;}else if(CFEqual(matrix,kCVImageBufferYCbCrMatrix_ITU_R_2020)){params.kr=.2627;params.kb=.0593;}CFRelease(matrix);}
    params.yoffset=params.full_range?0:(ten?4096.0/65535.0:16.0/255.0);
    params.yscale=params.full_range?(ten?65535.0/65472.0:1):(ten?65535.0/56064.0:255.0/219.0);
    params.uvoffset=ten?32768.0/65535.0:128.0/255.0;
    params.uvscale=params.full_range?(ten?65535.0/65472.0:1):(ten?65535.0/57344.0:255.0/224.0);
    if(CVPixelBufferGetPlaneCount(buffer)<2)return -2;
    if(CVMetalTextureCacheCreateTextureFromImage(NULL,r.cache,buffer,NULL,ten?MTLPixelFormatR16Unorm:MTLPixelFormatR8Unorm,CVPixelBufferGetWidthOfPlane(buffer,0),CVPixelBufferGetHeightOfPlane(buffer,0),0,&luma)!=kCVReturnSuccess)return -3;
    if(CVMetalTextureCacheCreateTextureFromImage(NULL,r.cache,buffer,NULL,ten?MTLPixelFormatRG16Unorm:MTLPixelFormatRG8Unorm,CVPixelBufferGetWidthOfPlane(buffer,1),CVPixelBufferGetHeightOfPlane(buffer,1),1,&chroma)!=kCVReturnSuccess){CFRelease(luma);return -3;}
    yt=CVMetalTextureGetTexture(luma);ct=CVMetalTextureGetTexture(chroma);
 }else if(rgba){
    MTLTextureDescriptor *desc=[MTLTextureDescriptor texture2DDescriptorWithPixelFormat:MTLPixelFormatRGBA8Unorm width:sw height:sh mipmapped:NO];desc.storageMode=MTLStorageModeShared;desc.usage=MTLTextureUsageShaderRead;yt=[r.device newTextureWithDescriptor:desc];[yt replaceRegion:MTLRegionMake2D(0,0,sw,sh) mipmapLevel:0 withBytes:rgba bytesPerRow:sw*4];ct=yt;params.rgba=1;
 }else return -1;
 params.aspect=(float)sw/sh;params.width=output.width;params.height=output.height;
 MTLRenderPassDescriptor *pass=[MTLRenderPassDescriptor renderPassDescriptor];pass.colorAttachments[0].texture=output;pass.colorAttachments[0].loadAction=MTLLoadActionClear;pass.colorAttachments[0].storeAction=MTLStoreActionStore;pass.colorAttachments[0].clearColor=MTLClearColorMake(0,0,0,1);
 id<MTLCommandBuffer> command=[r.queue commandBuffer];id<MTLRenderCommandEncoder> encoder=[command renderCommandEncoderWithDescriptor:pass];[encoder setRenderPipelineState:r.pipeline];[encoder setFragmentBytes:&params length:sizeof(params) atIndex:0];[encoder setFragmentTexture:yt atIndex:0];[encoder setFragmentTexture:ct atIndex:1];[encoder drawPrimitives:MTLPrimitiveTypeTriangle vertexStart:0 vertexCount:3];[encoder endEncoding];
 if(drawable)[command presentDrawable:drawable];
 if(buffer)CFRetain(buffer);
 [command addCompletedHandler:^(id<MTLCommandBuffer> cb){if(luma)CFRelease(luma);if(chroma)CFRelease(chroma);if(buffer)CFRelease(buffer);}];
 [command commit];if(readback){[command waitUntilCompleted];[output getBytes:readback bytesPerRow:output.width*4 fromRegion:MTLRegionMake2D(0,0,output.width,output.height) mipmapLevel:0];}
 return 0;
}
int fsvr_metal_present(void *renderer,void *pixel_buffer,const unsigned char *rgba,int sw,int sh,FsvrParameters params,double width,double height,double scale){
 @autoreleasepool {FsvrMetal *r=(__bridge FsvrMetal *)renderer;if(!r.layer)return -1;[CATransaction begin];[CATransaction setDisableActions:YES];r.layer.frame=r.view.bounds;r.layer.contentsScale=scale;r.layer.drawableSize=CGSizeMake(width*scale,height*scale);[CATransaction commit];id<CAMetalDrawable> drawable=[r.layer nextDrawable];if(!drawable)return 0;return render(r,pixel_buffer,rgba,sw,sh,params,drawable.texture,drawable,NULL);}
}
int fsvr_metal_capture(void *renderer,void *pixel_buffer,const unsigned char *rgba,int sw,int sh,FsvrParameters params,int width,int height,unsigned char *out){
 @autoreleasepool {FsvrMetal *r=(__bridge FsvrMetal *)renderer;MTLTextureDescriptor *desc=[MTLTextureDescriptor texture2DDescriptorWithPixelFormat:MTLPixelFormatBGRA8Unorm width:width height:height mipmapped:NO];desc.storageMode=MTLStorageModeShared;desc.usage=MTLTextureUsageRenderTarget;id<MTLTexture> target=[r.device newTextureWithDescriptor:desc];return render(r,pixel_buffer,rgba,sw,sh,params,target,nil,out);}
}
void fsvr_metal_close(void *renderer){@autoreleasepool {FsvrMetal *r=CFBridgingRelease(renderer);[r.layer removeFromSuperlayer];}}
