package rules

import (
	"fmt"
	"github.com/martin/archreg/engine/i18n"

	"github.com/ctessum/polyclip-go"
)

// EnvelopePart is one height limit of a plan as a volume: the land it
// applies to, minus prickmark and korsmark, extruded to the limit. The map
// draws these see-through, so a model that is too tall pokes out of them.
type EnvelopePart struct {
	// Polygons in SWEREF 99 TM, GeoJSON MultiPolygon layout.
	Polygons [][][][2]float64 `json:"polygons"`
	Height   float64          `json:"height"` // metres above the building's ground
	Kind     string           `json:"kind"`   // nockhojd, totalhojd, byggnadshojd
	Label    string           `json:"label"`  // "h1: max ridge height 16 m"
}

// Envelope returns the height limits of a plan as volumes, and notes about
// limits it could not draw. baseElevation (the ground's height above sea
// level, RH 2000) is needed for limits given "över nollplanet".
func Envelope(p *Plan, baseElevation *float64, l i18n.Lang) ([]EnvelopePart, []string) {
	if p == nil || p.PDFOnly || p.Boundary == nil {
		return nil, nil
	}
	boundary, err := p.Boundary.polygons()
	if err != nil || len(boundary) == 0 {
		return nil, nil
	}
	// Land that must stay free of main buildings.
	var blocked polyclip.Polygon
	for _, r := range p.Regulations {
		if k := classify(r); (k == kindPrickmark || k == kindKorsmark) && r.Geometry != nil {
			if g, err := r.Geometry.polygons(); err == nil {
				blocked = append(blocked, g...)
			}
		}
	}

	var out []EnvelopePart
	var notes []string
	for _, r := range p.Regulations {
		k := classify(r)
		if k != kindRidgeHeight && k != kindTotalHeight && k != kindBuildingHeight {
			continue
		}
		v, ok := regValue(r)
		if !ok || isMin(r) {
			continue
		}
		name := l.T(map[regKind]string{kindRidgeHeight: "max ridge height", kindTotalHeight: "max total height", kindBuildingHeight: "max building height (byggnadshöjd)"}[k])
		label := l.Dec(fmt.Sprintf("%s %g m", name, v))
		if isAbsoluteHeight(r) {
			if baseElevation == nil {
				notes = append(notes, l.F("“%s” is measured above sea level. Enter the ground level (Model › Project details) to see it in 3D.", r.Text))
				continue
			}
			label = l.F("%s +%g m above sea level (%g m above your ground)", name, v, v-*baseElevation)
			v -= *baseElevation
		}
		if r.Label != "" {
			label = r.Label + ": " + label
		}
		if v <= 0 {
			continue
		}
		area := boundary
		if r.Geometry != nil {
			if g, err := r.Geometry.polygons(); err == nil && len(g) > 0 {
				area = g.Construct(polyclip.INTERSECTION, boundary)
			}
		}
		if len(blocked) > 0 {
			area = area.Construct(polyclip.DIFFERENCE, blocked)
		}
		polys := groupRings(area)
		if len(polys) == 0 {
			continue
		}
		out = append(out, EnvelopePart{Polygons: polys, Height: v, Kind: string(k), Label: label})
	}
	return out, notes
}

// groupRings turns polyclip contours into GeoJSON polygons (outer ring,
// then the holes inside it), with closed rings.
func groupRings(p polyclip.Polygon) [][][][2]float64 {
	closed := func(c polyclip.Contour) [][2]float64 {
		r := make([][2]float64, 0, len(c)+1)
		for _, q := range c {
			r = append(r, [2]float64{q.X, q.Y})
		}
		return append(r, r[0])
	}
	var out [][][][2]float64
	var outers []int
	for i, c := range p {
		if len(c) >= 3 && !isHole(p, i) {
			outers = append(outers, i)
			out = append(out, [][][2]float64{closed(c)})
		}
	}
	for i, c := range p {
		if len(c) < 3 || !isHole(p, i) {
			continue
		}
		// Attach the hole to the smallest outer ring that contains it.
		best, bestArea := -1, 0.0
		for j, o := range outers {
			if a := signedArea(p[o]); pointInContour(c[0], p[o]) && (best < 0 || abs(a) < bestArea) {
				best, bestArea = j, abs(a)
			}
		}
		if best >= 0 {
			out[best] = append(out[best], closed(c))
		}
	}
	return out
}
