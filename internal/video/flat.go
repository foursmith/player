package video

import "github.com/foursmith/player/internal/projection"

// FlatView uses source-relative offsets, independently of the VR camera.
type FlatView struct{ X, Y, Zoom float64 }

func flatExtent(aspect, sourceAspect float64) (float64, float64) {
	ratio := max(.000001, aspect) / max(.000001, sourceAspect)
	return max(1, ratio), max(1, 1/ratio)
}

func (v FlatView) Constrain(aspect, sourceAspect float64) FlatView {
	v.Zoom = projection.Clamp(v.Zoom, 1, 10)
	x, y := flatExtent(aspect, sourceAspect)
	x, y = max(0, (1-x/v.Zoom)/2), max(0, (1-y/v.Zoom)/2)
	v.X = projection.Clamp(v.X, -x, x)
	v.Y = projection.Clamp(v.Y, -y, y)
	return v
}

func (v FlatView) Pan(dx, dy, aspect, sourceAspect float64) FlatView {
	x, y := flatExtent(aspect, sourceAspect)
	v = v.Constrain(aspect, sourceAspect)
	v.X += dx * x / v.Zoom
	v.Y += dy * y / v.Zoom
	return v.Constrain(aspect, sourceAspect)
}

func (v FlatView) Scale(scale, aspect, sourceAspect float64) FlatView {
	v.Zoom = max(1, v.Zoom) * scale
	return v.Constrain(aspect, sourceAspect)
}

// UV fits the complete image, then applies bounded 2D zoom and pan.
func (view FlatView) UV(x, y, aspect, sourceAspect float64) (u, v float64, visible bool) {
	view = view.Constrain(aspect, sourceAspect)
	ex, ey := flatExtent(aspect, sourceAspect)
	u, v = (x-.5)*ex/view.Zoom+.5-view.X, (y-.5)*ey/view.Zoom+.5-view.Y
	return u, v, u >= 0 && u <= 1 && v >= 0 && v <= 1
}
