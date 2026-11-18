// Package plans loads detaljplan data published by cmd/fetch-plans as static
// JSON files in a 1 km grid (SWEREF 99 TM), so the browser only downloads the
// cells it needs.
package plans

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"

	"github.com/martin/archreg/engine/geo"
	"github.com/martin/archreg/engine/rules"
)

// Index is web/data/plans/index.json.
type Index struct {
	Generated string   `json:"generated"`
	Source    string   `json:"source"`
	CRS       string   `json:"crs"`
	CellSize  float64  `json:"cellSize"`
	Sample    bool     `json:"sample"`
	Cells     []string `json:"cells"`
	Count     int      `json:"count"`
}

// Cell is web/data/plans/<cx>_<cy>.json.
type Cell struct {
	Plans []rules.Plan `json:"plans"`
}

// CellKey names the cell containing (e, n).
func CellKey(e, n, size float64) string {
	return fmt.Sprintf("%d_%d", int(math.Floor(e/size)), int(math.Floor(n/size)))
}

// Fetcher reads a file relative to the plans directory.
type Fetcher func(name string) ([]byte, error)

// Store caches cells.
type Store struct {
	fetch Fetcher
	mu    sync.Mutex
	index *Index
	cells map[string][]*rules.Plan
	has   map[string]bool
}

func NewStore(f Fetcher) *Store {
	return &Store{fetch: f, cells: map[string][]*rules.Plan{}}
}

// Index loads (once) and returns the index. A missing index means no data.
func (s *Store) Index() (*Index, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.index != nil {
		return s.index, nil
	}
	b, err := s.fetch("index.json")
	if err != nil {
		return nil, err
	}
	var idx Index
	if err := json.Unmarshal(b, &idx); err != nil {
		return nil, fmt.Errorf("plans index: %w", err)
	}
	if idx.CellSize == 0 {
		idx.CellSize = 1000
	}
	s.index = &idx
	s.has = map[string]bool{}
	for _, c := range idx.Cells {
		s.has[c] = true
	}
	return s.index, nil
}

func (s *Store) cell(key string) ([]*rules.Plan, error) {
	s.mu.Lock()
	if ps, ok := s.cells[key]; ok {
		s.mu.Unlock()
		return ps, nil
	}
	known := s.has[key]
	s.mu.Unlock()
	if !known {
		return nil, nil
	}
	b, err := s.fetch(key + ".json")
	if err != nil {
		return nil, err
	}
	var c Cell
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("plans cell %s: %w", key, err)
	}
	ps := make([]*rules.Plan, len(c.Plans))
	for i := range c.Plans {
		ps[i] = &c.Plans[i]
	}
	s.mu.Lock()
	s.cells[key] = ps
	s.mu.Unlock()
	return ps, nil
}

// At returns the plans whose boundary contains (e, n), newest first.
func (s *Store) At(e, n float64) ([]*rules.Plan, error) {
	idx, err := s.Index()
	if err != nil {
		return nil, err
	}
	ps, err := s.cell(CellKey(e, n, idx.CellSize))
	if err != nil {
		return nil, err
	}
	var out []*rules.Plan
	for _, p := range ps {
		if Contains(p.Boundary, e, n) {
			out = append(out, p)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].LegallyBinding > out[j].LegallyBinding })
	return out, nil
}

// InBox returns plans in cells touching the box (deduplicated). It refuses
// boxes larger than maxCells cells.
func (s *Store) InBox(minE, minN, maxE, maxN float64, maxCells int) ([]*rules.Plan, error) {
	idx, err := s.Index()
	if err != nil {
		return nil, err
	}
	sz := idx.CellSize
	x0, x1 := int(math.Floor(minE/sz)), int(math.Floor(maxE/sz))
	y0, y1 := int(math.Floor(minN/sz)), int(math.Floor(maxN/sz))
	if (x1-x0+1)*(y1-y0+1) > maxCells {
		return nil, nil
	}
	seen := map[string]bool{}
	var out []*rules.Plan
	for x := x0; x <= x1; x++ {
		for y := y0; y <= y1; y++ {
			ps, err := s.cell(fmt.Sprintf("%d_%d", x, y))
			if err != nil {
				return out, err
			}
			for _, p := range ps {
				if !seen[p.ID] {
					seen[p.ID] = true
					out = append(out, p)
				}
			}
		}
	}
	return out, nil
}

// Rings parses a Polygon/MultiPolygon into polygons of rings.
func Rings(g *rules.Geometry) [][][]geo.Pt {
	if g == nil {
		return nil
	}
	switch g.Type {
	case "Polygon":
		var p [][]geo.Pt
		if json.Unmarshal(g.Coordinates, &p) == nil {
			return [][][]geo.Pt{p}
		}
	case "MultiPolygon":
		var mp [][][]geo.Pt
		if json.Unmarshal(g.Coordinates, &mp) == nil {
			return mp
		}
	}
	return nil
}

// Contains reports whether the geometry contains (e, n).
func Contains(g *rules.Geometry, e, n float64) bool {
	for _, poly := range Rings(g) {
		if geo.PointInPolygon(geo.Pt{e, n}, poly) {
			return true
		}
	}
	return false
}

// ToWGS84 converts a SWEREF 99 TM geometry to a GeoJSON geometry in lon/lat.
func ToWGS84(g *rules.Geometry) map[string]any {
	polys := Rings(g)
	conv := make([][][][2]float64, len(polys))
	for i, poly := range polys {
		for _, ring := range poly {
			var r [][2]float64
			for _, p := range ring {
				lon, lat := geo.ToWGS84(p[0], p[1])
				r = append(r, [2]float64{round6(lon), round6(lat)})
			}
			conv[i] = append(conv[i], r)
		}
	}
	return map[string]any{"type": "MultiPolygon", "coordinates": conv}
}

func round6(v float64) float64 { return math.Round(v*1e6) / 1e6 }

// FromWGS84Polygon builds a SWEREF 99 TM Polygon geometry from lon/lat rings.
func FromWGS84Polygon(rings [][][2]float64) *rules.Geometry {
	out := make([][][2]float64, len(rings))
	for i, r := range rings {
		for _, p := range r {
			e, n := geo.ToSweref(p[0], p[1])
			out[i] = append(out[i], [2]float64{math.Round(e*1000) / 1000, math.Round(n*1000) / 1000})
		}
	}
	b, _ := json.Marshal(out)
	return &rules.Geometry{Type: "Polygon", Coordinates: b}
}

// Cells returns the keys of every cell a geometry's bounding box touches.
func Cells(g *rules.Geometry, size float64) []string {
	minE, minN, maxE, maxN := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
	for _, poly := range Rings(g) {
		for _, ring := range poly {
			for _, p := range ring {
				minE, maxE = math.Min(minE, p[0]), math.Max(maxE, p[0])
				minN, maxN = math.Min(minN, p[1]), math.Max(maxN, p[1])
			}
		}
	}
	if math.IsInf(minE, 1) {
		return nil
	}
	var out []string
	for x := int(math.Floor(minE / size)); x <= int(math.Floor(maxE/size)); x++ {
		for y := int(math.Floor(minN / size)); y <= int(math.Floor(maxN/size)); y++ {
			out = append(out, fmt.Sprintf("%d_%d", x, y))
		}
	}
	return out
}

// Title for display.
func Title(p *rules.Plan) string {
	if p.Name != "" && !strings.EqualFold(p.Name, p.ID) {
		return p.Name
	}
	return "Detaljplan " + p.ID
}
