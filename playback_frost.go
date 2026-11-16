package main

import (
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/shader"
)

// Frost is local to the existing round playback buttons. The shader softly
// blurs content in the UI scene. Native video lies below that scene: there,
// the translucent matte tint provides contrast without copying video frames.
type playbackFrostMaterial struct{}

var playbackFrostShader = shader.MustCompile(`package main
var Dark float
var Radius float
var HighContrast float
func Fragment(dst vec4, pos vec2) vec4 {
 size := rectSize()
 q := abs(pos-size/2)-(size/2-vec2(Radius))
 distance := length(max(q,vec2(0)))+min(max(q.x,q.y),0)-Radius
 coverage := 1-smoothstep(-.5,.5,distance)
 if coverage<=0 { return vec4(0) }
 background := backdropSample(pos)
 // A neutral satin finish keeps foreground contrast stable over bright and
 // dark footage. It stays translucent when the backdrop is a native layer.
 tint := mix(vec3(.94,.95,.96),vec3(.13,.15,.17),Dark)
 opacity := mix(.76,.72,Dark)
 color := vec4(tint*opacity,opacity)+background*(1-opacity)
 // A narrow upper reflection and subdued lower edge give the matte surface
 // depth, without distorting the picture or creating a bright full outline.
 rim := smoothstep(-mix(.75,1.5,HighContrast),0,distance)
 upper := 1-smoothstep(.15,.85,pos.y/size.y)
 light := mix(vec3(.35),vec3(1),upper)
 strength := rim*mix(.12,.38,upper)*mix(1,1.5,HighContrast)
 color = vec4(light*strength,strength)+color*(1-strength)
 return color*coverage
}`)

func (playbackFrostMaterial) PaintMaterial(p *ui.Painter, r ui.Rect, radii [4]float32) {
	if p.Preferences().ReduceTransparency {
		p.FillRadii(r, radii, ui.Hex(windowBackground(p.Theme().Dark)))
		if p.Preferences().HighContrast {
			p.Stroke(r, ink(p, .4), radii[0], 1)
		}
		return
	}
	dark, contrast := float32(0), float32(0)
	if p.Theme().Dark {
		dark = 1
	}
	if p.Preferences().HighContrast {
		contrast = 1
	}
	playbackFrostShader.Draw(p, r, shader.Options{BackdropBlur: 6, Uniforms: map[string]any{
		"Dark": dark, "Radius": min(radii[0], r.W/2, r.H/2), "HighContrast": contrast,
	}})
}
