package video

import (
	"errors"
	"image"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/shader"
	"github.com/foursmith/player/internal/media"
	"github.com/foursmith/player/internal/projection"
)

// Paint draws surfaces sharing MyGo's GPU renderer; native layers draw below it.
func Paint(surface Surface, p *ui.Painter, r ui.Rect) {
	if drawable, ok := surface.(interface{ Paint(*ui.Painter, ui.Rect) }); ok {
		drawable.Paint(p, r)
	}
}

var vrShader = shader.MustCompile(`package main
var Camera vec4
var Mode int
var Split int
var Portrait int
var FOV float
func rotate(p vec3)vec3{yaw:=Camera.x*.01745329252;pitch:=Camera.y*.01745329252;sy:=sin(yaw);cy:=cos(yaw);sx:=sin(pitch);cx:=cos(pitch);y:=cx*p.y-sx*p.z;z:=sx*p.y+cx*p.z;return vec3(cy*p.x+sy*z,y,-sy*p.x+cy*z)}
func Fragment(dst vec4,pos vec2)vec4{
 size:=rectSize();count:=1.0;if size.x>size.y{desired:=1.0;if Portrait!=0{desired=9.0/16};count=min(float(Split),max(1,floor(size.x/(size.y*desired)+.5)))}
 uv:=vec2(fract(pos.x/size.x*count),pos.y/size.y);tanHalf:=tan(FOV*.01745329252/2)/max(.01,Camera.z);ray:=rotate(normalize(vec3((uv.x*2-1)*tanHalf*size.x/count/size.y,(1-uv.y*2)*tanHalf,-1)));origin:=rotate(vec3(0,0,-Camera.w));source:=vec2(0);visible:=true
 b:=dot(origin,ray);disc:=b*b-dot(origin,origin)+10000;hit:=-b+sqrt(max(0,disc));q:=(origin+ray*hit)/100;visible=disc>=0&&hit>0;if (Mode==0||Mode==1||Mode==3||Mode==4)&&q.z>0{visible=false}
 if Mode==1||Mode==4{r:=1-acos(clamp(q.z,-1,1))/3.14159265359;phi:=atan2(q.y,q.x);source=vec2(.5+r*cos(phi),.5-r*sin(phi));if Mode==1{source.x*=.5}}else{angle:=atan2(-q.x,-q.z);source=vec2(.5-angle/6.28318530718,acos(clamp(q.y,-1,1))/3.14159265359);if Mode==0{source.x=.25-angle/6.28318530718}else if Mode==3{source.x=.5-angle/3.14159265359}else if Mode==5{source.y*=.5}}
 if !visible{return vec4(.0784314,.0705882,.0588235,1)};return image0Sample(source*image0Size())
}`)

// Match the native ambient shaders while keeping the original image draw sharp.
var ambientShader = shader.MustCompile(`package main
var Extent vec2
var Pan vec2
var Zoom float
var Aspect float
func Fragment(dst vec4,pos vec2)vec4{
 source:=(pos/rectSize()-.5)*Extent/Zoom+.5-Pan
 if source.x>=0&&source.x<=1&&source.y>=0&&source.y<=1{return vec4(0,0,0,1)}
 metric:=vec2(Aspect,1)/min(Aspect,1)
 before:=(.5*Extent/Zoom-.5+Pan)*metric
 after:=(.5*Extent/Zoom-.5-Pan)*metric
 side:=before
 if source.x>=.5{side.x=after.x};if source.y>=.5{side.y=after.y}
 spread:=max(vec2(.000001),side)
 outside:=(source-clamp(source,0,1))*metric
 distance:=length(outside)
 reach:=clamp(length(outside/spread),0,1)
 transition:=smoothstep(0,.15,distance)
 fade:=(1-.22*transition)*(1-.78*reach*reach*(3-2*reach))
 center:=clamp(source,0,1)*.92+.04
 blur:=.085+.07*reach
 light:=vec3(0);total:=0.0
 for y:=0;y<7;y++{for x:=0;x<7;x++{
 uv:=(vec2(float(x),float(y))+.5)/7
 delta:=(uv-center)/blur
 weight:=exp(-dot(delta,delta)/2)
 light+=image0Sample(uv*image0Size()).rgb*weight;total+=weight
 }}
 light/=total
 luminance:=dot(light,vec3(.2126,.7152,.0722))
 gain:=1+.35*transition*(1-max(light.r,max(light.g,light.b)))
 light=clamp((luminance+(light-luminance)*(1+.25*transition))*gain,0,1)
 light*=fade
 noise:=fract(52.9829189*fract(dot(floor(dst.xy),vec2(.06711056,.00583715))))-.5
 return vec4(clamp(light+noise/255,0,1),1)
}`)

