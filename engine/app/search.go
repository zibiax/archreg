package app

import (
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/martin/archreg/engine/geo"
)

// Place is one search suggestion.
type Place struct {
	Label  string  `json:"label"`  // "Valhallavägen 79"
	Detail string  `json:"detail"` // "Östermalm, Stockholm"
	Kind   string  `json:"kind"`   // house, street, district, city, coordinate…
	Lon    float64 `json:"lon"`
	Lat    float64 `json:"lat"`
	Zoom   float64 `json:"zoom"`
}

// Sweden, with a little margin; suggestions outside it are dropped.
const swedenBBox = "10.5,55.0,24.2,69.1"

// Replaceable in tests.
var (
	photonFn    = photon
	nominatimFn = geocode
)

var suggestCache = struct {
	sync.Mutex
	m map[string][]Place
}{m: map[string][]Place{}}

// Suggest returns up to six places matching a partly typed query, nearest
// to (biasLon, biasLat) first. Coordinates typed as WGS84 ("59.34, 18.07")
// or SWEREF 99 TM ("674219 6581103") come back as a single place.
//
// Suggestions use Photon (photon.komoot.io), an OpenStreetMap geocoder
// built for search-as-you-type. Nominatim's usage policy forbids
// autocomplete, so it is only used as a fallback for a submitted search.
func Suggest(q string, biasLon, biasLat float64) ([]Place, error) {
	q = strings.TrimSpace(q)
	if p, ok := parseCoordinate(q); ok {
		return []Place{p}, nil
	}
	if len([]rune(q)) < 2 {
		return nil, nil
	}
	// Round the bias so panning a little still hits the cache.
	key := fmt.Sprintf("%s|%.2f|%.2f", strings.ToLower(q), biasLon, biasLat)
	suggestCache.Lock()
	if ps, ok := suggestCache.m[key]; ok {
		suggestCache.Unlock()
		return ps, nil
	}
	suggestCache.Unlock()

	ps, err := photonFn(q, biasLon, biasLat)
	if err != nil {
		return nil, err
	}
	ps = rank(q, ps)
	suggestCache.Lock()
	if len(suggestCache.m) > 500 {
		suggestCache.m = map[string][]Place{}
	}
	suggestCache.m[key] = ps
	suggestCache.Unlock()
	return ps, nil
}

func photon(q string, biasLon, biasLat float64) ([]Place, error) {
	v := url.Values{}
	v.Set("q", q)
	v.Set("limit", "25") // Photon repeats streets per segment; dedupe below.
	v.Set("bbox", swedenBBox)
	v.Set("lang", "default") // local names (Göteborg), not the browser's language (Gothenburg)
	if biasLon != 0 || biasLat != 0 {
		v.Set("lon", strconv.FormatFloat(biasLon, 'f', 4, 64))
		v.Set("lat", strconv.FormatFloat(biasLat, 'f', 4, 64))
	}
	b, err := httpGet("https://photon.komoot.io/api/?" + v.Encode())
	if err != nil {
		return nil, err
	}
	return parsePhoton(b, 6)
}

