package rules

import (
	"encoding/json"
	"fmt"
	"math"

	"github.com/ctessum/polyclip-go"
)

// ring is a list of points; it may or may not repeat the first point.
type ring [][2]float64

// polygons parses a GeoJSON Polygon or MultiPolygon into polyclip form.
func (g *Geometry) polygons() (polyclip.Polygon, error) {
	if g == nil {
		return nil, nil
	}
	var rings [][][2]float64
	switch g.Type {
	case "Polygon":
		if err := json.Unmarshal(g.Coordinates, &rings); err != nil {
			return nil, err
		}
	case "MultiPolygon":
		var polys [][][][2]float64
		if err := json.Unmarshal(g.Coordinates, &polys); err != nil {
			return nil, err
		}
		for _, p := range polys {
			rings = append(rings, p...)
		}
	default:
		return nil, fmt.Errorf("unsupported geometry type %q", g.Type)
	}
	var out polyclip.Polygon
	for _, r := range rings {
		out = append(out, toContour(r))
	}
	return out, nil
}

func toContour(r ring) polyclip.Contour {
	c := make(polyclip.Contour, 0, len(r))
	for i, p := range r {
		if i == len(r)-1 && len(r) > 1 && p == r[0] {
			break // drop closing point
		}
		c = append(c, polyclip.Point{X: p[0], Y: p[1]})
	}
	return c
}

// area of a polygon with holes (even-odd: polyclip output contours of holes
// are oriented opposite to outer contours, but we can't rely on it for input,
// so we use the absolute signed sum per contour and subtract contained ones).
func area(p polyclip.Polygon) float64 {
	total := 0.0
	for i, c := range p {
		a := math.Abs(signedArea(c))
		if isHole(p, i) {
			total -= a
		} else {
			total += a
		}
	}
	return total
}

// isHole reports whether contour i lies inside an odd number of other contours.
func isHole(p polyclip.Polygon, i int) bool {
	if len(p[i]) == 0 {
		return false
	}
	pt := p[i][0]
	n := 0
	for j, c := range p {
		if j != i && pointInContour(pt, c) {
			n++
		}
	}
	return n%2 == 1
}

func signedArea(c polyclip.Contour) float64 {
	s := 0.0
	for i := range c {
		j := (i + 1) % len(c)
		s += c[i].X*c[j].Y - c[j].X*c[i].Y
	}
	return s / 2
}

func pointInContour(p polyclip.Point, c polyclip.Contour) bool {
	in := false
	for i, j := 0, len(c)-1; i < len(c); j, i = i, i+1 {
		a, b := c[i], c[j]
		if (a.Y > p.Y) != (b.Y > p.Y) && p.X < (b.X-a.X)*(p.Y-a.Y)/(b.Y-a.Y)+a.X {
			in = !in
		}
	}
	return in
}

// intersectionArea returns the area of a ∩ b.
func intersectionArea(a, b polyclip.Polygon) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	ba, bb := a.BoundingBox(), b.BoundingBox()
	if !ba.Overlaps(bb) {
		return 0
	}
	return area(a.Construct(polyclip.INTERSECTION, b))
}

// minDistance returns the smallest distance between the edges of a and b.
// Returns 0 if they overlap.
func minDistance(a, b polyclip.Polygon) float64 {
	if intersectionArea(a, b) > 1e-9 {
		return 0
	}
	best := math.Inf(1)
	for _, ca := range a {
		for _, cb := range b {
			for i := range ca {
				p1, p2 := ca[i], ca[(i+1)%len(ca)]
				for j := range cb {
					q1, q2 := cb[j], cb[(j+1)%len(cb)]
					best = math.Min(best, segmentDistance(p1, p2, q1, q2))
				}
			}
		}
	}
	return best
}

func segmentDistance(p1, p2, q1, q2 polyclip.Point) float64 {
	if segmentsIntersect(p1, p2, q1, q2) {
		return 0
	}
	return math.Min(
		math.Min(pointSegment(p1, q1, q2), pointSegment(p2, q1, q2)),
		math.Min(pointSegment(q1, p1, p2), pointSegment(q2, p1, p2)),
	)
}

func pointSegment(p, a, b polyclip.Point) float64 {
	dx, dy := b.X-a.X, b.Y-a.Y
	l2 := dx*dx + dy*dy
	t := 0.0
	if l2 > 0 {
		t = math.Max(0, math.Min(1, ((p.X-a.X)*dx+(p.Y-a.Y)*dy)/l2))
	}
	return math.Hypot(p.X-(a.X+t*dx), p.Y-(a.Y+t*dy))
}

func cross(o, a, b polyclip.Point) float64 {
	return (a.X-o.X)*(b.Y-o.Y) - (a.Y-o.Y)*(b.X-o.X)
}

func segmentsIntersect(p1, p2, q1, q2 polyclip.Point) bool {
	d1, d2 := cross(q1, q2, p1), cross(q1, q2, p2)
	d3, d4 := cross(p1, p2, q1), cross(p1, p2, q2)
	return ((d1 > 0) != (d2 > 0)) && ((d3 > 0) != (d4 > 0))
}
