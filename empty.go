package main

import (
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/shader"
	"math"
	"time"
)

var ocean = shader.MustCompile(`package main
var Time float
func hash(p vec2)float{return fract(sin(dot(p,vec2(127.1,311.7)))*43758.5453123)}
func noise(p vec2)float{i:=floor(p);f:=fract(p);f=f*f*(3-2*f);return mix(mix(hash(i),hash(i+vec2(1,0)),f.x),mix(hash(i+vec2(0,1)),hash(i+1),f.x),f.y)}
func fbm(p vec2)float{value:=0.0;amplitude:=.5;for i:=0;i<3;i++{value+=amplitude*noise(p);p=p*2.03+vec2(17.1,9.2);amplitude*=.5};return value}
func Fragment(dst vec4,pos vec2)vec4{
 size:=rectSize();uv:=vec2(pos.x/size.x,1-pos.y/size.y);aspect:=size.x/max(size.y,1.0);waterTime:=Time*.24;rayTime:=Time*.65;waterUv:=vec2((uv.x-.5)*aspect,uv.y)
 current:=fbm(waterUv*vec2(2.2,2.8)+vec2(waterTime*.16,-waterTime*.1));fineCurrent:=noise(waterUv*vec2(5.5,4)+vec2(-waterTime*.11,waterTime*.14));depth:=smoothstep(0,1,uv.y)
 color:=mix(vec3(.012,.105,.165),vec3(.025,.285,.390),pow(depth,1.45));color=mix(color,vec3(.160,.650,.720),pow(depth,5)*.48);color+=(current-.5)*vec3(.018,.055,.065)
 rays:=0.0;for i:=0;i<3;i++{fi:=float(i);origin:=.16+fi*.31+sin(rayTime*(.35+fi*.03)+fi*2.1)*.025;center:=origin+(.14+(fi-1)*.055)*(1-uv.y);rays+=exp(-abs(uv.x-center)*(13+fi*2.7))*(.7+.3*current)}
 rays*=smoothstep(.02,.75,uv.y)*smoothstep(1.12,.62,uv.y);color+=vec3(.38,.81,.84)*rays*.14
 causticUv:=waterUv*vec2(8,6);waveA:=abs(sin(causticUv.x+sin(causticUv.y*.74+waterTime)));waveB:=abs(sin(causticUv.y*1.17-waterTime*.83+sin(causticUv.x*.63)));caustic:=pow(max(0,1-min(waveA,waveB)),5)*pow(depth,6)*(.45+fineCurrent*.55);color+=vec3(.72,.95,.93)*caustic*.16;color*=.7+(1-smoothstep(.28,.82,distance(uv,vec2(.5,.54))))*.3;return vec4(color,1)
}`)
var oceanStarted = time.Now()

func (a *application) empty(c *ui.Context) {
	t := c.Now().Sub(oceanStarted).Seconds()
	if c.Preferences().ReduceMotion {
		t = 0
	}
	wide := a.width >= 640
	heroW, heroH, logoSize := float32(352), float32(240), float32(160)
	if wide {
		heroW, heroH, logoSize = 400, 272, 192
	}
	ui.Box(c.Key("empty")).Absolute().Top(0).Left(0).Right(0).Bottom(0).Center().Draw(func(p *ui.Painter, r ui.Rect) { ocean.Draw(p, r, shader.Options{Uniforms: map[string]any{"Time": t}}) }).Children(func() {
		driftTime := math.Mod(t, 72) / 36
		if driftTime > 1 {
			driftTime = 2 - driftTime
		}
		drift := motion(driftTime, []motionFrame{{at: 0, x: 16, y: -6}, {at: .28, x: -18, y: 1}, {at: .55, y: 8}, {at: .78, x: 18, y: 2}, {at: 1, x: -6, y: -8}}, [4]float64{.42, 0, .58, 1})
		ui.Column(c).AlignItems(ui.Center).Gap(16).Translate(float32(drift.x), float32(drift.y)).Children(func() {
			ui.Box(c).Size(heroW, heroH).Children(func() {
				pose := motion(math.Mod(t, 18)/18, []motionFrame{{at: 0, x: 1, y: 1, rotate: -.2, scale: .998}, {at: .24, x: 1, y: 1, rotate: -.2, scale: .998}, {at: .48, x: 4, y: -2, rotate: .35, scale: 1.004}, {at: .56, x: -3, rotate: -.55, scale: .996}, {at: .63, x: -27, y: 1, rotate: -1.15, scale: 1.012}, {at: .70, x: -18, rotate: -.45, scale: 1.004}, {at: .84, x: 4, y: -1, rotate: .25, scale: 1}, {at: 1, x: 1, y: 1, rotate: -.2, scale: .998}}, [4]float64{.42, 0, .24, 1})
				ui.Image(c, logo).Size(logoSize, logoSize).Attach(ui.AnchorCenter, ui.AnchorCenter).Translate(float32(pose.x), float32(pose.y)).Rotate(float32(pose.rotate)).Scale(float32(pose.scale), float32(pose.scale))
				for i, dot := range []struct {
					x, y float32
					key  string
				}{{0, 24, "empty.noHeadset"}, {40, heroH - 6, "empty.portraitLayout"}, {heroW - 10, heroH - 38, "empty.portraitCentering"}, {heroW/2 - 72, -8, "empty.autoFramedVr"}, {-6, 130, "common.appName"}} {
					ui.Box(c.Key(i)).Absolute().Left(dot.x+float32(math.Sin(t/(2+float64(i)))*12)).Top(dot.y+float32(math.Cos(t/(3+float64(i)))*4)).Size(6, 6).Radius(3).Background(ui.Hex("#b8f3ec")).Focusable().Label(a.t(dot.key))
				}
			})
			ui.Column(c).AlignItems(ui.Center).Gap(24).Children(func() {
				ui.RichText(c, ui.Span{Text: a.t("common.tagline"), Italic: true, Size: 11, Weight: 500, LetterSpacing: 2.1, Color: ui.RGBA(245, 255, 252, .42)})
				if a.serverStatus == "authentication-required" || a.serverStatus == "connecting" {
					a.authPicker(c)
				} else {
					a.pickers(c, 256)
				}
			})
		})
	})
	if !c.Preferences().ReduceMotion {
		// Keep the foreground motion in step with the display, rather than a timer.
		c.AnimationFrame()
	}
}

type motionFrame struct{ at, x, y, rotate, scale float64 }

func bezierProgress(x, a, b, c, d float64) float64 {
	lo, hi := 0.0, 1.0
	curve := func(t, p, q float64) float64 { return 3*(1-t)*(1-t)*t*p + 3*(1-t)*t*t*q + t*t*t }
	for range 18 {
		mid := (lo + hi) / 2
		if curve(mid, a, c) < x {
			lo = mid
		} else {
			hi = mid
		}
	}
	return curve((lo+hi)/2, b, d)
}
func motion(t float64, frames []motionFrame, ease [4]float64) motionFrame {
	for i := 1; i < len(frames); i++ {
		right, left := frames[i], frames[i-1]
		if t > right.at {
			continue
		}
		p := bezierProgress((t-left.at)/(right.at-left.at), ease[0], ease[1], ease[2], ease[3])
		lerp := func(a, b float64) float64 { return a + (b-a)*p }
		return motionFrame{t, lerp(left.x, right.x), lerp(left.y, right.y), lerp(left.rotate, right.rotate), lerp(left.scale, right.scale)}
	}
	return frames[len(frames)-1]
}
