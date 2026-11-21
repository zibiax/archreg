package app

import (
	"bytes"
	"embed"
	"fmt"
	"github.com/martin/archreg/engine/i18n"
	"html/template"
	"math"
	"strings"

	"github.com/martin/archreg/engine/geo"
	"github.com/martin/archreg/engine/ingest"
	"github.com/martin/archreg/engine/plans"
	"github.com/martin/archreg/engine/rules"
)

// View is everything the JS side needs to update the page.
type View struct {
	Panel string   `json:"panel"`
	Chat  string   `json:"chat"`
	Tab   string   `json:"tab"`
	Map   MapView  `json:"map"`
	Model *Model3D `json:"model,omitempty"`
	Focus string   `json:"focus,omitempty"` // "map" or "3d"
	Error string   `json:"error,omitempty"`
	Lang  string   `json:"lang"` // "en" or "sv"
}

// MapView holds GeoJSON for the map layers.
type MapView struct {
	Pin       []float64      `json:"pin,omitempty"`
	Plans     map[string]any `json:"plans"`
	Regs      map[string]any `json:"regs"`
	Footprint map[string]any `json:"footprint"`
	FlyTo     []float64      `json:"flyTo,omitempty"`
	// Envelope: the selected plan's height limits as volumes (GeoJSON with
	// a "height" property, metres above ground) for the 3D map.
	Envelope     map[string]any `json:"envelope"`
	EnvelopeNote string         `json:"envelopeNote,omitempty"`
	// Place puts the 3D model on the map; nil when it isn't placed.
	Place *ModelPlace `json:"place,omitempty"`
}

// ModelPlace maps raw loader positions to the map:
//
//	local = scale·raw (y and z swapped first if yUp) − (cx, cy, groundZ)
//	world = origin + local.x·(east − origin) + local.y·(north − origin) + up
//
// origin/east/north are WGS84; the map converts them to its own projection.
type ModelPlace struct {
	Origin  [2]float64 `json:"origin"`
	East    [2]float64 `json:"east"`
	North   [2]float64 `json:"north"`
	Scale   float64    `json:"scale"`
	YUp     bool       `json:"yUp"`
	CX      float64    `json:"cx"`
	CY      float64    `json:"cy"`
	GroundZ float64    `json:"groundZ"`
	// Rotation in degrees and Radius (metres, centre to farthest corner)
	// place the rotate handle.
	Rotation float64 `json:"rotation"`
	Radius   float64 `json:"radius"`
}

// Model3D tells the viewer what to show.
type Model3D struct {
	Rebuild   bool     `json:"rebuild"` // (re)build meshes from the last loaded geometry
	Clear     bool     `json:"clear"`
	Highlight []string `json:"highlight"`
	Fit       bool     `json:"fit"`
}

func fc(features []map[string]any) map[string]any {
	if features == nil {
		features = []map[string]any{}
	}
	return map[string]any{"type": "FeatureCollection", "features": features}
}

