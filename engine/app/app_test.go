package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/martin/archreg/engine/ingest"
	"github.com/martin/archreg/engine/plans"
	ruleset "github.com/martin/archreg/rules"
)

func newTestApp(t *testing.T) *App {
	t.Helper()
	a, err := New("http://localhost/", ruleset.Boverket2026)
	if err != nil {
		t.Fatal(err)
	}
	a.store = plans.NewStore(func(name string) ([]byte, error) {
		return os.ReadFile(filepath.Join("../../web/data/plans", name))
	})
	reverseGeocodeFn = func(lon, lat float64) string { return "Valhallavägen 201, Stockholm" }
	return a
}

func TestFlow(t *testing.T) {
	a := newTestApp(t)
	v := a.Init()
	if !strings.Contains(v.Panel, "Drop a pin") || !strings.Contains(v.Panel, "Sample plan data") {
		t.Fatalf("start panel: %s", v.Panel)
	}

	v = a.DropPin(18.0835, 59.3468) // centre of sample plan 1
	if !strings.Contains(v.Panel, "EXEMPEL-2023/01") || !strings.Contains(v.Panel, "Valhallavägen 201") {
		t.Fatalf("pin panel: %s", v.Panel)
	}
	if len(v.Map.Regs["features"].([]map[string]any)) != 2 {
		t.Errorf("regulation areas: %v", v.Map.Regs)
	}

	raw, pos := ingest.Sample()
	rj, _ := json.Marshal(raw)
	v = a.LoadModel(rj, pos)
	if v.Error != "" {
		t.Fatal(v.Error)
	}
	if v.Model == nil || !v.Model.Rebuild || len(v.Map.Footprint["features"].([]map[string]any)) != 1 {
		t.Fatalf("model view %+v", v.Model)
	}
	for _, tab := range []string{"model", "results", "site"} {
		v = a.Action("tab", tab, "")
		if strings.Contains(v.Panel, "Template error") {
			t.Fatalf("%s: %s", tab, v.Panel)
		}
	}
	v = a.Action("tab", "results", "")
	for _, want := range []string{"Detaljplan", "Building regulations", "prickmark", "Escape window"} {
		if !strings.Contains(v.Panel, want) {
			t.Errorf("results panel lacks %q", want)
		}
	}
	// The sample sits in the middle of plan 1: 24×12 m, 3 storeys, 9.4 m.
	fails := map[string]bool{}
	for _, r := range a.Output.Results {
		if r.Status == "fail" {
			fails[r.RuleID] = true
		}
	}
	for _, id := range []string{"bfs2024-12.bathroom.turning", "bfs2024-7.escapeWindow", "bfs2024-12.elevator", "bfs2024-12.door.entrance"} {
		if !fails[id] {
			t.Errorf("expected %s to fail; fails: %v", id, fails)
		}
	}

	childManual := false
	for _, r := range a.Output.Results {
		if r.RuleID == "bfs2024-9.window.childSafety" && r.Status == "manual" {
			childManual = true
		}
	}
	if !childManual {
		t.Error("the 0.4 m sill window should need a child safety check")
	}

	// Moving onto the prickmark strip (south edge of plan 1) fails dp.prickmark.
	v = a.MoveModel(18.0835, 59.3468-55/111320.0)
	found := false
	for _, r := range a.Output.Results {
		if r.RuleID == "dp.prickmark" && r.Status != "pass" {
			found = true
		}
	}
	if !found {
		t.Error("prickmark not flagged after moving onto it")
	}

	// Select a result → focus 3D and highlight.
	for i, r := range a.Output.Results {
		if r.RuleID == "bfs2024-7.escapeWindow" {
			v = a.Action("select", itoa(i), "")
			if v.Focus != "3d" || len(v.Model.Highlight) == 0 {
				t.Errorf("select: focus %q highlight %v", v.Focus, v.Model.Highlight)
			}
		}
	}

	// Chat.
	v = a.Action("ask", "what does e1 mean?", "")
	if !strings.Contains(v.Chat, "1200") {
		t.Errorf("chat e1: %s", v.Chat)
	}
	v = a.Action("ask", "why does the window fail?", "")
	if !strings.Contains(strings.ToLower(v.Chat), "escape window") {
		t.Errorf("chat why: %s", v.Chat)
	}

	// Project info changes results.
	a.Action("bikeParking", "40", "")
	a.Action("apartments", "12", "")
	for _, r := range a.Output.Results {
		if r.RuleID == "bfs2026-4.bikeParking" && r.Status != "pass" {
			t.Errorf("bike parking 40/12 should pass: %+v", r)
		}
	}
}

func itoa(i int) string { b, _ := json.Marshal(i); return string(b) }
