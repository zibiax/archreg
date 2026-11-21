package rules

import (
	"encoding/json"
	"math"
	"os"
	"testing"
)

func square(x, y, s float64) [][2]float64 {
	return [][2]float64{{x, y}, {x + s, y}, {x + s, y + s}, {x, y + s}, {x, y}}
}

func polyGeom(r [][2]float64) *Geometry {
	b, _ := json.Marshal([][][2]float64{r})
	return &Geometry{Type: "Polygon", Coordinates: b}
}

func f(v float64) *float64 { return &v }
func n(v int) *int         { return &v }

func find(t *testing.T, out Output, id string) Result {
	t.Helper()
	for _, r := range out.Results {
		if r.RuleID == id {
			return r
		}
	}
	t.Fatalf("no result %s in %+v", id, out.Results)
	return Result{}
}

func TestGeometry(t *testing.T) {
	a := polyGeom(square(0, 0, 10))
	b := polyGeom(square(5, 5, 10))
	pa, _ := a.polygons()
	pb, _ := b.polygons()
	if got := area(pa); math.Abs(got-100) > 1e-9 {
		t.Errorf("area = %v", got)
	}
	if got := intersectionArea(pa, pb); math.Abs(got-25) > 1e-6 {
		t.Errorf("intersection = %v", got)
	}
	// L-shaped (non-convex) clip polygon.
	l := polyGeom([][2]float64{{0, 0}, {10, 0}, {10, 2}, {2, 2}, {2, 10}, {0, 10}})
	pl, _ := l.polygons()
	if got := intersectionArea(pa, pl); math.Abs(got-36) > 1e-6 {
		t.Errorf("L intersection = %v, want 36", got)
	}
	// Hole.
	b2, _ := json.Marshal([][][2]float64{square(0, 0, 10), square(2, 2, 2)})
	ph, _ := (&Geometry{Type: "Polygon", Coordinates: b2}).polygons()
	if got := area(ph); math.Abs(got-96) > 1e-9 {
		t.Errorf("area with hole = %v", got)
	}
}

func plan() *Plan {
	return &Plan{
		ID: "0180K-TEST", LegallyBinding: "2023-04-01",
		Boundary: polyGeom(square(0, 0, 100)),
		Regulations: []PlanRegulation{
			{ID: "r1", Text: "Marken får inte förses med byggnad.", Geometry: polyGeom(square(0, 0, 10))},
			{ID: "r2", Label: "h1", Text: "Högsta nockhöjd är 12 meter.", Value: f(12), ValueType: "max", Unit: "meter"},
			{ID: "r3", Label: "e1", Text: "Största byggnadsarea är 150 m².", Value: f(150), Unit: "m2"},
			{ID: "r4", Text: "Högsta antal våningar är 3."},
			{ID: "r5", Text: "Byggnader ska utformas med hänsyn till omgivningen."},
			{ID: "r6", Text: "Största byggnadsarea är 30 % av fastighetsarean.", Value: f(30), Unit: "%"},
		},
	}
}

func TestPlanPassAndFail(t *testing.T) {
	in := Input{
		Model: Model{
			Footprint: square(8, 8, 12), FootprintMethod: "exact", // 144 m², 4 m² on prickmark
			RidgeHeight: f(13.5), Storeys: n(3),
		},
		Plan: plan(),
	}
	out := Check(in, nil)

	pm := find(t, out, "dp.prickmark")
	if pm.Status != Fail || math.Abs(*pm.Measured-4) > 0.05 {
		t.Errorf("prickmark: %+v", pm)
	}
	if r := find(t, out, "dp.nockhojd"); r.Status != Fail || *r.Required != 12 {
		t.Errorf("nockhöjd: %+v", r)
	}
	if r := find(t, out, "dp.byggnadsarea"); r.Status != Pass && r.RegulationID == "r3" {
		t.Errorf("BYA: %+v", r)
	}
	if r := find(t, out, "dp.vaningar"); r.Status != Pass {
		t.Errorf("storeys: %+v", r)
	}
	if r := find(t, out, "dp.manual"); r.Status != Manual {
		t.Errorf("free text: %+v", r)
	}
	if r := find(t, out, "dp.withinPlan"); r.Status != Pass {
		t.Errorf("within: %+v", r)
	}
	for _, r := range out.Results {
		if r.RegulationID == "r6" && r.Status != Missing {
			t.Errorf("percent BYA without property should be missing: %+v", r)
		}
	}
	if out.Results[0].Status != Fail {
		t.Errorf("fails should come first, got %s", out.Results[0].Status)
	}
}

