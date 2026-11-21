// Package app is the whole user-facing application logic: state, actions and
// the rendered side panel. It runs inside the WASM module; the JS glue only
// forwards events here and applies the View it gets back to the page, the
// map and the 3D viewer.
package app

import (
	"encoding/json"
	"fmt"
	"github.com/martin/archreg/engine/i18n"
	"math"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"github.com/martin/archreg/engine/geo"
	"github.com/martin/archreg/engine/ingest"
	"github.com/martin/archreg/engine/plans"
	"github.com/martin/archreg/engine/rules"
)

// Pin is the selected site.
type Pin struct {
	Lon, Lat float64
	E, N     float64
	Address  string
}

// ChatMsg is one line in the chat log.
type ChatMsg struct {
	From string // "user" or "app"
	Text string
}

// App holds all state.
type App struct {
	Lang i18n.Lang // "en" or "sv"; set with the "lang" action
	mu   sync.Mutex

	base    *url.URL
	store   *plans.Store
	index   *plans.Index
	idxErr  error
	ruleset *rules.RuleSet

	Tab string

	Pin       *Pin
	Plans     []*rules.Plan
	PlanIdx   int
	Outlines  []*rules.Plan
	busy      string
	lastError string

	// Model.
	raw        *ingest.Raw
	positions  []float32
	Building   *ingest.Building
	Placement  *ingest.Placement
	layerKinds map[string]string
	roomType   map[string]string
	escape     map[string]bool
	external   map[string]bool

	// Project facts entered by the user.
	Project Project

	Output   *rules.Output
	Selected int

	Chat []ChatMsg

	// One-shot instructions for the JS side.
	flyTo    []float64
	toast    string
	focus    string
	newMesh  bool
	fitModel bool
}

// Project facts the model can't tell us.
type Project struct {
	Use            string   // house, apartments, nonResidential
	Apartments     *float64 // bostadslägenheter
	BikeParking    *float64
	CarParking     *float64
	ChargingPoints *float64
	Elevators      *float64 // overrides the model's count
	BaseElevation  *float64 // ground level, RH 2000
}

// New creates the app. base is the page URL; rulesJSON the rule set.
func New(base string, rulesJSON []byte) (*App, error) {
	u, err := url.Parse(base)
	if err != nil {
		return nil, err
	}
	var rs rules.RuleSet
	if err := json.Unmarshal(rulesJSON, &rs); err != nil {
		return nil, fmt.Errorf("rules: %w", err)
	}
	if err := rules.Validate(&rs); err != nil {
		return nil, fmt.Errorf("rules: %w", err)
	}
	a := &App{base: u, ruleset: &rs, Tab: "site", Selected: -1,
		layerKinds: map[string]string{}, roomType: map[string]string{}, escape: map[string]bool{}, external: map[string]bool{}}
	a.store = plans.NewStore(func(name string) ([]byte, error) { return a.get("data/plans/" + name) })
	a.Chat = nil
	return a, nil
}

// get fetches a URL relative to the page (works in the browser via fetch).
func (a *App) get(rel string) ([]byte, error) {
	u, err := a.base.Parse(rel)
	if err != nil {
		return nil, err
	}
	return httpGet(u.String())
}

// Init loads the plan index.
func (a *App) Init() *View {
	idx, err := a.store.Index()
	a.mu.Lock()
	defer a.mu.Unlock()
	a.index, a.idxErr = idx, err
	return a.view()
}

// DropPin selects a site where the user clicked the map.
func (a *App) DropPin(lon, lat float64) *View { return a.dropPin(lon, lat, "") }

