// Command fetch-plans downloads digital detaljplaner from Lantmäteriet's
// Nationella geodataplattformen (NGP) and publishes them as static JSON in a
// 1 km grid under web/data/plans, for the app to load.
//
// Credentials (from the environment or .env in the working directory):
//
//	LM_CLIENT_ID      consumer key from Lantmäteriet's API portal
//	LM_CLIENT_SECRET  consumer secret
//	LM_ENV            "prod" (default) or "ver" (Lantmäteriet's test environment)
//
// Usage:
//
//	go run ./cmd/fetch-plans                       # Stockholm inner city
//	go run ./cmd/fetch-plans -bbox 667000,6575000,680000,6588000
//	go run ./cmd/fetch-plans -sample               # fictional demo plans, no credentials
//
// API: Geodatakatalog Sökning, detaljplan v2 (OGC API Features / STAC search).
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/martin/archreg/engine/plans"
	"github.com/martin/archreg/engine/rules"
)

const cellSize = 1000.0

func main() {
	out := flag.String("out", "web/data/plans", "output directory")
	bbox := flag.String("bbox", "667000,6575000,680000,6588000", "area to fetch, SWEREF 99 TM: minE,minN,maxE,maxN")
	sample := flag.Bool("sample", false, "write fictional demo plans instead of fetching")
	statusFilter := flag.String("status", "laga kraft", "only keep plans with this status (empty = all)")
	dump := flag.String("dump", "", "also save the raw API features to this file (for debugging)")
	flag.Parse()
	loadDotEnv(".env")

	var ps []rules.Plan
	source := "Lantmäteriet, Nationella geodataplattformen (detaljplan)"
	if *sample {
		ps = samplePlans()
		source = "FICTIONAL SAMPLE DATA — not real plans"
	} else {
		var err error
		ps, err = fetchNGP(*bbox, *statusFilter, *dump)
		if err != nil {
			log.Fatal(err)
		}
	}
	if err := write(*out, ps, source, *sample); err != nil {
		log.Fatal(err)
	}
	log.Printf("wrote %d plans to %s", len(ps), *out)
}

func write(dir string, ps []rules.Plan, source string, sample bool) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	// Remove old cells so stale plans don't linger.
	old, _ := filepath.Glob(filepath.Join(dir, "*_*.json"))
	for _, f := range old {
		os.Remove(f)
	}
	cells := map[string][]rules.Plan{}
	for _, p := range ps {
		for _, c := range plans.Cells(p.Boundary, cellSize) {
			cells[c] = append(cells[c], p)
		}
	}
	idx := plans.Index{
		Generated: time.Now().UTC().Format(time.RFC3339), Source: source,
		CRS: "EPSG:3006", CellSize: cellSize, Sample: sample, Count: len(ps),
	}
	for k, v := range cells {
		idx.Cells = append(idx.Cells, k)
		if err := writeJSON(filepath.Join(dir, k+".json"), plans.Cell{Plans: v}); err != nil {
			return err
		}
	}
	sort.Strings(idx.Cells)
	return writeJSON(filepath.Join(dir, "index.json"), idx)
}

func writeJSON(path string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

// --- NGP -------------------------------------------------------------------

type client struct {
	base, api string
	token     string
	http      *http.Client
}

func newClient() (*client, error) {
	id, secret := os.Getenv("LM_CLIENT_ID"), os.Getenv("LM_CLIENT_SECRET")
	if id == "" || secret == "" {
		return nil, fmt.Errorf("set LM_CLIENT_ID and LM_CLIENT_SECRET (in .env or the environment), or use -sample")
	}
	base := "https://api.lantmateriet.se"
	if os.Getenv("LM_ENV") == "ver" {
		base = "https://api-ver.lantmateriet.se"
	}
	c := &client{base: base, api: base + "/distribution/geodatakatalog/sokning/v1/detaljplan/v2", http: &http.Client{Timeout: 60 * time.Second}}

	req, _ := http.NewRequest("POST", base+"/token", strings.NewReader("grant_type=client_credentials"))
	req.SetBasicAuth(id, secret)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("token: HTTP %d: %s", resp.StatusCode, trim(body))
	}
	var t struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(body, &t); err != nil || t.AccessToken == "" {
		return nil, fmt.Errorf("token: unexpected response: %s", trim(body))
	}
	c.token = t.AccessToken
	return c, nil
}

