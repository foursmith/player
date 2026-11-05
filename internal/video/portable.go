package video

import (
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/shader"
	"github.com/foursmith/player/internal/media"
	"github.com/foursmith/player/internal/projection"
	"image"
)

// Paint draws surfaces sharing MyGo's GPU renderer. macOS is composited below it.
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

var flatShader = shader.MustCompile(`package main
var Flat vec3
func Fragment(dst vec4,pos vec2)vec4{
 size:=rectSize();ratio:=(size.x/size.y)/(image0Size().x/image0Size().y)
 source:=(pos/size-vec2(.5))*vec2(max(1,ratio),max(1,1/ratio))/Flat.z+vec2(.5)-Flat.xy
 if source.x<0||source.x>1||source.y<0||source.y>1{return vec4(0,0,0,1)}
 return image0Sample(source*image0Size())
}`)

// rgbaSurface is used when direct hardware-texture sharing is unavailable.
// Projection still runs on MyGo's platform GPU backend.
type rgbaSurface struct {
	bitmap *ui.Bitmap
	serial uint64
	params Parameters
	aspect float64
}

func (r *rgbaSurface) Present(frame *media.Frame, p Parameters, width, height, scale float64) error {
	if r.bitmap == nil || frame.ID() != r.serial {
		w, h := frame.Size()
		img, err := frame.RGBA(w, h)
		if err != nil {
			return err
		}
		if r.bitmap == nil {
			r.bitmap = ui.NewBitmap(img)
		} else {
			r.bitmap.Update(img)
		}
		r.serial = frame.ID()
	}
	r.params = p
	w, h := frame.Size()
	r.aspect = float64(w) / float64(h)
	return nil
}
func (r *rgbaSurface) Paint(p *ui.Painter, rect ui.Rect) {
	if r.bitmap == nil {
		return
	}
	if r.params.Mode == projection.Flat {
		v := r.params.FlatView.Constrain(float64(rect.W/rect.H), r.aspect)
		flatShader.Draw(p, rect, shader.Options{Images: [4]*ui.Bitmap{r.bitmap}, Uniforms: map[string]any{"Flat": [3]float32{float32(v.X), float32(v.Y), float32(v.Zoom)}}})
		return
	}
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
func (r *rgbaSurface) Capture(frame *media.Frame, p Parameters, width, height int) (*image.RGBA, error) {
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
				if p.Mode == projection.Flat {
					out.Pix[at], out.Pix[at+1], out.Pix[at+2] = 0, 0, 0
				}
				continue
			}
			sx, sy := projection.Clamp(u*float64(sw)-.5, 0, float64(sw-1)), projection.Clamp(v*float64(sh)-.5, 0, float64(sh-1))
			x0, y0 := int(sx), int(sy)
			x1, y1 := min(sw-1, x0+1), min(sh-1, y0+1)
			fx, fy := sx-float64(x0), sy-float64(y0)
			for channel := range 4 {
				a := float64(source.Pix[y0*source.Stride+x0*4+channel])
				b := float64(source.Pix[y0*source.Stride+x1*4+channel])
				c := float64(source.Pix[y1*source.Stride+x0*4+channel])
				d := float64(source.Pix[y1*source.Stride+x1*4+channel])
				out.Pix[at+channel] = uint8((a*(1-fx)+b*fx)*(1-fy) + (c*(1-fx)+d*fx)*fy + .5)
			}
		}
	}
	return out, nil
}
func (r *rgbaSurface) Close() { r.bitmap = nil }
