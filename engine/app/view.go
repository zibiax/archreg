package app

import (
	"bytes"
	"embed"
	"fmt"
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
}

// MapView holds GeoJSON for the map layers.
type MapView struct {
	Pin       []float64      `json:"pin,omitempty"`
	Plans     map[string]any `json:"plans"`
	Regs      map[string]any `json:"regs"`
	Footprint map[string]any `json:"footprint"`
	FlyTo     []float64      `json:"flyTo,omitempty"`
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
	v := &View{Tab: a.Tab, Error: a.lastError}

	// Map.
	if a.Pin != nil {
		v.Map.Pin = []float64{a.Pin.Lon, a.Pin.Lat}
	}
	v.Map.FlyTo, a.flyTo = a.flyTo, nil
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
			kind, _ := rules.Describe(r)
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
			"properties": map[string]any{"highlight": highlight}})
	}
	v.Map.Footprint = fc(ff)

	// 3D.
	if a.Building != nil || a.newMesh {
		m := &Model3D{Rebuild: a.newMesh && a.Building != nil, Clear: a.Building == nil, Fit: a.fitModel, Highlight: []string{}}
		if r := a.selectedResult(); r != nil && r.Group == rules.GroupRegulations {
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

func (a *App) selectedResult() *rules.Result {
	if a.Output != nil && a.Selected >= 0 && a.Selected < len(a.Output.Results) {
		return &a.Output.Results[a.Selected]
	}
	return nil
}

// --- template data --------------------------------------------------------

type regRow struct {
	Label, Plain, Text, Kind string
}

type resultRow struct {
	I        int
	R        rules.Result
	Selected bool
	Names    []string
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
	Plan0      []resultRow
	Regs0      []resultRow
	Counts     map[rules.Status]int
	RoomTypes  []string
	LayerKinds map[string]string
	Uses       [][2]string
	Busy       string
}

func (a *App) panelData() panelData {
	d := panelData{Tab: a.Tab, Index: a.index, IdxErr: a.idxErr, Pin: a.Pin, Plans: a.Plans, PlanIdx: a.PlanIdx,
		B: a.Building, Place: a.Placement, P: a.Project, Out: a.Output, RoomTypes: ingest.RoomTypes, LayerKinds: a.layerKinds,
		Uses: [][2]string{{"house", "One- or two-family house (en-/tvåbostadshus)"}, {"apartments", "Apartment building (flerbostadshus)"}, {"nonResidential", "Non-residential (lokalbyggnad)"}}}
	if p := a.plan(); p != nil {
		d.Plan = p
		for _, r := range p.Regulations {
			kind, plain := rules.Describe(r)
			d.Regs = append(d.Regs, regRow{Label: r.Label, Plain: plain, Text: r.Text, Kind: kind})
		}
		if p.LegallyBinding != "" && p.LegallyBinding < "2011-05-02" {
			d.ByDate = "Adopted before 2 May 2011: older definitions (ÄPBL) apply, e.g. for byggnadshöjd."
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
		names := a.elementNames()
		for i, r := range a.Output.Results {
			row := resultRow{I: i, R: r, Selected: i == a.Selected}
			for _, id := range r.Elements {
				if n, ok := names[id]; ok {
					row.Names = append(row.Names, n)
				}
			}
			if r.Group == rules.GroupPlan {
				d.Plan0 = append(d.Plan0, row)
			} else {
				d.Regs0 = append(d.Regs0, row)
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

var tmpl = template.Must(template.New("").Funcs(template.FuncMap{
	"icon": func(s rules.Status) string {
		return map[rules.Status]string{rules.Pass: "✅", rules.Fail: "❌", rules.Manual: "👁", rules.Missing: "❔"}[s]
	},
	"statusText": func(s rules.Status) string {
		return map[rules.Status]string{rules.Pass: "Pass", rules.Fail: "Fail", rules.Manual: "Check manually", rules.Missing: "Missing data"}[s]
	},
	"num": func(v any) string {
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
	},
	"str":    func(v any) string { s, _ := v.(string); return s },
	"isTrue": func(v any) bool { b, _ := v.(bool); return b },
	"eq2":    func(a, b string) bool { return a == b },
	"count":  func(m map[rules.Status]int, s string) int { return m[rules.Status(s)] },
}).ParseFS(tmplFS, "templates/*.tmpl"))