type link struct {
	Rel    string          `json:"rel"`
	Href   string          `json:"href"`
	Method string          `json:"method"`
	Body   json.RawMessage `json:"body"`
	Merge  bool            `json:"merge"`
}

type feature struct {
	ID         string          `json:"id"`
	Geometry   json.RawMessage `json:"geometry"`
	Properties struct {
		Feature struct {
			Typ     string `json:"typ"`
			Etikett string `json:"etikett"`
		} `json:"feature"`
		Title      string `json:"title"`
		Detaljplan *struct {
			Objektidentitet string `json:"objektidentitet"`
			Beteckning      string `json:"beteckning"`
			Namn            string `json:"namn"`
			Status          string `json:"status"`
			DatumLagakraft  string `json:"datumLagakraft"`
		} `json:"detaljplan"`
		Planbestammelse *struct {
			Katalogref  string `json:"planbestammelsekatalogreferens"`
			Formulering string `json:"bestammelseformulering"`
			Varde       *struct {
				Variabelvarde *float64 `json:"variabelvarde"`
				Vardetyp      string   `json:"vardetyp"`
				Enhet         string   `json:"enhet"`
			} `json:"bestammelsevarde"`
			Anvandningsform string `json:"anvandningsform"`
			Kategori        string `json:"kategori"`
			Underkategori   string `json:"underkategori"`
		} `json:"planbestammelse"`
	} `json:"properties"`
	Links  []link `json:"links"`
	Assets map[string]struct {
		Href  string   `json:"href"`
		Title string   `json:"title"`
		Roles []string `json:"roles"`
	} `json:"assets"`
	Collection string `json:"collection"`
}

type page struct {
	Features []feature `json:"features"`
	Links    []link    `json:"links"`
}

func (c *client) do(method, u string, body []byte) (*page, []byte, error) {
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	req, _ := http.NewRequest(method, u, r)
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/geo+json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, nil, fmt.Errorf("%s %s: HTTP %d: %s", method, u, resp.StatusCode, trim(b))
	}
	var p page
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, nil, fmt.Errorf("%s: %w", u, err)
	}
	return &p, b, nil
}

func fetchNGP(bbox, status, dump string) ([]rules.Plan, error) {
	c, err := newClient()
	if err != nil {
		return nil, err
	}
	var box []float64
	for _, s := range strings.Split(bbox, ",") {
		var v float64
		if _, err := fmt.Sscan(strings.TrimSpace(s), &v); err != nil {
			return nil, fmt.Errorf("bad -bbox: %v", err)
		}
		box = append(box, v)
	}
	if len(box) != 4 {
		return nil, fmt.Errorf("-bbox needs 4 numbers")
	}

	body, _ := json.Marshal(map[string]any{"bbox": box, "limit": 1000})
	u := c.api + "/search?" + url.Values{"crs": {"http://www.opengis.net/def/crs/EPSG/0/3006"}, "bbox-crs": {"http://www.opengis.net/def/crs/EPSG/0/3006"}}.Encode()
	method := "POST"
	var all []feature
	var raw [][]byte
	for pageNo := 1; u != ""; pageNo++ {
		p, b, err := c.do(method, u, body)
		if err != nil {
			return nil, err
		}
		all = append(all, p.Features...)
		if dump != "" {
			raw = append(raw, b)
		}
		log.Printf("page %d: %d features (total %d)", pageNo, len(p.Features), len(all))
		u = ""
		for _, l := range p.Links {
			if l.Rel != "next" {
				continue
			}
			u = l.Href
			method = "GET"
			if strings.EqualFold(l.Method, "POST") {
				method = "POST"
				if len(l.Body) > 0 {
					if l.Merge {
						body = mergeJSON(body, l.Body)
					} else {
						body = l.Body
					}
				}
			} else {
				body = nil
			}
		}
		if pageNo > 500 {
			return nil, fmt.Errorf("too many pages; use a smaller -bbox")
		}
	}
	if dump != "" {
		os.WriteFile(dump, []byte("["+string(bytes.Join(raw, []byte(",")))+"]"), 0o644)
	}
	return normalise(all, status), nil
}

