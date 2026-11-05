// Package projection owns video mode IDs, VR projections and camera geometry.
package projection

import "math"

type Mode int

const (
	SBS180 Mode = iota
	SBSFisheye
	Mono360
	Mono180
	MonoFisheye
	TB360
	Flat
)

var Labels = [...]string{"SBS 180 EQR", "SBS 180 FE", "Mono 360 EQR", "Mono 180 EQR", "Mono 180 FE", "TB 360 EQR", "Flat 2D"}
var IDs = [...]string{"sbs_180_eqr", "sbs_180_fe", "mono_360_eqr", "m_180_eqr", "m_180_fe", "tb_360_eqr", "flat_2d"}

type View struct{ Yaw, Pitch, Zoom, Forward float64 }
type Vec3 struct{ X, Y, Z float64 }
type Rect struct{ X, Y, Width, Height float64 }

func DefaultView() View               { return View{Zoom: 1} }
func (m Mode) IsVR() bool             { return m >= SBS180 && m <= TB360 }
func (m Mode) Half() bool             { return m == SBS180 || m == SBSFisheye || m == Mono180 || m == MonoFisheye }
func (m Mode) Fisheye() bool          { return m == SBSFisheye || m == MonoFisheye }
func (m Mode) Binocular() bool        { return m == SBS180 || m == SBSFisheye || m == TB360 }
func Clamp(v, lo, hi float64) float64 { return math.Min(hi, math.Max(lo, v)) }
func Angle(v float64) float64         { return math.Mod(math.Mod(v+180, 360)+360, 360) - 180 }
func Radians(v float64) float64       { return v * math.Pi / 180 }
func Degrees(v float64) float64       { return v * 180 / math.Pi }
func (v Vec3) Add(w Vec3) Vec3        { return Vec3{v.X + w.X, v.Y + w.Y, v.Z + w.Z} }
func (v Vec3) Scale(k float64) Vec3   { return Vec3{v.X * k, v.Y * k, v.Z * k} }
func (v Vec3) Dot(w Vec3) float64     { return v.X*w.X + v.Y*w.Y + v.Z*w.Z }
func (v Vec3) Unit() Vec3             { return v.Scale(1 / math.Sqrt(v.Dot(v))) }

// Rotate applies Three.js Euler order YXZ, with roll zero.
func Rotate(p Vec3, yaw, pitch float64) Vec3 {
	sy, cy := math.Sincos(Radians(yaw))
	sx, cx := math.Sincos(Radians(pitch))
	y, z := cx*p.Y-sx*p.Z, sx*p.Y+cx*p.Z
	return Vec3{cy*p.X + sy*z, y, -sy*p.X + cy*z}
}
func Ray(x, y, aspect, fov float64, view View) (Vec3, Vec3) {
	tan := math.Tan(Radians(fov)/2) / math.Max(.01, view.Zoom)
	return Rotate(Vec3{Z: -view.Forward}, view.Yaw, view.Pitch), Rotate(Vec3{(2*x - 1) * tan * aspect, (1 - 2*y) * tan, -1}.Unit(), view.Yaw, view.Pitch)
}
func SpherePoint(origin, ray Vec3, radius float64) (Vec3, bool) {
	b := origin.Dot(ray)
	discriminant := b*b - origin.Dot(origin) + radius*radius
	if discriminant < 0 {
		return Vec3{}, false
	}
	t := -b + math.Sqrt(discriminant)
	if t <= 0 {
		return Vec3{}, false
	}
	return origin.Add(ray.Scale(t)), true
}
func UV(mode Mode, origin, ray Vec3) (u, v float64, visible bool) {
	if !mode.IsVR() {
		return 0, 0, false
	}
	p, ok := SpherePoint(origin, ray, 100)
	if !ok || (mode.Half() && p.Z > 0) {
		return 0, 0, false
	}
	p = p.Scale(.01)
	if mode.Fisheye() {
		r := 1 - math.Acos(Clamp(p.Z, -1, 1))/math.Pi
		phi := math.Atan2(p.Y, p.X)
		u = .5 + r*math.Cos(phi)
		v = .5 - r*math.Sin(phi)
		if mode == SBSFisheye {
			u *= .5
		}
		return u, v, true
	}
	yaw := math.Atan2(-p.X, -p.Z)
	u = .5 - yaw/(2*math.Pi)
	v = math.Acos(Clamp(p.Y, -1, 1)) / math.Pi
	if mode == SBS180 {
		u = .25 - yaw/(2*math.Pi)
	} else if mode == Mono180 {
		u = .5 - yaw/math.Pi
	} else if mode == TB360 {
		v *= .5
	}
	return u, v, true
}
func Viewports(width, height float64, count int, portrait bool) []Rect {
	if width <= height {
		count = 1
	}
	aspect := 1.0
	if portrait {
		aspect = 9.0 / 16
	}
	count = max(1, min(count, int(math.Round(width/(height*aspect)))))
	result := make([]Rect, count)
	for i := range result {
		result[i] = Rect{float64(i) * width / float64(count), 0, width / float64(count), height}
	}
	return result
}
func Coverage(mode Mode, view View, aspect float64) float64 {
	if !mode.Half() {
		return math.Inf(1)
	}
	margin := math.Inf(1)
	radius := 100.0
	if mode.Fisheye() {
		radius = 99
	}
	for i := 0; i <= 8; i++ {
		n := -1 + float64(i)/4
		for _, q := range [][2]float64{{-1, n}, {1, n}, {n, -1}, {n, 1}} {
			o, r := Ray((q[0]+1)/2, (q[1]+1)/2, aspect, 80, view)
			p, ok := SpherePoint(o, r, radius)
			if !ok {
				return math.Inf(-1)
			}
			// The half-sphere mask covers z >= 0; retain the original 2° inset.
			angle := Degrees(math.Asin(Clamp(-p.Z/radius, -1, 1)))
			margin = math.Min(margin, angle-2)
		}
	}
	return margin
}
func Constrain(mode Mode, current, proposed View, aspect float64) View {
	proposed.Pitch = Clamp(proposed.Pitch, -85, 85)
	currentMargin, proposedMargin := Coverage(mode, current, aspect), Coverage(mode, proposed, aspect)
	if !mode.Half() || proposedMargin >= 0 || currentMargin < 0 && proposedMargin > currentMargin {
		return proposed
	}
	if currentMargin < 0 {
		return current
	}
	lo, hi := 0.0, 1.0
	dy := Angle(proposed.Yaw - current.Yaw)
	for range 20 {
		mid := (lo + hi) / 2
		v := current
		v.Yaw += dy * mid
		v.Pitch += (proposed.Pitch - current.Pitch) * mid
		v.Forward += (proposed.Forward - current.Forward) * mid
		if Coverage(mode, v, aspect) >= 0 {
			lo = mid
		} else {
			hi = mid
		}
	}
	current.Yaw += dy * lo
	current.Pitch += (proposed.Pitch - current.Pitch) * lo
	current.Forward += (proposed.Forward - current.Forward) * lo
	return current
}
func ZoomForward(forward, scale float64) float64 {
	return 100 - (100-forward)/math.Max(.000001, scale)
}
