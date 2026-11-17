package geo

import (
	"math"
	"testing"
)

func TestSwerefRoundTrip(t *testing.T) {
	// Lantmäteriet's published test point for SWEREF 99 TM:
	// N 7453389.762, E 761594.784 ⇄ lat 67°0'0", lon 20°0'0"? Not available
	// offline, so check round trips and a known-ish Stockholm value instead.
	for _, c := range [][2]float64{{18.0686, 59.3293}, {11.97, 57.70}, {20.26, 67.85}, {13.0, 55.6}} {
		e, n := ToSweref(c[0], c[1])
		lon, lat := ToWGS84(e, n)
		if math.Abs(lon-c[0]) > 1e-8 || math.Abs(lat-c[1]) > 1e-8 {
			t.Errorf("%v → (%f,%f) → (%f,%f)", c, e, n, lon, lat)
		}
	}
	// Stockholm city centre is about E 674 000, N 6 580 000.
	e, n := ToSweref(18.0686, 59.3293)
	if math.Abs(e-674000) > 2000 || math.Abs(n-6580800) > 2000 {
		t.Errorf("Stockholm: %f %f", e, n)
	}
	// On the central meridian easting is exactly 500 000.
	if e, _ := ToSweref(15, 62); math.Abs(e-500000) > 1e-6 {
		t.Errorf("central meridian easting %f", e)
	}
}

func TestHullAndWidth(t *testing.T) {
	pts := []Pt{{0, 0}, {4, 0}, {4, 2}, {0, 2}, {2, 1}, {1, 1}}
	h := ConvexHull(pts)
	if len(h) != 4 || Area(h) != 8 {
		t.Errorf("hull %v area %v", h, Area(h))
	}
	if w := MinWidth(pts); math.Abs(w-2) > 1e-9 {
		t.Errorf("min width %v", w)
	}
	r := Transform([]Pt{{1, 0}}, 90, 10, 0)
	if math.Abs(r[0][0]-10) > 1e-9 || math.Abs(r[0][1]-1) > 1e-9 {
		t.Errorf("transform %v", r)
	}
	if !PointInPolygon(Pt{1, 1}, [][]Pt{{{0, 0}, {4, 0}, {4, 2}, {0, 2}}}) {
		t.Error("point in polygon")
	}
}