func normalise(fs []feature, status string) []rules.Plan {
	byID := map[string]*rules.Plan{}
	var order []string
	parentOf := func(f feature) string {
		for _, l := range f.Links {
			if l.Rel == "huvudobjekt" {
				return l.Href[strings.LastIndex(l.Href, "/")+1:]
			}
		}
		return ""
	}
	// Plans first.
	for _, f := range fs {
		d := f.Properties.Detaljplan
		if f.Properties.Feature.Typ != "detaljplan" || d == nil {
			continue
		}
		if status != "" && !strings.EqualFold(d.Status, status) {
			continue
		}
		p := &rules.Plan{
			ID: firstNonEmpty(d.Beteckning, d.Objektidentitet, f.ID), Name: d.Namn, Status: d.Status,
			LegallyBinding: firstN(d.DatumLagakraft, 10),
			Boundary:       &rules.Geometry{},
		}
		if err := json.Unmarshal(f.Geometry, p.Boundary); err != nil || (p.Boundary.Type != "Polygon" && p.Boundary.Type != "MultiPolygon") {
			continue
		}
		for _, a := range f.Assets {
			for _, r := range a.Roles {
				if r == "beslutshandling" && p.URL == "" {
					p.URL = a.Href
				}
			}
		}
		byID[f.ID] = p
		order = append(order, f.ID)
	}
	// Then regulations.
	for _, f := range fs {
		pb := f.Properties.Planbestammelse
		if pb == nil {
			continue
		}
		p := byID[parentOf(f)]
		if p == nil {
			continue
		}
		r := rules.PlanRegulation{
			ID: f.ID, Label: f.Properties.Feature.Etikett, Category: pb.Kategori, Subcategory: pb.Underkategori,
			Text: pb.Formulering, CatalogRef: pb.Katalogref, UseForm: pb.Anvandningsform,
		}
		if pb.Varde != nil {
			r.Value, r.ValueType, r.Unit = pb.Varde.Variabelvarde, pb.Varde.Vardetyp, pb.Varde.Enhet
		}
		if len(f.Geometry) > 0 && string(f.Geometry) != "null" {
			g := &rules.Geometry{}
			if json.Unmarshal(f.Geometry, g) == nil && (g.Type == "Polygon" || g.Type == "MultiPolygon") {
				r.Geometry = g
			}
		}
		p.Regulations = append(p.Regulations, r)
	}
	out := make([]rules.Plan, 0, len(order))
	for _, id := range order {
		out = append(out, *byID[id])
	}
	return out
}

func mergeJSON(a, b []byte) []byte {
	var ma, mb map[string]any
	json.Unmarshal(a, &ma)
	json.Unmarshal(b, &mb)
	if ma == nil {
		ma = map[string]any{}
	}
	for k, v := range mb {
		ma[k] = v
	}
	out, _ := json.Marshal(ma)
	return out
}

// --- helpers ---------------------------------------------------------------

func loadDotEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(strings.TrimPrefix(line, "export "), "=")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.Trim(strings.TrimSpace(v), `"'`)
		if os.Getenv(k) == "" {
			os.Setenv(k, v)
		}
	}
}

func trim(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	return s
}

func firstNonEmpty(s ...string) string {
	for _, x := range s {
		if x != "" {
			return x
		}
	}
	return ""
}

func firstN(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
