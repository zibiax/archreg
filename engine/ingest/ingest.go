// Package ingest turns raw geometry from the model loaders (web-ifc for IFC,
// rhino3dm for .3dm, or the built-in sample) into the simplified model the
// rules engine checks: footprint, heights, storeys, rooms, windows, doors.
//
// The JS side does as little as possible: it streams triangles and a few
// attributes per element. Everything else is decided here.
package ingest

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"

	"github.com/martin/archreg/engine/geo"
	"github.com/martin/archreg/engine/rules"
)

// Raw is what a loader sends.
type Raw struct {
	Source   string `json:"source"` // "ifc", "3dm", "sample"
	FileName string `json:"fileName"`
	// YUp: positions are three.js world coordinates (x, y up, z towards the
	// viewer = south). web-ifc produces these. Otherwise x east, y north, z up.
	YUp bool `json:"yUp"`
	// UnitScale converts attribute lengths (OverallWidth, Elevation, ...) to
	// metres. Geometry is assumed to be in metres already for IFC (web-ifc
	// applies the scale), and is multiplied by GeomScale otherwise.
	UnitScale float64 `json:"unitScale"`
	GeomScale float64 `json:"geomScale"`
	// RhinoUnitSystem is rhino3dm's UnitSystem value, if Source is "3dm".
	RhinoUnitSystem int `json:"rhinoUnitSystem"`

	Elements      []RawElement   `json:"elements"`
	MapConversion *MapConversion `json:"mapConversion,omitempty"`
}

// RawElement is one element. Off/Len index triangle soup positions
// (9 floats per triangle) in the shared positions buffer.
type RawElement struct {
	ID         string         `json:"id"`
	Kind       string         `json:"kind"` // space, window, door, storey, roof, slab, wall, transport, site, other
	IfcType    string         `json:"ifcType,omitempty"`
	Name       string         `json:"name,omitempty"`
	LongName   string         `json:"longName,omitempty"`
	Layer      string         `json:"layer,omitempty"`
	Storey     string         `json:"storey,omitempty"`
	Attrs      map[string]any `json:"attrs,omitempty"`
	Off        int            `json:"off"`
	Len        int            `json:"len"`
	BBox       []float64      `json:"bbox,omitempty"` // minx,miny,minz,maxx,maxy,maxz (3dm), model units
	Predefined string         `json:"predefined,omitempty"`
	ObjectType string         `json:"objectType,omitempty"`
}

// MapConversion is IfcMapConversion, for georeferenced IFC files.
type MapConversion struct {
	Eastings      float64 `json:"eastings"`
	Northings     float64 `json:"northings"`
	Height        float64 `json:"height"`
	XAxisAbscissa float64 `json:"xAxisAbscissa"`
	XAxisOrdinate float64 `json:"xAxisOrdinate"`
	Scale         float64 `json:"scale"`
	CRS           string  `json:"crs"`
}

// Building is the simplified model in local coordinates: metres, x east,
// y north, z up, with the ground floor at z = 0.
type Building struct {
	Source          string          `json:"source"`
	FileName        string          `json:"fileName"`
	Footprint       []geo.Pt        `json:"footprint"`
	FootprintMethod string          `json:"footprintMethod"`
	TotalHeight     float64         `json:"totalHeight"`
	RidgeHeight     float64         `json:"ridgeHeight"`
	RidgeFromRoof   bool            `json:"ridgeFromRoof"`
	Storeys         int             `json:"storeys"`
	GrossFloorArea  *float64        `json:"grossFloorArea,omitempty"`
	Elevators       int             `json:"elevators"`
	GroundZ         float64         `json:"groundZ"` // original z of the ground floor
	Rooms           []rules.Element `json:"rooms"`
	Windows         []rules.Element `json:"windows"`
	Doors           []rules.Element `json:"doors"`
	Georef          *Georef         `json:"georef,omitempty"`
	Warnings        []string        `json:"warnings,omitempty"`
	Layers          []string        `json:"layers,omitempty"` // 3dm layer names
	Center          geo.Pt          `json:"center"`           // footprint centroid (local), placement pivot
}