// dropPin selects a site; an empty address is looked up.
func (a *App) dropPin(lon, lat float64, addr string) *View {
	e, n := geo.ToSweref(lon, lat)
	a.mu.Lock()
	a.Pin = &Pin{Lon: lon, Lat: lat, E: e, N: n, Address: a.Lang.T("Looking up address…")}
	a.Tab = "site"
	a.lastError = ""
	a.mu.Unlock()

	ps, err := a.store.At(e, n)
	if addr == "" {
		addr = reverseGeocodeFn(lon, lat)
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	if a.Pin == nil || a.Pin.Lon != lon || a.Pin.Lat != lat {
		return a.view() // a newer pin replaced this one
	}
	a.Pin.Address = addr
	a.Plans, a.PlanIdx = ps, 0
	if err != nil {
		a.lastError = a.Lang.F("Couldn't load plan data: %s", err.Error())
	}
	// The building comes along to a new site: an unplaced model goes to
	// the pin, and so does a placed one when the pin is on another site
	// (clicks near it, e.g. to read the plan, leave it where it is).
	if a.Building != nil {
		switch {
		case a.Placement == nil:
			a.Placement = &ingest.Placement{E: e, N: n}
		case math.Hypot(a.Placement.E-e, a.Placement.N-n) > newSiteDistance:
			a.Placement = &ingest.Placement{E: e, N: n, RotationDeg: a.Placement.RotationDeg}
		}
	}
	a.recheck()
	return a.view()
}

// Viewport loads plan outlines in view (only when zoomed in).
func (a *App) Viewport(minLon, minLat, maxLon, maxLat, zoom float64) *View {
	var ps []*rules.Plan
	if zoom >= 12 {
		e0, n0 := geo.ToSweref(minLon, minLat)
		e1, n1 := geo.ToSweref(maxLon, maxLat)
		e2, n2 := geo.ToSweref(minLon, maxLat)
		e3, n3 := geo.ToSweref(maxLon, minLat)
		ps, _ = a.store.InBox(math.Min(math.Min(e0, e2), math.Min(e1, e3)), math.Min(math.Min(n0, n3), math.Min(n1, n2)),
			math.Max(math.Max(e0, e2), math.Max(e1, e3)), math.Max(math.Max(n0, n3), math.Max(n1, n2)), 100)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.Outlines = ps
	return a.view()
}

// LoadModel ingests a model from a loader.
func (a *App) LoadModel(rawJSON []byte, positions []float32) *View {
	var raw ingest.Raw
	err := json.Unmarshal(rawJSON, &raw)
	a.mu.Lock()
	defer a.mu.Unlock()
	if err != nil {
		a.lastError = a.Lang.F("Couldn't read the model: %s", err.Error())
		return a.view()
	}
	a.raw, a.positions = &raw, positions
	a.layerKinds = map[string]string{}
	a.roomType, a.escape, a.external = map[string]string{}, map[string]bool{}, map[string]bool{}
	if err := a.rebuild(); err != nil {
		a.lastError = a.Lang.F("Couldn't read the model: %s", err.Error())
		a.Building = nil
		return a.view()
	}
	a.Placement = nil
	if p, ok := a.Building.DefaultPlacement(); ok {
		a.Placement = &p
		lon, lat := geo.ToWGS84(p.E, p.N)
		a.flyTo = []float64{lon, lat, 18}
		a.Chat = append(a.Chat, ChatMsg{"app", a.Lang.T("The model is georeferenced in SWEREF 99 TM, so I placed it automatically.")})
	} else if a.Pin != nil {
		a.Placement = &ingest.Placement{E: a.Pin.E, N: a.Pin.N}
	}
	if a.Project.Use == "" {
		a.Project.Use = "apartments"
	}
	a.newMesh, a.fitModel = true, true
	a.Tab = "model"
	a.lastError = ""
	a.recheck()
	return a.view()
}

func (a *App) rebuild() error {
	b, err := ingest.Build(*a.raw, a.positions, a.layerKinds)
	if err != nil {
		return err
	}
	a.Building = b
	return nil
}

// MoveModel places the model's centre at lon/lat.
func (a *App) MoveModel(lon, lat float64) *View {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.Building == nil {
		return a.view()
	}
	e, n := geo.ToSweref(lon, lat)
	rot := 0.0
	if a.Placement != nil {
		rot = a.Placement.RotationDeg
	}
	a.Placement = &ingest.Placement{E: e, N: n, RotationDeg: rot}
	a.recheck()
	return a.view()
}

// recheck runs the engine. Caller holds the lock.
func (a *App) recheck() {
	if a.Building == nil {
		a.Output = nil
		return
	}
	b := a.Building
	// Apply the user's corrections.
	for _, r := range b.Rooms {
		if t, ok := a.roomType[r["id"].(string)]; ok {
			r["type"] = t
		}
	}
	for _, w := range b.Windows {
		if v, ok := a.escape[w["id"].(string)]; ok {
			w["escape"] = v
		}
	}
	for _, d := range b.Doors {
		if v, ok := a.external[d["id"].(string)]; ok {
			d["isExternal"] = v
		}
	}
	facts := rules.Element{}
	if a.Project.Use != "" {
		facts["use"] = a.Project.Use
	}
	setF := func(k string, v *float64) {
		if v != nil {
			facts[k] = *v
		}
	}
	setF("apartments", a.Project.Apartments)
	setF("bikeParking", a.Project.BikeParking)
	setF("carParking", a.Project.CarParking)
	setF("chargingPoints", a.Project.ChargingPoints)
	setF("elevators", a.Project.Elevators)

	m := b.Model(a.Placement, facts, a.Project.BaseElevation)
	in := rules.Input{Model: m, Ruleset: a.ruleset.ID, Lang: string(a.Lang)}
	if p := a.plan(); p != nil {
		in.Plan = p
	}
	out := rules.Check(in, a.ruleset)
	a.Output = &out
	if a.Selected >= len(out.Results) {
		a.Selected = -1
	}
}

func (a *App) plan() *rules.Plan {
	if a.PlanIdx >= 0 && a.PlanIdx < len(a.Plans) {
		return a.Plans[a.PlanIdx]
	}
	return nil
}

// Action handles panel events: name is the data-action, value its value.
func (a *App) Action(name, value, id string) *View {
	if name == "ask" {
		a.mu.Lock()
		a.Chat = append(a.Chat, ChatMsg{"user", value})
		a.Chat = append(a.Chat, ChatMsg{"app", a.answer(value)})
		v := a.view()
		a.mu.Unlock()
		return v
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	f := func() *float64 {
		v, err := strconv.ParseFloat(strings.Replace(strings.TrimSpace(value), ",", ".", 1), 64)
		if err != nil {
			return nil
		}
		return &v
	}
	switch name {
	case "lang":
		a.Lang = i18n.Parse(value)
	case "tab":
		switch value {
		case "site", "model", "results":
			a.Tab = value
		}
	case "plan":
		if i, err := strconv.Atoi(value); err == nil {
			a.PlanIdx = i
		}
	case "rotate":
		if a.Placement != nil && f() != nil {
			a.Placement.RotationDeg = *f()
		}
	case "reset-placement":
		// Back to the pin, unrotated.
		if a.Pin != nil && a.Building != nil {
			a.Placement = &ingest.Placement{E: a.Pin.E, N: a.Pin.N}
		}
	case "place-at-pin":
		if a.Pin != nil && a.Building != nil {
			rot := 0.0
			if a.Placement != nil {
				rot = a.Placement.RotationDeg
			}
			a.Placement = &ingest.Placement{E: a.Pin.E, N: a.Pin.N, RotationDeg: rot}
		}
	case "use":
		a.Project.Use = value
	case "apartments":
		a.Project.Apartments = f()
	case "bikeParking":
		a.Project.BikeParking = f()
	case "carParking":
		a.Project.CarParking = f()
	case "chargingPoints":
		a.Project.ChargingPoints = f()
	case "elevators":
		a.Project.Elevators = f()
	case "baseElevation":
		a.Project.BaseElevation = f()
	case "room-type":
		a.roomType[id] = value
	case "escape":
		a.escape[id] = value == "true"
	case "external":
		a.external[id] = value == "true"
	case "layer":
		a.layerKinds[id] = value
		if a.raw != nil {
			if err := a.rebuild(); err != nil {
				a.lastError = err.Error()
			}
		}
	case "select":
		if i, err := strconv.Atoi(value); err == nil {
			if a.Selected == i {
				a.Selected = -1
			} else {
				a.Selected = i
				if a.Output != nil && i < len(a.Output.Results) {
					r := a.Output.Results[i]
					if r.Group == rules.GroupPlan {
						a.focus = "map"
					} else if len(r.Elements) > 0 && r.Elements[0] != "building" {
						a.focus = "3d"
					}
				}
			}
		}
	case "clear-model":
		a.Building, a.raw, a.positions, a.Placement, a.Output = nil, nil, nil, nil, nil
		a.newMesh = true
		a.Tab = "site"
	}
	a.recheck()
	return a.view()
}

// newSiteDistance: a pin farther than this (metres) from the building
// counts as a different site, and the building moves to it.
const newSiteDistance = 150

// Replaceable in tests.
var reverseGeocodeFn = reverseGeocode

// --- geocoding (OpenStreetMap Nominatim, usage policy: light, interactive use)

func reverseGeocode(lon, lat float64) string {
	u := fmt.Sprintf("https://nominatim.openstreetmap.org/reverse?format=jsonv2&lat=%.6f&lon=%.6f&accept-language=sv&zoom=18", lat, lon)
	b, err := httpGet(u)
	if err == nil {
		var r struct {
			Address map[string]string `json:"address"`
			Name    string            `json:"display_name"`
		}
		if json.Unmarshal(b, &r) == nil {
			road := r.Address["road"]
			if road == "" {
				road = r.Address["pedestrian"]
			}
			if road != "" {
				s := road
				if hn := r.Address["house_number"]; hn != "" {
					s += " " + hn
				}
				if c := firstOf(r.Address, "city", "town", "suburb"); c != "" {
					s += ", " + c
				}
				return s
			}
			if r.Name != "" {
				return strings.SplitN(r.Name, ",", 3)[0]
			}
		}
	}
	return fmt.Sprintf("%.5f° N, %.5f° E", lat, lon)
}

func geocode(q string) (float64, float64, error) {
	u := "https://nominatim.openstreetmap.org/search?format=jsonv2&limit=1&countrycodes=se&q=" + url.QueryEscape(q)
	b, err := httpGet(u)
	if err != nil {
		return 0, 0, err
	}
	var rs []struct {
		Lat string `json:"lat"`
		Lon string `json:"lon"`
	}
	if err := json.Unmarshal(b, &rs); err != nil || len(rs) == 0 {
		return 0, 0, fmt.Errorf("no match")
	}
	lat, _ := strconv.ParseFloat(rs[0].Lat, 64)
	lon, _ := strconv.ParseFloat(rs[0].Lon, 64)
	return lon, lat, nil
}

func firstOf(m map[string]string, keys ...string) string {
	for _, k := range keys {
		if m[k] != "" {
			return m[k]
		}
	}
	return ""
}
