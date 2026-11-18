package ingest

import (
	"testing"

	"github.com/martin/archreg/engine/rules"
)

func TestSample(t *testing.T) {
	raw, pos := Sample()
	b, err := Build(raw, pos, nil)
	if err != nil {
		t.Fatal(err)
	}
	if b.Storeys != 3 {
		t.Errorf("storeys %d", b.Storeys)
	}
	if b.TotalHeight != 9.4 || !b.RidgeFromRoof {
		t.Errorf("height %v %v", b.TotalHeight, b.RidgeFromRoof)
	}
	if len(b.Rooms) != 18 || len(b.Windows) != 9 || len(b.Doors) != 7 {
		t.Errorf("rooms %d windows %d doors %d", len(b.Rooms), len(b.Windows), len(b.Doors))
	}
	types := map[string]int{}
	for _, r := range b.Rooms {
		types[r["type"].(string)]++
	}
	if types["bathroom"] != 6 || types["bedroom"] != 3 || types["kitchen"] != 3 || types["living"] != 3 || types["circulation"] != 3 {
		t.Errorf("types %v", types)
	}
	r := b.Rooms[0]
	if a := r["area"].(float64); a < 49 || a > 50 { // 7.7 × 6.7 = 51.59? (8-0.3)*(7-0.3)=51.59
		if a < 51 || a > 52 {
			t.Errorf("living area %v", a)
		}
	}
	if r["minDimension"].(float64) != 6.7 {
		t.Errorf("min dimension %v", r["minDimension"])
	}
	var escape rules.Element
	for _, w := range b.Windows {
		if w["escape"] == true {
			escape = w
		}
	}
	if escape == nil || escape["sillHeight"].(float64) != 1.1 {
		t.Errorf("escape window %v", escape)
	}
	if fa := b.Footprint; len(fa) != 4 {
		t.Errorf("footprint %v", fa)
	}
	p := Placement{E: 674000, N: 6581000, RotationDeg: 90}
	fp := b.PlacedFootprint(p)
	// rotated 90°: the 24 m side now runs north–south
	minN, maxN := fp[0][1], fp[0][1]
	for _, q := range fp {
		minN, maxN = min(minN, q[1]), max(maxN, q[1])
	}
	if d := maxN - minN; d < 24.5 || d > 24.7 {
		t.Errorf("rotated extent %v", d)
	}
}

func TestRoomType(t *testing.T) {
	for in, want := range map[string]string{"Badrum 1": "bathroom", "WC": "bathroom", "Sovrum 2": "bedroom", "KÖK": "kitchen", "Living room": "living", "Förråd": "storage", "Rum 7": "other"} {
		if got := RoomType(in); got != want {
			t.Errorf("%q → %s, want %s", in, got, want)
		}
	}
}