// Georef places the local model in SWEREF 99 TM directly.
type Georef struct {
	E, N        float64
	RotationDeg float64
}

type elem struct {
	raw  RawElement
	pts  [][3]float64 // triangle soup, local z-up metres
	min  [3]float64
	max  [3]float64
	tris int
}

// Build converts a Raw model and its positions buffer.
func Build(raw Raw, positions []float32, layerKinds map[string]string) (*Building, error) {
	if raw.UnitScale == 0 {
		raw.UnitScale = 1
	}
	if raw.GeomScale == 0 {
		raw.GeomScale = 1
	}
	if raw.Source == "3dm" {
		raw.GeomScale = rhinoScale(raw.RhinoUnitSystem)
		raw.UnitScale = raw.GeomScale
	}

	b := &Building{Source: raw.Source, FileName: raw.FileName}
	var els []*elem
	layerSet := map[string]bool{}
	for _, re := range raw.Elements {
		if re.Layer != "" {
			layerSet[re.Layer] = true
			if k, ok := layerKinds[re.Layer]; ok && k != "" {
				re.Kind = k
			} else if re.Kind == "" || re.Kind == "other" {
				re.Kind = guessLayerKind(re.Layer)
			}
		}
		if re.Kind == "slab" && strings.EqualFold(re.Predefined, "ROOF") {
			re.Kind = "roof"
		}
		e := &elem{raw: re, min: [3]float64{math.Inf(1), math.Inf(1), math.Inf(1)}, max: [3]float64{math.Inf(-1), math.Inf(-1), math.Inf(-1)}}
		if re.Len > 0 {
			if re.Off < 0 || re.Off+re.Len > len(positions) || re.Len%9 != 0 {
				return nil, fmt.Errorf("element %s: bad position range", re.ID)
			}
			for i := re.Off; i < re.Off+re.Len; i += 3 {
				e.add(raw.point(positions[i], positions[i+1], positions[i+2]))
			}
			e.tris = re.Len / 9
		} else if len(re.BBox) == 6 {
			s := raw.GeomScale
			bb := re.BBox
			for _, c := range [][3]float64{{bb[0], bb[1], bb[2]}, {bb[3], bb[4], bb[5]}, {bb[0], bb[4], bb[2]}, {bb[3], bb[1], bb[5]},
				{bb[0], bb[1], bb[5]}, {bb[3], bb[4], bb[2]}, {bb[0], bb[4], bb[5]}, {bb[3], bb[1], bb[2]}} {
				e.add([3]float64{c[0] * s, c[1] * s, c[2] * s})
			}
		}
		els = append(els, e)
	}
	for l := range layerSet {
		b.Layers = append(b.Layers, l)
	}
	sort.Strings(b.Layers)

	// Storeys and ground level.
	type storey struct {
		id   string
		elev float64
	}
	var storeys []storey
	storeyElev := map[string]float64{}
	for _, e := range els {
		if e.raw.Kind != "storey" {
			continue
		}
		if v, ok := num(e.raw.Attrs, "Elevation"); ok {
			storeys = append(storeys, storey{e.raw.ID, v * raw.UnitScale})
			storeyElev[e.raw.ID] = v * raw.UnitScale
		}
	}
	sort.Slice(storeys, func(i, j int) bool { return storeys[i].elev < storeys[j].elev })

	var buildingPts []geo.Pt
	minZ, maxZ := math.Inf(1), math.Inf(-1)
	roofMax := math.Inf(-1)
	for _, e := range els {
		switch e.raw.Kind {
		case "space", "site", "storey":
			continue
		}
		if e.empty() {
			continue
		}
		for _, p := range e.pts {
			buildingPts = append(buildingPts, geo.Pt{p[0], p[1]})
		}
		minZ = math.Min(minZ, e.min[2])
		maxZ = math.Max(maxZ, e.max[2])
		if e.raw.Kind == "roof" {
			roofMax = math.Max(roofMax, e.max[2])
		}
	}
	if len(buildingPts) < 3 {
		return nil, fmt.Errorf("the model has no building geometry")
	}

	ground := minZ
	if len(storeys) > 0 {
		// The ground floor is the storey closest to the lowest geometry that
		// isn't a basement: pick the storey with elevation nearest 0 when the
		// lowest one is clearly below ground, else the lowest one.
		g := storeys[0].elev
		if g < -1.5 {
			best := math.Inf(1)
			for _, s := range storeys {
				if math.Abs(s.elev) < best {
					best, g = math.Abs(s.elev), s.elev
				}
			}
		}
		if math.Abs(g-minZ) < 5 { // sanity: storey elevations in the same frame as geometry
			ground = g
		}
		above := 0
		for _, s := range storeys {
			if s.elev >= ground-0.3 && s.elev < maxZ-1.0 {
				above++
			}
		}
		b.Storeys = above
	} else {
		b.Warnings = append(b.Warnings, "No storeys in the model; number of storeys is estimated from height.")
		b.Storeys = int(math.Max(1, math.Round((maxZ-minZ)/3.0)))
	}
	b.GroundZ = ground

	hull := geo.ConvexHull(buildingPts)
	b.Footprint = hull
	b.FootprintMethod = "convexHull"
	b.Center = centroid(hull)
	b.TotalHeight = round(maxZ-ground, 2)
	if !math.IsInf(roofMax, -1) {
		b.RidgeHeight = round(roofMax-ground, 2)
		b.RidgeFromRoof = true
	} else {
		b.RidgeHeight = b.TotalHeight
		b.Warnings = append(b.Warnings, "No roof elements found; ridge height (nockhöjd) is taken as the total height.")
	}

	var gfa float64
	gfaN := 0
	for _, e := range els {
		if e.raw.Kind == "storey" {
			if v, ok := num(e.raw.Attrs, "GrossFloorArea"); ok {
				gfa += v
				gfaN++
			}
		}
	}
	if gfaN > 0 {
		b.GrossFloorArea = &gfa
	}

	// Finished floor level per storey: the top of that storey's slabs
	// (within 0.6 m above the storey elevation), else the elevation.
	floorTop := map[string]float64{}
	for id, z := range storeyElev {
		floorTop[id] = z
	}
	for _, e := range els {
		if e.raw.Kind != "slab" || e.empty() {
			continue
		}
		if z, ok := storeyElev[e.raw.Storey]; ok && e.max[2] > floorTop[e.raw.Storey] && e.max[2] <= z+0.6 {
			floorTop[e.raw.Storey] = e.max[2]
		}
	}

	for _, e := range els {
		switch e.raw.Kind {
		case "space":
			if r := room(e, raw.UnitScale); r != nil {
				b.Rooms = append(b.Rooms, r)
			}
		case "window", "door":
			el := opening(e, raw.UnitScale, floorTop, ground)
			if e.raw.Kind == "window" {
				b.Windows = append(b.Windows, el)
			} else {
				b.Doors = append(b.Doors, el)
			}
		case "transport":
			if p := strings.ToUpper(e.raw.Predefined); p == "" || p == "ELEVATOR" || p == "NOTDEFINED" || p == "USERDEFINED" {
				b.Elevators++
			}
		}
	}

	if mc := raw.MapConversion; mc != nil && strings.Contains(strings.ToUpper(mc.CRS), "3006") {
		rot := 0.0
		if mc.XAxisAbscissa != 0 || mc.XAxisOrdinate != 0 {
			rot = math.Atan2(mc.XAxisOrdinate, mc.XAxisAbscissa) * 180 / math.Pi
		}
		b.Georef = &Georef{E: mc.Eastings, N: mc.Northings, RotationDeg: rot}
	} else if mc != nil {
		b.Warnings = append(b.Warnings, fmt.Sprintf("The IFC is georeferenced in %q, not SWEREF 99 TM, so it has to be placed by hand.", mc.CRS))
	}
	if len(b.Rooms) == 0 {
		b.Warnings = append(b.Warnings, "No rooms (IfcSpace) in the model, so room checks can't run. Export rooms/spaces from your program.")
	}
	return b, nil
}