func TestApproxFootprintNeverFails(t *testing.T) {
	in := Input{Model: Model{Footprint: square(8, 8, 12), FootprintMethod: "convexHull"}, Plan: plan()}
	if r := find(t, Check(in, nil), "dp.prickmark"); r.Status != Manual {
		t.Errorf("approx footprint on prickmark should be manual, got %s", r.Status)
	}
}

func TestNoFootprint(t *testing.T) {
	out := Check(Input{Plan: plan()}, nil)
	if r := find(t, out, "dp.nockhojd"); r.Status != Missing {
		t.Errorf("got %s", r.Status)
	}
}

func TestPDFOnly(t *testing.T) {
	out := Check(Input{Plan: &Plan{ID: "x", PDFOnly: true}}, nil)
	if len(out.Results) != 1 || out.Results[0].Status != Manual {
		t.Errorf("%+v", out.Results)
	}
}

func TestByggnadshojdDefinition(t *testing.T) {
	p := &Plan{ID: "old", LegallyBinding: "1985-06-01", Regulations: []PlanRegulation{{ID: "b", Text: "Högsta byggnadshöjd är 7,5 meter."}}}
	out := Check(Input{Model: Model{Footprint: square(0, 0, 5)}, Plan: p}, nil)
	r := find(t, out, "dp.byggnadshojd")
	if r.Status != Manual {
		t.Errorf("status %s", r.Status)
	}
	p.Regulations[0].Text = "Högsta byggnadshöjd är 7,5 meter."
	out = Check(Input{Model: Model{Footprint: square(0, 0, 5), BuildingHeight: f(8)}, Plan: p}, nil)
	if r := find(t, out, "dp.byggnadshojd"); r.Status != Fail || *r.Required != 7.5 {
		t.Errorf("%+v", r)
	}
}

func TestAbsoluteHeight(t *testing.T) {
	p := &Plan{ID: "p", Regulations: []PlanRegulation{{ID: "h", Text: "Högsta nockhöjd är +30,0 meter över nollplanet."}}}
	m := Model{Footprint: square(0, 0, 5), RidgeHeight: f(10)}
	if r := find(t, Check(Input{Model: m, Plan: p}, nil), "dp.nockhojd"); r.Status != Missing {
		t.Errorf("no base elevation: %s", r.Status)
	}
	m.BaseElevation = f(22)
	if r := find(t, Check(Input{Model: m, Plan: p}, nil), "dp.nockhojd"); r.Status != Fail {
		t.Errorf("22+10 > 30: %+v", r)
	}
}

func loadRules(t *testing.T) *RuleSet {
	t.Helper()
	b, err := os.ReadFile("../../rules/boverket-2026.json")
	if err != nil {
		t.Fatal(err)
	}
	var rs RuleSet
	if err := json.Unmarshal(b, &rs); err != nil {
		t.Fatal(err)
	}
	if err := Validate(&rs); err != nil {
		t.Fatal(err)
	}
	return &rs
}

func byRule(out Output, id string) []Result {
	var rs []Result
	for _, r := range out.Results {
		if r.RuleID == id {
			rs = append(rs, r)
		}
	}
	return rs
}