// view renders the current state. Caller holds the lock.
func (a *App) view() *View {
	v := &View{Tab: a.Tab, Error: a.lastError, Lang: string(a.Lang)}

	// Map.
	if a.Pin != nil {
		v.Map.Pin = []float64{a.Pin.Lon, a.Pin.Lat}
	}
	v.Map.FlyTo, a.flyTo = a.flyTo, nil
	if a.toast != "" && v.Error == "" {
		v.Error = a.toast
	}
	a.toast = ""
	sel := a.plan()
	var pf []map[string]any
	seen := map[string]bool{}
	for _, p := range append(append([]*rules.Plan{}, a.Outlines...), a.Plans...) {
		if seen[p.ID] {
			continue
		}
		seen[p.ID] = true
		pf = append(pf, map[string]any{"type": "Feature", "geometry": plans.ToWGS84(p.Boundary),
			"properties": map[string]any{"id": p.ID, "selected": sel != nil && p.ID == sel.ID, "pdfOnly": p.PDFOnly}})
	}
	v.Map.Plans = fc(pf)
	var rf []map[string]any
	if sel != nil {
		for _, r := range sel.Regulations {
			if r.Geometry == nil {
				continue
			}
			kind, _ := rules.Describe(r, a.Lang)
			rf = append(rf, map[string]any{"type": "Feature", "geometry": plans.ToWGS84(r.Geometry),
				"properties": map[string]any{"id": r.ID, "kind": kind, "label": r.Label}})
		}
	}
	v.Map.Regs = fc(rf)
	var ff []map[string]any
	if a.Building != nil && a.Placement != nil {
		ring := a.Building.PlacedFootprint(*a.Placement)
		var coords [][2]float64
		for _, p := range append(ring, ring[0]) {
			lon, lat := geo.ToWGS84(p[0], p[1])
			coords = append(coords, [2]float64{lon, lat})
		}
		highlight := false
		if r := a.selectedResult(); r != nil {
			highlight = r.Group == rules.GroupPlan && r.Status != rules.Pass
		}
		ff = append(ff, map[string]any{"type": "Feature",
			"geometry":   map[string]any{"type": "Polygon", "coordinates": [][][2]float64{coords}},
			"properties": map[string]any{"highlight": highlight, "state": a.placementState()}})
	}
	v.Map.Footprint = fc(ff)

	// Height limits as volumes, and where the model sits.
	var ef []map[string]any
	if sel != nil {
		parts, notes := rules.Envelope(sel, a.Project.BaseElevation, a.Lang)
		for _, p := range parts {
			var mp [][][][2]float64
			for _, poly := range p.Polygons {
				var rings [][][2]float64
				for _, ring := range poly {
					var r [][2]float64
					for _, q := range ring {
						lon, lat := geo.ToWGS84(q[0], q[1])
						r = append(r, [2]float64{lon, lat})
					}
					rings = append(rings, r)
				}
				mp = append(mp, rings)
			}
			ef = append(ef, map[string]any{"type": "Feature",
				"geometry":   map[string]any{"type": "MultiPolygon", "coordinates": mp},
				"properties": map[string]any{"height": p.Height, "kind": p.Kind, "label": p.Label}})
		}
		v.Map.EnvelopeNote = strings.Join(notes, " ")
	}
	v.Map.Envelope = fc(ef)
	if a.Building != nil && a.Placement != nil && a.raw != nil {
		o, e, n := a.Building.MapFrame(*a.Placement)
		v.Map.Place = &ModelPlace{Origin: o, East: e, North: n, Scale: a.raw.EffectiveGeomScale(), YUp: a.raw.YUp,
			CX: a.Building.Center[0], CY: a.Building.Center[1], GroundZ: a.Building.GroundZ,
			Rotation: a.Placement.RotationDeg}
		for _, q := range a.Building.Footprint {
			v.Map.Place.Radius = math.Max(v.Map.Place.Radius, math.Hypot(q[0]-a.Building.Center[0], q[1]-a.Building.Center[1]))
		}
	}

	// 3D.
	if a.Building != nil || a.newMesh {
		m := &Model3D{Rebuild: a.newMesh && a.Building != nil, Clear: a.Building == nil, Fit: a.fitModel, Highlight: []string{}}
		// Highlight every element in the selected row (all three windows).
		for _, r := range a.selectedIssue() {
			if r.Group != rules.GroupRegulations {
				continue
			}
			for _, id := range r.Elements {
				if id != "building" {
					m.Highlight = append(m.Highlight, id)
				}
			}
		}
		v.Model = m
		a.newMesh, a.fitModel = false, false
	}
	v.Focus, a.focus = a.focus, ""

	var buf bytes.Buffer
	tmpl, ok := tmpls[a.Lang]
	if !ok {
		a.Lang, tmpl = i18n.EN, tmpls[i18n.EN]
	}
	if err := tmpl.ExecuteTemplate(&buf, "panel", a.panelData()); err != nil {
		v.Panel = "<p class=error>Template error: " + template.HTMLEscapeString(err.Error()) + "</p>"
	} else {
		v.Panel = buf.String()
	}
	buf.Reset()
	tmpl.ExecuteTemplate(&buf, "chat", a.Chat)
	v.Chat = buf.String()
	return v
}

// placementState sums up the checks that depend on where the building
// stands (inside the plan, prickmark, korsmark, distance to boundary), so
// the footprint can be coloured while it is dragged: "conflict" if any
// fails (or the building measurably stands on prickmark), "unsure" if any
// can't be decided, else "ok".
func (a *App) placementState() string {
	if a.Output == nil {
		return "unsure"
	}
	state := "ok"
	for _, r := range a.Output.Results {
		switch r.RuleID {
		case "dp.withinPlan", "dp.prickmark", "dp.korsmark", "dp.avstand":
		default:
			continue
		}
		// A measured prickmark overlap is shown red even when the result
		// says "check manually" only because the outline is approximate;
		// the Results tab explains the uncertainty.
		if r.RuleID == "dp.prickmark" && r.Measured != nil && *r.Measured > 0 {
			return "conflict"
		}
		switch r.Status {
		case rules.Fail:
			return "conflict"
		case rules.Manual, rules.Missing:
			state = "unsure"
		}
	}
	if len(a.Plans) == 0 {
		return "unsure"
	}
	return state
}

func (a *App) selectedResult() *rules.Result {
	if a.Output != nil && a.Selected >= 0 && a.Selected < len(a.Output.Results) {
		return &a.Output.Results[a.Selected]
	}
	return nil
}