func (r Raw) point(x, y, z float32) [3]float64 {
	s := r.GeomScale
	if r.YUp {
		// three.js / web-ifc: x east, y up, z = -north.
		return [3]float64{float64(x) * s, -float64(z) * s, float64(y) * s}
	}
	return [3]float64{float64(x) * s, float64(y) * s, float64(z) * s}
}

func (e *elem) add(p [3]float64) {
	e.pts = append(e.pts, p)
	for i := 0; i < 3; i++ {
		e.min[i] = math.Min(e.min[i], p[i])
		e.max[i] = math.Max(e.max[i], p[i])
	}
}

func (e *elem) empty() bool { return len(e.pts) == 0 }

func (e *elem) xy() []geo.Pt {
	out := make([]geo.Pt, len(e.pts))
	for i, p := range e.pts {
		out[i] = geo.Pt{p[0], p[1]}
	}
	return out
}

// floorArea sums the plan-projected area of downward-facing triangles, which
// is the floor area of a prismatic space.
func (e *elem) floorArea() float64 {
	if e.tris == 0 {
		return (e.max[0] - e.min[0]) * (e.max[1] - e.min[1])
	}
	a := 0.0
	for i := 0; i+2 < len(e.pts); i += 3 {
		p, q, r := e.pts[i], e.pts[i+1], e.pts[i+2]
		ux, uy, uz := q[0]-p[0], q[1]-p[1], q[2]-p[2]
		vx, vy, vz := r[0]-p[0], r[1]-p[1], r[2]-p[2]
		nx, ny, nz := uy*vz-uz*vy, uz*vx-ux*vz, ux*vy-uy*vx
		l := math.Sqrt(nx*nx + ny*ny + nz*nz)
		if l == 0 || nz/l > -0.9 {
			continue
		}
		a += -nz / 2
	}
	return a
}

