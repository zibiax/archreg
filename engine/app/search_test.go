package app

import (
	"errors"
	"math"
	"strings"
	"testing"
)

// Trimmed from a real Photon response for "valhallav": the street comes
// back once per OSM way, plus one foreign hit that must be dropped.
const photonFixture = `{"features":[
 {"geometry":{"coordinates":[18.0830,59.3424]},"properties":{"name":"Valhallavägen","type":"street","district":"Östermalm","city":"Stockholm","countrycode":"SE"}},
 {"geometry":{"coordinates":[18.0770,59.3444]},"properties":{"name":"Valhallavägen","type":"street","district":"Norra Djurgården","city":"Stockholm","countrycode":"SE"}},
 {"geometry":{"coordinates":[18.0712,59.3431]},"properties":{"housenumber":"79","street":"Valhallavägen","type":"house","district":"Östermalm","city":"Stockholm","postcode":"114 28","countrycode":"SE"}},
 {"geometry":{"coordinates":[18.0706,59.3498]},"properties":{"name":"KTH: Biblioteket","street":"Osquars Backe","housenumber":"31","type":"house","city":"Stockholm","countrycode":"SE"}},
 {"geometry":{"coordinates":[10.75,59.91]},"properties":{"name":"Valhallveien","type":"street","city":"Oslo","countrycode":"NO"}},
 {"geometry":{"coordinates":[13.0,55.6]},"properties":{"type":"house","countrycode":"SE"}}
]}`

func TestParsePhoton(t *testing.T) {
	ps, err := parsePhoton([]byte(photonFixture), 6)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, p := range ps {
		got = append(got, p.Label+" | "+p.Detail)
	}
	want := []string{
		"Valhallavägen | Östermalm, Stockholm",
		"Valhallavägen 79 | Östermalm, Stockholm",
		"KTH: Biblioteket, Osquars Backe 31 | Stockholm",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if ps[0].Zoom != 16 || ps[1].Zoom != 18 {
		t.Errorf("zoom: street %v, house %v", ps[0].Zoom, ps[1].Zoom)
	}
}

func TestParseCoordinate(t *testing.T) {
	for _, c := range []struct {
		in       string
		lon, lat float64
		ok       bool
	}{
		{"59.3468, 18.0835", 18.0835, 59.3468, true},
		{"18.0835 59.3468", 18.0835, 59.3468, true},  // lon first
		{"59,3468; 18,0835", 18.0835, 59.3468, true}, // Swedish decimal comma
		{"674219 6581103", 18.0627, 59.3327, true},   // SWEREF 99 TM, E N
		{"6581103 674219", 18.0627, 59.3327, true},   // N E
		{"Valhallavägen 79", 0, 0, false},
		{"40.7, -74.0", 0, 0, false}, // New York: not Sweden
	} {
		p, ok := parseCoordinate(c.in)
		if ok != c.ok {
			t.Errorf("%q: ok=%v", c.in, ok)
			continue
		}
		if ok && (math.Abs(p.Lon-c.lon) > 1e-3 || math.Abs(p.Lat-c.lat) > 1e-3) {
			t.Errorf("%q → %f, %f", c.in, p.Lon, p.Lat)
		}
	}
}

func TestSearch(t *testing.T) {
	a := newTestApp(t)
	a.Init()
	oldP, oldN := photonFn, nominatimFn
	defer func() { photonFn, nominatimFn = oldP, oldN }()
	photonCalls := 0
	photonFn = func(q string, _, _ float64) ([]Place, error) {
		photonCalls++
		if q == "nothing here" || q == "kv. ekorren 3" {
			return nil, nil
		}
		return parsePhoton([]byte(photonFixture), 6)
	}
	nominatimFn = func(q string) (float64, float64, error) {
		if q == "kv. ekorren 3" {
			return 18.08, 59.34, nil
		}
		return 0, 0, errors.New("no match")
	}

	// Partial query: suggestions, cached on repeat.
	ps, _ := Suggest("valhallav", 18.07, 59.34)
	ps2, _ := Suggest("Valhallav ", 18.07, 59.34)
	if len(ps) != 3 || len(ps2) != 3 || photonCalls != 1 {
		t.Fatalf("suggest: %d %d results, %d calls", len(ps), len(ps2), photonCalls)
	}

	// Submitting takes the best suggestion, flies there and labels the pin
	// without a reverse lookup.
	reverseGeocodeFn = func(lon, lat float64) string { t.Error("unexpected reverse geocode"); return "" }
	v := a.Search("valhallav", 18.07, 59.34)
	if v.Map.FlyTo == nil || v.Map.FlyTo[2] != 16 || !strings.Contains(v.Panel, "Valhallavägen, Stockholm") {
		t.Fatalf("search: flyTo %v\n%s", v.Map.FlyTo, v.Panel)
	}

	// Picking a suggestion.
	v = a.GoTo(18.0712, 59.3431, 18, "Valhallavägen 79, Östermalm, Stockholm")
	if a.Pin.Address != "Valhallavägen 79, Östermalm, Stockholm" || v.Map.FlyTo[2] != 18 {
		t.Fatalf("goTo: %+v %v", a.Pin, v.Map.FlyTo)
	}

	// Coordinates skip the network.
	before := photonCalls
	v = a.Search("6582000 674800", 0, 0)
	if photonCalls != before || v.Map.FlyTo == nil {
		t.Fatalf("coordinate search: calls %d→%d, flyTo %v", before, photonCalls, v.Map.FlyTo)
	}

	// Photon finds nothing → Nominatim fallback, which reverse geocodes.
	reverseGeocodeFn = func(lon, lat float64) string { return "Ekorrstigen 3, Stockholm" }
	v = a.Search("kv. ekorren 3", 0, 0)
	if v.Map.FlyTo == nil || a.Pin.Address != "Ekorrstigen 3, Stockholm" {
		t.Fatalf("fallback: %v %+v", v.Map.FlyTo, a.Pin)
	}

	// Nothing anywhere: a one-shot message, pin left where it was.
	v = a.Search("nothing here", 0, 0)
	if !strings.Contains(v.Error, "No place in Sweden matches") || v.Map.FlyTo != nil || a.Pin.Address != "Ekorrstigen 3, Stockholm" {
		t.Fatalf("not found: %q %v", v.Error, v.Map.FlyTo)
	}
	if v = a.Viewport(18, 59, 18.1, 59.4, 14); v.Error != "" {
		t.Fatalf("error should not repeat: %q", v.Error)
	}
}

func TestRank(t *testing.T) {
	ps := []Place{
		{Label: "Vasagatan 10", Detail: "Berget, Töreboda"},
		{Label: "Vasagatan 10", Detail: "Hjo"},
		{Label: "Vasagatan 10", Detail: "Vasastaden, Göteborg"},
	}
	got := rank("vasagatan 10 göt", ps)
	if got[0].Detail != "Vasastaden, Göteborg" || got[1].Detail != "Berget, Töreboda" {
		t.Fatalf("rank: %+v", got)
	}
}