// --- template data --------------------------------------------------------

type regRow struct {
	Label, Plain, Short, Text, Kind string
}

type panelData struct {
	Tab        string
	Index      *plans.Index
	IdxErr     error
	Pin        *Pin
	Plans      []*rules.Plan
	PlanIdx    int
	Plan       *rules.Plan
	Regs       []regRow
	ByDate     string
	B          *ingest.Building
	Place      *ingest.Placement
	PlaceSW    string
	FootArea   float64
	P          Project
	Out        *rules.Output
	Sections   []issueSection
	FailCount  int // rows (not results) that must be fixed
	NeedsInfo  bool
	Counts     map[rules.Status]int
	RoomTypes  []string
	LayerKinds map[string]string
	Uses       [][2]string
	Busy       string
}

func (a *App) panelData() panelData {
	d := panelData{Tab: a.Tab, Index: a.index, IdxErr: a.idxErr, Pin: a.Pin, Plans: a.Plans, PlanIdx: a.PlanIdx,
		B: a.Building, Place: a.Placement, P: a.Project, Out: a.Output, RoomTypes: ingest.RoomTypes, LayerKinds: a.layerKinds,
		Uses: [][2]string{{"house", a.Lang.T("One- or two-family house (en-/tvåbostadshus)")}, {"apartments", a.Lang.T("Apartment building (flerbostadshus)")}, {"nonResidential", a.Lang.T("Non-residential (lokalbyggnad)")}}}
	if p := a.plan(); p != nil {
		d.Plan = p
		for _, r := range p.Regulations {
			kind, plain := rules.Describe(r, a.Lang)
			d.Regs = append(d.Regs, regRow{Label: r.Label, Plain: plain, Short: rules.Short(r, a.Lang), Text: r.Text, Kind: kind})
		}
		if p.LegallyBinding != "" && p.LegallyBinding < "2011-05-02" {
			d.ByDate = a.Lang.T("Pre-2011 plan: older height definitions (ÄPBL) apply.")
		}
	}
	if a.Building != nil {
		d.FootArea = math.Round(geo.Area(a.Building.Footprint)*10) / 10
	}
	if a.Placement != nil {
		d.PlaceSW = fmt.Sprintf("E %.1f, N %.1f (SWEREF 99 TM)", a.Placement.E, a.Placement.N)
	}
	if a.Output != nil {
		d.Counts = a.Output.Summary
		d.Sections = a.issues()
		for _, sec := range d.Sections {
			switch sec.Status {
			case rules.Fail:
				d.FailCount = len(sec.Issues)
			case rules.Missing:
				for _, is := range sec.Issues {
					if strings.HasPrefix(is.R.RuleID, "bfs") {
						d.NeedsInfo = true
					}
				}
			}
		}
	}
	return d
}

func (a *App) elementNames() map[string]string {
	out := map[string]string{}
	if a.Building == nil {
		return out
	}
	for _, list := range [][]rules.Element{a.Building.Rooms, a.Building.Windows, a.Building.Doors} {
		for _, e := range list {
			id, _ := e["id"].(string)
			name, _ := e["name"].(string)
			out[id] = name
		}
	}
	return out
}

//go:embed templates/*.tmpl
var tmplFS embed.FS

// One template set per language; the "t" function translates a label.
var tmpls = map[i18n.Lang]*template.Template{i18n.EN: newTemplates(i18n.EN), i18n.SV: newTemplates(i18n.SV)}

func newTemplates(l i18n.Lang) *template.Template {
	return template.Must(template.New("").Funcs(template.FuncMap{
		"t":    l.T,
		"tf":   l.F,
		"lang": func() string { return string(l) },

		"num":    func(v any) string { return l.Dec(num(v)) },
		"str":    func(v any) string { s, _ := v.(string); return s },
		"isTrue": func(v any) bool { b, _ := v.(bool); return b },
		"eq2":    func(a, b string) bool { return a == b },
		"count":  func(m map[rules.Status]int, s string) int { return m[rules.Status(s)] },
	}).ParseFS(tmplFS, "templates/*.tmpl"))
}

// num formats a number briefly: 9.4, 12, – for missing.
func num(v any) string {
	switch x := v.(type) {
	case float64:
		return strings.TrimSuffix(strings.TrimRight(fmt.Sprintf("%.2f", x), "0"), ".")
	case *float64:
		if x == nil {
			return ""
		}
		return strings.TrimSuffix(strings.TrimRight(fmt.Sprintf("%.2f", *x), "0"), ".")
	case int:
		return fmt.Sprint(x)
	case nil:
		return "–"
	}
	return fmt.Sprint(v)
}