func room(e *elem, unit float64) rules.Element {
	if e.empty() {
		return nil
	}
	name := firstNonEmpty(e.raw.LongName, e.raw.Name, e.raw.ID)
	r := rules.Element{
		"id":   e.raw.ID,
		"name": name,
		"type": RoomType(e.raw.LongName + " " + e.raw.Name + " " + e.raw.ObjectType),
	}
	area := e.floorArea()
	if v, ok := num(e.raw.Attrs, "NetFloorArea"); ok && v > 0 {
		area = v // quantity sets are in m² already
	}
	r["area"] = round(area, 2)
	h := e.max[2] - e.min[2]
	if v, ok := num(e.raw.Attrs, "FinishCeilingHeight"); ok && v > 0 {
		h = v * unit
	}
	r["ceilingHeight"] = round(h, 2)
	r["minDimension"] = round(geo.MinWidth(e.xy()), 2)
	r["floorZ"] = round(e.min[2], 2)
	return r
}

func opening(e *elem, unit float64, floorTop map[string]float64, ground float64) rules.Element {
	kind := e.raw.Kind
	el := rules.Element{"id": e.raw.ID, "name": firstNonEmpty(e.raw.Name, e.raw.ObjectType, e.raw.ID)}
	w, okW := num(e.raw.Attrs, "OverallWidth")
	h, okH := num(e.raw.Attrs, "OverallHeight")
	if okW && w > 0 {
		el["width"] = round(w*unit, 3)
	} else if !e.empty() {
		el["width"] = round(geo.Diameter(e.xy()), 3)
	}
	if okH && h > 0 {
		el["height"] = round(h*unit, 3)
	} else if !e.empty() {
		el["height"] = round(e.max[2]-e.min[2], 3)
	}
	if v, ok := boolAttr(e.raw.Attrs, "IsExternal"); ok {
		el["isExternal"] = v
	}
	if v, ok := boolAttr(e.raw.Attrs, "escape"); ok {
		el["escape"] = v
	}
	if kind == "window" && !e.empty() {
		if z, ok := floorTop[e.raw.Storey]; ok {
			el["sillHeight"] = round(e.min[2]-z, 2)
		}
	}
	if !e.empty() {
		el["z"] = round(e.min[2]-ground, 2)
	}
	// IFC has no standard "clear width"; some exporters write it.
	for _, k := range []string{"ClearWidth", "FriBredd"} {
		if v, ok := num(e.raw.Attrs, k); ok && v > 0 {
			el["clearWidth"] = round(v*unit, 3)
		}
	}
	return el
}