func TestRuleSet(t *testing.T) {
	rs := loadRules(t)
	m := Model{
		Storeys:  n(3),
		Building: Element{"use": "apartments", "apartments": 10.0, "bikeParking": 12.0, "elevators": 0.0, "carParking": 4.0, "chargingPoints": 1.0},
		Rooms: []Element{
			{"id": "R1", "name": "Sovrum", "type": "bedroom", "area": 12.0, "ceilingHeight": 2.5},
			{"id": "R2", "name": "Vardagsrum", "type": "living", "area": 20.0, "ceilingHeight": 2.35},
			{"id": "R3", "name": "Bad", "type": "bathroom", "minDimension": 1.2},
			{"id": "R4", "name": "WC", "type": "bathroom", "minDimension": 1.1},
		},
		Windows: []Element{
			{"id": "W1", "name": "Fönster sovrum", "width": 0.5, "height": 0.6, "sillHeight": 0.9, "escape": true},
			{"id": "W2", "name": "Fönster vardagsrum", "width": 1.2, "height": 1.2, "sillHeight": 0.4},
		},
		Doors: []Element{
			{"id": "D1", "name": "Entré", "width": 0.78, "height": 2.1, "isExternal": true},
			{"id": "D2", "name": "Garderob", "width": 0.6, "height": 2.1, "isExternal": false},
			{"id": "D3", "name": "Sovrum", "width": 0.9, "height": 2.1, "clearWidth": 0.8, "clearHeight": 2.0, "isExternal": false},
		},
	}
	out := Check(Input{Model: m}, rs)
	status := func(id string) []Status {
		var s []Status
		for _, r := range byRule(out, id) {
			s = append(s, r.Status)
		}
		return s
	}
	want := map[string][]Status{
		"bfs2024-11.hygiene.room":           {Pass},
		"bfs2024-12.bathroom.turning":       {Fail},                 // no bathroom reaches 1.30 m
		"bfs2024-12.door.entrance":          {Fail},                 // overall 0.78 < 0.80 clear
		"bfs2024-12.door.entrance.dwelling": nil,                    // not a house
		"bfs2024-12.door.interior":          {Manual, Pass},         // closet → manual, D3 passes
		"bfs2024-9.door.freeHeight":         {Manual, Manual, Pass}, // no clear height on D1, D2
		"bfs2024-12.elevator":               {Fail},
		"bfs2024-12.elevator.tall":          nil,
		"bfs2024-7.escapeWindow":            {Fail}, // 0.5+0.6 < 1.5
		"bfs2024-9.window.childSafety":      {Manual, Pass},
		"bfs2024-9.window.safetyGlass":      {Manual, Pass},   // W2 sill 0.4 < 0.6
		"bfs2024-11.kitchen":                {Manual},         // no kitchen room → check by hand
		"bfs2024-8.ceilingHeight":           {Manual, Manual}, // reference value only
		"bfs2026-4.bikeParking":             {Fail},
		"bfs2026-4.charging.apartments":     {Manual}, // necessary only
		"bfs2026-9.energy":                  {Manual},
	}
	for id, w := range want {
		got := status(id)
		if len(got) != len(w) {
			t.Errorf("%s: got %v, want %v\n%+v", id, got, w, byRule(out, id))
			continue
		}
		for i := range w {
			if got[i] != w[i] {
				t.Errorf("%s: got %v, want %v\n%+v", id, got, w, byRule(out, id))
				break
			}
		}
	}
	for _, r := range out.Results {
		if r.Status == Pass && r.Draft {
			t.Errorf("draft rule produced a pass: %+v", r)
		}
	}
}

func TestMissingBuildingUse(t *testing.T) {
	out := Check(Input{Model: Model{Rooms: []Element{{"id": "R", "type": "bathroom"}}}}, loadRules(t))
	if r := byRule(out, "bfs2024-11.hygiene.room"); len(r) != 1 || r[0].Status != Missing {
		t.Errorf("%+v", r)
	}
}

func TestDraftNeverPasses(t *testing.T) {
	rs := &RuleSet{Rules: []Rule{{ID: "x", Title: "x", AppliesTo: "room", Check: &Condition{Property: "area", Op: ">=", Value: 1.0}, Source: Source{Title: "t"}}}}
	out := Check(Input{Model: Model{Rooms: []Element{{"id": "R", "area": 5.0}}}}, rs)
	if out.Results[0].Status != Manual {
		t.Errorf("%+v", out.Results)
	}
	rs.Rules[0].Verified = true
	out = Check(Input{Model: Model{Rooms: []Element{{"id": "R", "area": 5.0}}}}, rs)
	if out.Results[0].Status != Pass {
		t.Errorf("%+v", out.Results)
	}
}

func TestValidateRejects(t *testing.T) {
	bad := []RuleSet{
		{Rules: []Rule{{ID: "a", AppliesTo: "room", Source: Source{Title: "x"}}}},
		{Rules: []Rule{{ID: "a", AppliesTo: "roof", Manual: "m", Source: Source{Title: "x"}}}},
		{Rules: []Rule{{ID: "a", AppliesTo: "room", Check: &Condition{Property: "area", Op: "=>", Value: 1.0}, Source: Source{Title: "x"}}}},
		{Rules: []Rule{{ID: "a", AppliesTo: "room", Check: &Condition{Property: "area", Op: ">=", Value: "big"}, Source: Source{Title: "x"}}}},
	}
	for i, rs := range bad {
		if Validate(&rs) == nil {
			t.Errorf("case %d should fail validation", i)
		}
	}
}

func TestCheckJSONReadmeExample(t *testing.T) {
	in := `{"model":{"footprint":[[674210.2,6581340.1],[674220.2,6581340.1],[674220.2,6581350.1],[674210.2,6581350.1]],
	"ridgeHeight":6.9,"storeys":2,"rooms":[{"id":"R12","type":"bathroom","area":3.6,"ceilingHeight":2.35}],
	"windows":[{"id":"W-04","name":"Fönster sovrum","width":0.9,"height":1.1}]},
	"plan":{"id":"0180K-P2023/04","regulations":[]},"ruleset":"boverket-2026"}`
	out, err := CheckJSON([]byte(in), nil)
	if err != nil {
		t.Fatal(err)
	}
	var o Output
	if err := json.Unmarshal(out, &o); err != nil || o.Engine != Version {
		t.Fatalf("%s %v", out, err)
	}
}
