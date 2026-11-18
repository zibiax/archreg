package geo

import (
	"math"
	"sort"
)

// Pt is a 2D point.
type Pt = [2]float64

// ConvexHull returns the convex hull (counter-clockwise, not closed) of pts.
func ConvexHull(pts []Pt) []Pt {
	if len(pts) < 3 {
		return append([]Pt(nil), pts...)
	}
	p := append([]Pt(nil), pts...)
	sort.Slice(p, func(i, j int) bool {
		if p[i][0] != p[j][0] {
			return p[i][0] < p[j][0]
		}
		return p[i][1] < p[j][1]
	})
	cross := func(o, a, b Pt) float64 { return (a[0]-o[0])*(b[1]-o[1]) - (a[1]-o[1])*(b[0]-o[0]) }
	var h []Pt
	for _, pt := range p {
		for len(h) >= 2 && cross(h[len(h)-2], h[len(h)-1], pt) <= 0 {
			h = h[:len(h)-1]
		}
		h = append(h, pt)
	}
	lower := len(h) + 1
	for i := len(p) - 2; i >= 0; i-- {
		for len(h) >= lower && cross(h[len(h)-2], h[len(h)-1], p[i]) <= 0 {
			h = h[:len(h)-1]
		}
		h = append(h, p[i])
	}
	return h[:len(h)-1]
}

// Area of a simple polygon (absolute value).
func Area(r []Pt) float64 {
	s := 0.0
	for i := range r {
		j := (i + 1) % len(r)
		s += r[i][0]*r[j][1] - r[j][0]*r[i][1]
	}
	return math.Abs(s) / 2
}

// PointInRing reports whether p is inside ring r (even-odd).
func PointInRing(p Pt, r []Pt) bool {
	in := false
	for i, j := 0, len(r)-1; i < len(r); j, i = i, i+1 {
		a, b := r[i], r[j]
		if (a[1] > p[1]) != (b[1] > p[1]) && p[0] < (b[0]-a[0])*(p[1]-a[1])/(b[1]-a[1])+a[0] {
			in = !in
		}
	}
	return in
}

// PointInPolygon: rings[0] is the outer ring, the rest are holes.
func PointInPolygon(p Pt, rings [][]Pt) bool {
	if len(rings) == 0 || !PointInRing(p, rings[0]) {
		return false
	}
	for _, h := range rings[1:] {
		if PointInRing(p, h) {
			return false
		}
	}
	return true
}

// MinWidth returns the smallest width of the convex hull of pts (rotating
// calipers): the shortest side of the minimum-width enclosing strip. Used as
// a room's "minDimension".
func MinWidth(pts []Pt) float64 {
	h := ConvexHull(pts)
	if len(h) < 3 {
		return 0
	}
	best := math.Inf(1)
	for i := range h {
		a, b := h[i], h[(i+1)%len(h)]
		dx, dy := b[0]-a[0], b[1]-a[1]
		l := math.Hypot(dx, dy)
		if l == 0 {
			continue
		}
		far := 0.0
		for _, p := range h {
			d := math.Abs((p[0]-a[0])*dy-(p[1]-a[1])*dx) / l
			far = math.Max(far, d)
		}
		best = math.Min(best, far)
	}
	return best
}

// Transform rotates (degrees, counter-clockwise) and then translates points.
func Transform(pts []Pt, rotDeg, dx, dy float64) []Pt {
	s, c := math.Sincos(rotDeg * math.Pi / 180)
	out := make([]Pt, len(pts))
	for i, p := range pts {
		out[i] = Pt{p[0]*c - p[1]*s + dx, p[0]*s + p[1]*c + dy}
	}
	return out
}

// Diameter is the largest distance between two points of the convex hull
// (used for the width of a window or door seen from above).
func Diameter(pts []Pt) float64 {
	h := ConvexHull(pts)
	best := 0.0
	for i := range h {
		for j := i + 1; j < len(h); j++ {
			best = math.Max(best, math.Hypot(h[i][0]-h[j][0], h[i][1]-h[j][1]))
		}
	}
	return best
}