var roomWords = []struct {
	re   *regexp.Regexp
	kind string
}{
	{regexp.MustCompile(`(?i)\b(bad|badrum|wc|toa|toalett|dusch|hygien|bathroom|toilet|restroom|shower|badezimmer)`), "bathroom"},
	{regexp.MustCompile(`(?i)(sovrum|sov\b|bedroom|bed\b|schlafzimmer|kinderzimmer)`), "bedroom"},
	{regexp.MustCompile(`(?i)(kök|kokvrå|kitchen|pentry|küche)`), "kitchen"},
	{regexp.MustCompile(`(?i)(vardagsrum|allrum|vardag|living|lounge|samvaro|matplats|dining|wohnen|wohnzimmer|essen)`), "living"},
	{regexp.MustCompile(`(?i)(hall|entré|korridor|corridor|trapphus|stair|kommunikation|flur|diele|treppe)`), "circulation"},
	{regexp.MustCompile(`(?i)(förråd|klk|klädkammare|storage|closet|städ)`), "storage"},
	{regexp.MustCompile(`(?i)(tvätt|laundry)`), "laundry"},
	{regexp.MustCompile(`(?i)(teknik|el-?central|fläkt|undercentral|technical|plant)`), "technical"},
}

// RoomTypes the UI lets the user choose from.
var RoomTypes = []string{"living", "bedroom", "kitchen", "bathroom", "circulation", "storage", "laundry", "technical", "other"}

// RoomType classifies a room from its name (Swedish or English).
func RoomType(s string) string {
	for _, w := range roomWords {
		if w.re.MatchString(s) {
			return w.kind
		}
	}
	return "other"
}

func guessLayerKind(layer string) string {
	l := strings.ToLower(layer)
	switch {
	case strings.Contains(l, "fönster") || strings.Contains(l, "window"):
		return "window"
	case strings.Contains(l, "dörr") || strings.Contains(l, "door"):
		return "door"
	case strings.Contains(l, "rum") || strings.Contains(l, "room") || strings.Contains(l, "space"):
		return "space"
	case strings.Contains(l, "tak") || strings.Contains(l, "roof"):
		return "roof"
	case strings.Contains(l, "mark") || strings.Contains(l, "terrain") || strings.Contains(l, "site") || strings.Contains(l, "tomt"):
		return "site"
	}
	return "other"
}

// rhinoScale maps rhino3dm UnitSystem values to metres.
func rhinoScale(u int) float64 {
	switch u {
	case 1: // Microns
		return 1e-6
	case 2: // Millimeters
		return 0.001
	case 3: // Centimeters
		return 0.01
	case 4: // Meters
		return 1
	case 5: // Kilometers
		return 1000
	case 8: // Inches
		return 0.0254
	case 9: // Feet
		return 0.3048
	}
	return 1
}

func num(m map[string]any, k string) (float64, bool) {
	switch v := m[k].(type) {
	case float64:
		return v, true
	case map[string]any: // web-ifc {type, value}
		if f, ok := v["value"].(float64); ok {
			return f, true
		}
	}
	return 0, false
}

func boolAttr(m map[string]any, k string) (bool, bool) {
	switch v := m[k].(type) {
	case bool:
		return v, true
	case string:
		u := strings.ToUpper(strings.Trim(v, "."))
		return u == "T" || u == "TRUE", u == "T" || u == "TRUE" || u == "F" || u == "FALSE"
	case map[string]any:
		return boolAttr(map[string]any{"x": v["value"]}, "x")
	}
	return false, false
}

func firstNonEmpty(s ...string) string {
	for _, x := range s {
		if strings.TrimSpace(x) != "" {
			return strings.TrimSpace(x)
		}
	}
	return ""
}

func centroid(r []geo.Pt) geo.Pt {
	var x, y float64
	for _, p := range r {
		x += p[0]
		y += p[1]
	}
	if len(r) == 0 {
		return geo.Pt{}
	}
	return geo.Pt{x / float64(len(r)), y / float64(len(r))}
}

func round(v float64, d int) float64 {
	p := math.Pow(10, float64(d))
	return math.Round(v*p) / p
}
