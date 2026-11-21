package ingest

import (
	"github.com/martin/archreg/engine/geo"
	"github.com/martin/archreg/engine/rules"
)

// Placement puts the building's footprint centre at (E, N) in SWEREF 99 TM,
// rotated RotationDeg counter-clockwise.
type Placement struct {
	E           float64 `json:"e"`
	N           float64 `json:"n"`
	RotationDeg float64 `json:"rotationDeg"`
}

// DefaultPlacement uses the IFC's georeference if it has one.
func (b *Building) DefaultPlacement() (Placement, bool) {
	if b.Georef == nil {
		return Placement{}, false
	}
	c := geo.Transform([]geo.Pt{b.Center}, b.Georef.RotationDeg, b.Georef.E, b.Georef.N)[0]
	return Placement{E: c[0], N: c[1], RotationDeg: b.Georef.RotationDeg}, true
}

// PlacedFootprint returns the footprint in SWEREF 99 TM.
func (b *Building) PlacedFootprint(p Placement) []geo.Pt {
	local := make([]geo.Pt, len(b.Footprint))
	for i, q := range b.Footprint {
		local[i] = geo.Pt{q[0] - b.Center[0], q[1] - b.Center[1]}
	}
	return geo.Transform(local, p.RotationDeg, p.E, p.N)
}

// Model builds the engine input. building holds the user's project facts.
func (b *Building) Model(p *Placement, building rules.Element, baseElevation *float64) rules.Model {
	m := rules.Model{
		FootprintMethod: b.FootprintMethod,
		Rooms:           b.Rooms,
		Windows:         b.Windows,
		Doors:           b.Doors,
		GrossFloorArea:  b.GrossFloorArea,
		BaseElevation:   baseElevation,
		Building:        rules.Element{},
	}
	if p != nil {
		for _, q := range b.PlacedFootprint(*p) {
			m.Footprint = append(m.Footprint, [2]float64(q))
		}
	}
	th, rh, st := b.TotalHeight, b.RidgeHeight, b.Storeys
	m.TotalHeight, m.RidgeHeight, m.Storeys = &th, &rh, &st
	m.Building["elevators"] = float64(b.Elevators)
	for k, v := range building {
		m.Building[k] = v
	}
	return m
}

// EffectiveGeomScale is the factor from a loader's raw positions to metres, as
// Build applies it.
func (r Raw) EffectiveGeomScale() float64 {
	if r.Source == "3dm" {
		return rhinoScale(r.RhinoUnitSystem)
	}
	if r.GeomScale == 0 {
		return 1
	}
	return r.GeomScale
}

// MapFrame says where the building's local metres land on the map: the
// WGS84 position of its placement centre at ground level, and of points one
// metre along local +x and +y. Three points are enough for the map to build
// an affine transform; over a building's size the projection is linear.
func (b *Building) MapFrame(p Placement) (origin, east, north [2]float64) {
	pts := geo.Transform([]geo.Pt{{0, 0}, {1, 0}, {0, 1}}, p.RotationDeg, p.E, p.N)
	ll := func(q geo.Pt) [2]float64 { lon, lat := geo.ToWGS84(q[0], q[1]); return [2]float64{lon, lat} }
	origin, east, north = ll(pts[0]), ll(pts[1]), ll(pts[2])
	return
}