// parsePhoton turns Photon's GeoJSON into deduplicated places.
func parsePhoton(b []byte, max int) ([]Place, error) {
	var r struct {
		Features []struct {
			Geometry struct {
				Coordinates []float64 `json:"coordinates"`
			} `json:"geometry"`
			Properties map[string]any `json:"properties"`
		} `json:"features"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	var out []Place
	seen := map[string]bool{}
	for _, f := range r.Features {
		p := f.Properties
		s := func(k string) string { v, _ := p[k].(string); return v }
		if cc := s("countrycode"); cc != "" && !strings.EqualFold(cc, "SE") {
			continue
		}
		if len(f.Geometry.Coordinates) < 2 {
			continue
		}
		kind := s("type")
		name, street, hn := s("name"), s("street"), s("housenumber")
		var label string
		switch {
		case hn != "" && name != "" && name != street:
			label = name + ", " + street + " " + hn
		case hn != "" && street != "":
			label = street + " " + hn
		case name != "":
			label = name
		case street != "":
			label = street
		default:
			continue
		}
		// Detail: one neighbourhood, then always the town.
		city := s("city")
		if city == "" {
			city = s("county")
		}
		var parts []string
		for _, k := range []string{"district", "locality"} {
			if v := s(k); v != "" && v != label && v != city {
				parts = append(parts, v)
				break
			}
		}
		if city != "" && city != label {
			parts = append(parts, city)
		}
		detail := strings.Join(parts, ", ")
		k := strings.ToLower(label + "|" + city)
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, Place{
			Label: label, Detail: detail, Kind: kind,
			Lon: f.Geometry.Coordinates[0], Lat: f.Geometry.Coordinates[1],
			Zoom: zoomFor(kind),
		})
		if len(out) == max {
			break
		}
	}
	return out, nil
}

func zoomFor(kind string) float64 {
	switch kind {
	case "house", "coordinate":
		return 18
	case "street":
		return 16
	case "district", "locality":
		return 15
	case "city":
		return 13
	case "county", "state":
		return 10
	}
	return 17
}

var coordRe = regexp.MustCompile(`^\s*([-+]?\d+(?:[.,]\d+)?)\s*[,;\s]\s*([-+]?\d+(?:[.,]\d+)?)\s*$`)

// parseCoordinate accepts "lat, lon" or "lon, lat" in WGS84 (Sweden's
// latitudes and longitudes don't overlap, so the order is unambiguous) and
// "E N" or "N E" in SWEREF 99 TM.
func parseCoordinate(q string) (Place, bool) {
	m := coordRe.FindStringSubmatch(q)
	if m == nil {
		return Place{}, false
	}
	a, err1 := strconv.ParseFloat(strings.Replace(m[1], ",", ".", 1), 64)
	b, err2 := strconv.ParseFloat(strings.Replace(m[2], ",", ".", 1), 64)
	if err1 != nil || err2 != nil {
		return Place{}, false
	}
	isLat := func(x float64) bool { return x >= 55 && x <= 69.5 }
	isLon := func(x float64) bool { return x >= 10 && x <= 24.5 }
	isE := func(x float64) bool { return x >= 180000 && x <= 1000000 }
	isN := func(x float64) bool { return x >= 6100000 && x <= 7700000 }
	var lon, lat float64
	var detail string
	switch {
	case isLat(a) && isLon(b):
		lat, lon, detail = a, b, "WGS84"
	case isLon(a) && isLat(b):
		lon, lat, detail = a, b, "WGS84"
	case isE(a) && isN(b):
		lon, lat = geo.ToWGS84(a, b)
		detail = "SWEREF 99 TM"
	case isN(a) && isE(b):
		lon, lat = geo.ToWGS84(b, a)
		detail = "SWEREF 99 TM"
	default:
		return Place{}, false
	}
	return Place{
		Label:  fmt.Sprintf("%.5f° N, %.5f° E", lat, lon),
		Detail: "Coordinate (" + detail + ")",
		Kind:   "coordinate", Lon: lon, Lat: lat, Zoom: 18,
	}, true
}

// Search handles a submitted query: the best suggestion, else Nominatim
// (which can find some things Photon can't, and falls back to the street
// when a house number is unknown).
func (a *App) Search(q string, biasLon, biasLat float64) *View {
	q = strings.TrimSpace(q)
	ps, _ := Suggest(q, biasLon, biasLat)
	if len(ps) > 0 {
		p := ps[0]
		return a.GoTo(p.Lon, p.Lat, p.Zoom, joinLabel(p))
	}
	if lon, lat, err := nominatimFn(q); err == nil {
		return a.GoTo(lon, lat, 17, "")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.toast = a.Lang.F("No place in Sweden matches “%s”. Try a street and number, a place name, or coordinates.", q)
	return a.view()
}

// GoTo flies to a chosen place and drops the pin there. A non-empty label
// is used as the address, saving a reverse-geocoding round trip.
func (a *App) GoTo(lon, lat, zoom float64, label string) *View {
	if math.IsNaN(lon) || math.IsNaN(lat) {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.view()
	}
	if zoom <= 0 {
		zoom = 17
	}
	a.mu.Lock()
	a.flyTo = []float64{lon, lat, zoom}
	a.mu.Unlock()
	return a.dropPin(lon, lat, label)
}

func joinLabel(p Place) string {
	if p.Kind == "coordinate" || p.Detail == "" {
		return p.Label
	}
	city := p.Detail
	if i := strings.LastIndex(city, ", "); i >= 0 {
		city = city[i+2:]
	}
	return p.Label + ", " + city
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// rank moves places that match every typed word to the front. Photon ranks
// by distance to the map centre, so "vasagatan 10 göt" typed while looking
// at Stockholm would otherwise list Töreboda before Göteborg. The sort is
// stable, so Photon's order is kept among equally good matches.
func rank(q string, ps []Place) []Place {
	words := strings.Fields(strings.ToLower(q))
	score := func(p Place) int {
		hay := strings.FieldsFunc(strings.ToLower(p.Label+" "+p.Detail), func(r rune) bool {
			return r == ' ' || r == ',' || r == ':' || r == '-' || r == '/'
		})
		n := 0
		for _, w := range words {
			for _, h := range hay {
				if strings.HasPrefix(h, w) {
					n++
					break
				}
			}
		}
		return n
	}
	out := append([]Place(nil), ps...)
	sort.SliceStable(out, func(i, j int) bool { return score(out[i]) > score(out[j]) })
	return out
}
