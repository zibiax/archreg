// Package rules is the archreg rules engine: a pure function that checks a
// simplified building model against a detaljplan and a set of building
// regulation rules.
//
//	Check(input, ruleset) → Output
//
// All coordinates are SWEREF 99 TM (EPSG:3006) metres. Heights are metres.
package rules

import "encoding/json"

// Version of the engine, reported in every Output.
const Version = "0.1.0"

// Status of a single check.
type Status string

const (
	Pass    Status = "pass"    // The model meets the rule.
	Fail    Status = "fail"    // The model breaks the rule.
	Manual  Status = "manual"  // A human has to judge.
	Missing Status = "missing" // The model lacks the data needed.
)

// Group a result belongs to.
type Group string

const (
	GroupPlan        Group = "detaljplan"
	GroupRegulations Group = "regulations"
)

// Input is what the UI sends to the engine.
type Input struct {
	Model   Model  `json:"model"`
	Plan    *Plan  `json:"plan,omitempty"`
	Ruleset string `json:"ruleset,omitempty"`
}

// Model is the simplified building model produced by the loaders.
type Model struct {
	// Footprint is the outline of the building on the ground, in SWEREF 99 TM.
	Footprint [][2]float64 `json:"footprint,omitempty"`
	// FootprintMethod says how the footprint was derived: "exact",
	// "convexHull" or "bbox". Anything but "exact" overestimates the area,
	// so failures based on it are reported as "manual".
	FootprintMethod string `json:"footprintMethod,omitempty"`

	RidgeHeight    *float64 `json:"ridgeHeight,omitempty"`    // nockhöjd, above ground
	TotalHeight    *float64 `json:"totalHeight,omitempty"`    // totalhöjd, above ground
	BuildingHeight *float64 `json:"buildingHeight,omitempty"` // byggnadshöjd, if the user has computed it
	BaseElevation  *float64 `json:"baseElevation,omitempty"`  // ground level in RH 2000, for "+X m över nollplanet"
	Storeys        *int     `json:"storeys,omitempty"`
	GrossFloorArea *float64 `json:"grossFloorArea,omitempty"` // BTA, m²

	// Building holds project facts the user enters or the loader derives:
	// "use" (singleFamily, apartments, public, office), "apartments",
	// "elevators", "bikeParking", "carParking", "chargingPoints".
	Building Element `json:"building,omitempty"`

	Rooms   []Element `json:"rooms,omitempty"`
	Windows []Element `json:"windows,omitempty"`
	Doors   []Element `json:"doors,omitempty"`
}

// Element is a room, window or door. It is a free-form map so that rules can
// refer to any property the loader provides ("area", "ceilingHeight",
// "width", "type", ...). "id" is required, "name" is used in messages.
type Element map[string]any

// Plan is a detaljplan with its regulations, in SWEREF 99 TM.
type Plan struct {
	ID             string           `json:"id"`
	Name           string           `json:"name,omitempty"`
	Status         string           `json:"status,omitempty"`
	LegallyBinding string           `json:"legallyBinding,omitempty"` // datumLagakraft, ISO date
	URL            string           `json:"url,omitempty"`
	PDFOnly        bool             `json:"pdfOnly,omitempty"` // scanned plan, cannot be checked
	Boundary       *Geometry        `json:"boundary,omitempty"`
	Regulations    []PlanRegulation `json:"regulations,omitempty"`
	Property       *Property        `json:"property,omitempty"`
}

// Property (fastighet) the building stands on. Optional: without it, checks
// that need the property area or boundary report "missing".
type Property struct {
	ID       string    `json:"id,omitempty"`
	Area     *float64  `json:"area,omitempty"`
	Boundary *Geometry `json:"boundary,omitempty"`
}

