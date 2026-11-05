package projection

import (
	"image"
	"math"
	"math/bits"
)

type rimPoint struct{ x, y float64 }
type lensCircle struct{ x, y, radius, scaleY float64 }

func (c lensCircle) distance(x, y float64) float64 {
	return math.Hypot(x-c.x, (y-c.y)*c.scaleY)
}

// circular fits the visible dark-to-image rim instead of assuming a centered,
// perfectly inscribed circle. Outlier rays may hit watermarks or dark content.
func (g grayFrame) circular(rect image.Rectangle) bool {
	points := g.rimPoints(rect)
	if len(points) < 10 {
		return false
	}
	best, bestCount, bestResidual := lensCircle{}, 0, math.Inf(1)
	tolerance := math.Max(.012, 1.5/float64(min(rect.Dx(), rect.Dy())))
	// Encoders and lens calibration can leave a slightly elliptical rim.
	for stretch := -4; stretch <= 4; stretch++ {
		scaleY := 1 + float64(stretch)*.025
		for i := range points {
			for _, j := range []int{len(points) / 3, len(points)/3 + 2} {
				for _, k := range []int{len(points) * 2 / 3, len(points)*2/3 + 2} {
					a, b, c := points[i], points[(i+j)%len(points)], points[(i+k)%len(points)]
					a.y, b.y, c.y = a.y*scaleY, b.y*scaleY, c.y*scaleY
					circle, ok := fitCircle(a, b, c)
					circle.y /= scaleY
					circle.scaleY = scaleY
					if !ok || circle.radius < .34 || circle.radius > .75 || circle.x < .25 || circle.x > .75 || circle.y < .25 || circle.y > .75 {
						continue
					}
					count, quadrants, residual := 0, 0, 0.0
					for _, point := range points {
						distance := math.Abs(circle.distance(point.x, point.y) - circle.radius)
						if distance <= tolerance {
							count++
							residual += distance * distance
							quadrant := 0
							if point.x > circle.x {
								quadrant++
							}
							if point.y > circle.y {
								quadrant += 2
							}
							quadrants |= 1 << quadrant
						}
					}
					// At least three quadrants must support one circle; a curved
					// object in one corner or straight letterbox edge is insufficient.
					if (count > bestCount || count == bestCount && residual < bestResidual) && bits.OnesCount(uint(quadrants)) >= 3 {
						best, bestCount, bestResidual = circle, count, residual
					}
				}
			}
		}
	}
	if bestCount < 10 || bestCount*3 < len(points)*2 {
		return false
	}
	precision := math.Max(.004, 1/float64(min(rect.Dx(), rect.Dy())))
	if best.radius > .55 {
		// A mostly cropped rim exposes only short corner arcs. Require a
		// tighter fit there so rounded rectangles do not masquerade as lenses.
		precision = math.Max(.0025, .75/float64(min(rect.Dx(), rect.Dy())))
	}
	if math.Sqrt(bestResidual/float64(bestCount)) > precision {
		return false
	}
	var outside, dark, inside, bright, texture float64
	for y := rect.Min.Y; y < rect.Max.Y; y += 2 {
		for x := rect.Min.X; x < rect.Max.X; x += 2 {
			u, v := (float64(x-rect.Min.X)+.5)/float64(rect.Dx()), (float64(y-rect.Min.Y)+.5)/float64(rect.Dy())
			distance := best.distance(u, v)
			value := g.luma[y*g.w+x]
			if distance > best.radius+.025 {
				outside++
				if value < .06 {
					dark++
				}
			}
			if distance < best.radius*.75 {
				inside++
				texture += math.Abs(g.detail[y*g.w+x])
				if value > .08 {
					bright++
				}
			}
		}
	}
	return outside >= float64(rect.Dx()*rect.Dy())*.0025 && dark/outside >= .75 && inside > 0 && bright/inside > .25 && texture/inside > .001
}

func (g grayFrame) rimPoints(rect image.Rectangle) []rimPoint {
	points := make([]rimPoint, 0, 128)
	step := .75 / float64(min(rect.Dx(), rect.Dy()))
	for i := range 128 {
		sine, cosine := math.Sincos(float64(i) * 2 * math.Pi / 128)
		tip := math.Min(.5/math.Max(math.Abs(cosine), .0001), .5/math.Max(math.Abs(sine), .0001)) - step
		bright, dark := 0, 0
		for radius := tip; radius > .25; radius -= step {
			x, y := .5+radius*cosine, .5+radius*sine
			px := rect.Min.X + int(x*float64(rect.Dx()))
			py := rect.Min.Y + int(y*float64(rect.Dy()))
			if px < rect.Min.X || px >= rect.Max.X || py < rect.Min.Y || py >= rect.Max.Y {
				break
			}
			value := g.luma[py*g.w+px]
			if value < .06 {
				dark++
				bright = 0
			} else {
				if dark < 2 {
					break // This ray exits through image content, not a visible rim.
				}
				bright++
				if bright >= 2 {
					points = append(points, rimPoint{x, y})
					break
				}
			}
		}
	}
	return points
}

func fitCircle(a, b, c rimPoint) (lensCircle, bool) {
	d := 2 * (a.x*(b.y-c.y) + b.x*(c.y-a.y) + c.x*(a.y-b.y))
	if math.Abs(d) < .0001 {
		return lensCircle{}, false
	}
	aa, bb, cc := a.x*a.x+a.y*a.y, b.x*b.x+b.y*b.y, c.x*c.x+c.y*c.y
	x := (aa*(b.y-c.y) + bb*(c.y-a.y) + cc*(a.y-b.y)) / d
	y := (aa*(c.x-b.x) + bb*(a.x-c.x) + cc*(b.x-a.x)) / d
	return lensCircle{x, y, math.Hypot(a.x-x, a.y-y), 1}, true
}