var ambientStepShader = shader.MustCompile(`package main
var Mix float
func Fragment(dst vec4,pos vec2)vec4{
 uv:=pos/rectSize()
 current:=vec3(0)
 for y:=-1;y<=1;y++{for x:=-1;x<=1;x++{
 current+=image0Sample((uv+vec2(float(x),float(y))/21)*image0Size()).rgb/9
 }}
 if Mix>=1{return vec4(current,1)}
 return vec4(mix(image1Sample(uv*image1Size()).rgb,current,Mix),1)
}`)

// bitmapSurface is used when direct hardware-texture sharing is unavailable.
// Projection still runs on MyGo's platform GPU backend.
type bitmapSurface struct {
	converter       media.RGBAConverter
	bitmap          *ui.Bitmap
	frame           *media.Frame // owns the planes borrowed by UpdateYUV
	serial          uint64
	params          Parameters
	aspect          float64
	pts             float64
	ambientHistory  ambientHistory
	ambientCanvases [2]*ui.Canvas
	ambientIndex    int
}

func (r *bitmapSurface) Present(frame *media.Frame, p Parameters, width, height, scale float64) error {
	if r.bitmap == nil || frame.ID() != r.serial || (p.Mode == projection.Flat) != (r.params.Mode == projection.Flat) {
		var held *media.Frame
		var yuv media.YUV420
		var direct bool
		if p.Mode == projection.Flat {
			yuv, direct = frame.YUV420()
		}
		if direct {
			held = frame.Clone()
			if held == nil {
				return errors.New("cannot retain video frame")
			}
			if r.bitmap == nil {
				r.bitmap = ui.NewBitmap(image.NewRGBA(image.Rect(0, 0, 1, 1)))
			}
			format, matrix := ui.I420, ui.BT601
			if yuv.Interleaved {
				format = ui.NV12
			}
			switch yuv.Colorspace {
			case 1:
				matrix = ui.BT709
			case 9, 10:
				matrix = ui.BT2020
			}
			r.bitmap.UpdateYUV(&ui.YUVImage{Format: format, Matrix: matrix, FullRange: yuv.FullRange,
				Width: yuv.Width, Height: yuv.Height, Planes: yuv.Planes, Strides: yuv.Strides})
			r.converter.Close()
		} else {
			w, h := frame.Size()
			img, err := r.converter.Convert(frame, w, h)
			if err != nil {
				return err
			}
			if r.bitmap == nil {
				r.bitmap = ui.NewBitmap(img)
			} else {
				r.bitmap.Update(img)
			}
		}
		// UpdateYUV borrows its planes, including for paused redraws. Retire
		// the old reference only after the bitmap has its replacement.
		r.frame.Close()
		r.frame = held
		r.serial = frame.ID()
	}
	r.pts = frame.PTS()
	r.params = p
	w, h := frame.Size()
	r.aspect = float64(w) / float64(h)
	return nil
}
func (r *bitmapSurface) Paint(p *ui.Painter, rect ui.Rect) {
	if r.bitmap == nil {
		return
	}
	if r.params.Mode == projection.Flat {
		light := r.bitmap
		if !r.params.DisableAmbientLight {
			bw, bh := r.bitmap.Size()
			alpha, changed := r.ambientHistory.step(r.serial, r.pts, r.params.Generation, image.Pt(bw, bh))
			if r.ambientCanvases[0] == nil {
				for i := range r.ambientCanvases {
					r.ambientCanvases[i] = ui.NewCanvas(ambientSize, ambientSize)
				}
			}
			if changed {
				next := 1 - r.ambientIndex
				r.ambientCanvases[next].Draw(p, func(cp *ui.Painter) {
					ambientStepShader.Draw(cp, ui.Rect{W: ambientSize, H: ambientSize}, shader.Options{Images: [4]*ui.Bitmap{r.bitmap, r.ambientCanvases[r.ambientIndex].Bitmap()}, Uniforms: map[string]any{"Mix": alpha}})
				})
				r.ambientIndex = next
			}
			light = r.ambientCanvases[r.ambientIndex].Bitmap()
		}
		v := r.params.FlatView.Constrain(float64(rect.W/rect.H), r.aspect)
		ex, ey := flatExtent(float64(rect.W/rect.H), r.aspect)
		w, h := float32(v.Zoom/ex)*rect.W, float32(v.Zoom/ey)*rect.H
		dst := ui.Rect{X: rect.X + rect.W/2 - float32(.5-v.X)*w,
			Y: rect.Y + rect.H/2 - float32(.5-v.Y)*h, W: w, H: h}
		if r.params.DisableAmbientLight {
			r.ambientHistory = ambientHistory{}
			p.Fill(rect, ui.RGB(0, 0, 0), 0)
		} else {
			ambientShader.Draw(p, rect, shader.Options{Images: [4]*ui.Bitmap{light}, Uniforms: map[string]any{
				"Extent": [2]float32{float32(ex), float32(ey)}, "Pan": [2]float32{float32(v.X), float32(v.Y)},
				"Zoom": v.Zoom, "Aspect": r.aspect,
			}})
		}
		p.Clip(rect, 0, func() { p.ImageRegion(r.bitmap, ui.Rect{}, dst, ui.ImageOptions{}) })
		return
	}
	r.ambientHistory = ambientHistory{}
	fov := r.params.FOV
	if fov <= 0 {
		fov = 80
	}
	portrait := 0
	if r.params.Portrait {
		portrait = 1
	}
	v := r.params.View
	vrShader.Draw(p, rect, shader.Options{Images: [4]*ui.Bitmap{r.bitmap}, Uniforms: map[string]any{"Camera": [4]float32{float32(v.Yaw), float32(v.Pitch), float32(v.Zoom), float32(v.Forward)}, "Mode": int(r.params.Mode), "Split": max(1, r.params.SplitCount), "Portrait": portrait, "FOV": fov}})
}
func (r *bitmapSurface) Capture(frame *media.Frame, p Parameters, width, height int) (*image.RGBA, error) {
	sw, sh := frame.Size()
	source, err := frame.RGBA(sw, sh)
	if err != nil {
		return nil, err
	}
	out := image.NewRGBA(image.Rect(0, 0, width, height))
	count := len(projection.Viewports(float64(width), float64(height), max(1, p.SplitCount), p.Portrait))
	if p.Mode == projection.Flat {
		count = 1
	}
	aspect := float64(width) / float64(count) / float64(height)
	var field ambientField
	var spread ambientSpread
	if p.Mode == projection.Flat && !p.DisableAmbientLight {
		field = newAmbientField(source)
		spread = flatAmbientSpread(p.FlatView, aspect, float64(sw)/float64(sh))
	}
	fov := p.FOV
	if fov <= 0 {
		fov = 80
	}
	for y := range height {
		for x := range width {
			localX := (float64(x) + .5) * float64(count) / float64(width)
			localX -= float64(int(localX))
			localY := (float64(y) + .5) / float64(height)
			var u, v float64
			var visible bool
			if p.Mode == projection.Flat {
				u, v, visible = p.FlatView.UV(localX, localY, aspect, float64(sw)/float64(sh))
			} else {
				origin, ray := projection.Ray(localX, localY, aspect, fov, p.View)
				u, v, visible = projection.UV(p.Mode, origin, ray)
			}
			at := y*out.Stride + x*4
			if !visible {
				out.Pix[at], out.Pix[at+1], out.Pix[at+2], out.Pix[at+3] = 20, 18, 15, 255
				if p.Mode == projection.Flat && !p.DisableAmbientLight {
					out.SetRGBA(x, y, field.color(u, v, float64(sw)/float64(sh), image.Pt(x, y), spread))
				} else if p.Mode == projection.Flat {
					out.Pix[at], out.Pix[at+1], out.Pix[at+2] = 0, 0, 0
				}
				continue
			}
			sample := sampleRGBA(source, u, v)
			for channel, value := range sample {
				out.Pix[at+channel] = uint8(value + .5)
			}
		}
	}
	return out, nil
}
func (r *bitmapSurface) Clear() {
	r.converter.Close()
	r.ambientHistory = ambientHistory{}
	r.ambientCanvases = [2]*ui.Canvas{}
	r.bitmap = nil
	r.frame.Close()
	r.frame = nil
	r.serial = 0
}
func (r *bitmapSurface) Close() { r.Clear() }