// PlanRegulation is one planbestämmelse, normalised from NGP.
type PlanRegulation struct {
	ID          string    `json:"id"`
	Label       string    `json:"label,omitempty"` // beteckning on the plan map, e.g. "e1"
	Category    string    `json:"category,omitempty"`
	Subcategory string    `json:"subcategory,omitempty"`
	Text        string    `json:"text"` // bestammelseformulering
	Value       *float64  `json:"value,omitempty"`
	ValueType   string    `json:"valueType,omitempty"` // "max", "min", ...
	Unit        string    `json:"unit,omitempty"`
	CatalogRef  string    `json:"catalogRef,omitempty"`
	UseForm     string    `json:"useForm,omitempty"` // användningsform
	Geometry    *Geometry `json:"geometry,omitempty"`
}

// Geometry is a GeoJSON Polygon or MultiPolygon.
type Geometry struct {
	Type        string          `json:"type"`
	Coordinates json.RawMessage `json:"coordinates"`
}

// Source of a rule, shown and linked in the UI.
type Source struct {
	Title   string `json:"title"`
	Section string `json:"section,omitempty"`
	URL     string `json:"url,omitempty"`
}

// Result of one check.
type Result struct {
	RuleID       string   `json:"ruleId"`
	Group        Group    `json:"group"`
	Status       Status   `json:"status"`
	Title        string   `json:"title"`
	Message      string   `json:"message"`
	Elements     []string `json:"elements,omitempty"`
	Measured     *float64 `json:"measured,omitempty"`
	Required     *float64 `json:"required,omitempty"`
	Unit         string   `json:"unit,omitempty"`
	Source       Source   `json:"source"`
	Draft        bool     `json:"draft,omitempty"`        // rule values not verified against the regulation text
	RegulationID string   `json:"regulationId,omitempty"` // plan regulation the result comes from
}

// Output of Check.
type Output struct {
	Engine  string         `json:"engine"`
	Ruleset string         `json:"ruleset,omitempty"`
	Results []Result       `json:"results"`
	Summary map[Status]int `json:"summary"`
}

// RuleSet is a file in rules/*.json.
type RuleSet struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
	Rules   []Rule `json:"rules"`
}

// Rule is one hand-written, checkable requirement.
type Rule struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	AppliesTo string `json:"appliesTo"` // "room", "window", "door" or "building"
	// Where filters elements by equality (case-insensitive for strings).
	Where map[string][]any `json:"where,omitempty"`
	// When are conditions on the building ("use", "storeys", ...) that must
	// hold for the rule to apply at all.
	When []Condition `json:"when,omitempty"`
	// Check / Checks: every condition must hold for an element to pass.
	Check  *Condition  `json:"check,omitempty"`
	Checks []Condition `json:"checks,omitempty"`
	// Quantifier "any": the rule is met if at least one matching element
	// passes (e.g. "at least one bathroom must be accessible").
	Quantifier string `json:"quantifier,omitempty"`
	// Necessary: the check is a necessary but not sufficient condition, so a
	// failure is real but a pass still needs a human to confirm.
	Necessary bool `json:"necessary,omitempty"`
	// Binding false: the value is a reference (allmänt råd, old rule,
	// practice), not law. Results are always "manual".
	Binding *bool `json:"binding,omitempty"`
	// OnFail "manual" turns failures into "manual" with FailHint appended,
	// for conditional requirements (e.g. "if this door must be accessible").
	OnFail   string `json:"onFail,omitempty"`
	FailHint string `json:"failHint,omitempty"`
	// Manual, when set, makes the rule always ask for human judgement with
	// this text (for requirements that can't be measured from a model).
	Manual   string `json:"manual,omitempty"`
	Text     string `json:"text,omitempty"` // regulation text, quoted
	Source   Source `json:"source"`
	Verified bool   `json:"verified"`
	Notes    string `json:"notes,omitempty"`
}

// Condition compares a property of an element with a value.
type Condition struct {
	Property string `json:"property"`
	Op       string `json:"op"`    // ">=", ">", "<=", "<", "==", "in"
	Value    any    `json:"value"` // number, string, bool, or a list for "in"
	Unit     string `json:"unit,omitempty"`
	// Upper names a property that is always >= Property (e.g. a door's
	// overall "width" for its "clearWidth"). If Property is missing, a
	// ">=" check can still fail on Upper; otherwise it needs a human.
	Upper string `json:"upper,omitempty"`
}

func (c Condition) number() (float64, bool) { return toFloat(c.Value) }
