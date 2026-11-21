package rules

import (
	"sync"

	"encoding/json"
	"fmt"
	"github.com/martin/archreg/engine/i18n"
	"sort"
)

// Check runs every check and returns the results, sorted so that failures
// come first within each group.
func Check(in Input, rs *RuleSet) Output {
	// Messages are built deep in the checks; the language is set for the
	// duration of one Check (see tr).
	checkMu.Lock()
	defer checkMu.Unlock()
	lang = i18n.Parse(in.Lang)
	if rs != nil && lang == i18n.SV {
		rs = rs.localized()
	}
	out := Output{Engine: Version, Ruleset: in.Ruleset, Results: []Result{}}
	if in.Plan != nil {
		out.Results = append(out.Results, checkPlan(&in.Model, in.Plan)...)
	}
	if rs != nil {
		if out.Ruleset == "" {
			out.Ruleset = rs.ID
		}
		out.Results = append(out.Results, checkRuleSet(&in.Model, rs)...)
	}
	order := map[Status]int{Fail: 0, Missing: 1, Manual: 2, Pass: 3}
	group := map[Group]int{GroupPlan: 0, GroupRegulations: 1}
	sort.SliceStable(out.Results, func(i, j int) bool {
		a, b := out.Results[i], out.Results[j]
		if group[a.Group] != group[b.Group] {
			return group[a.Group] < group[b.Group]
		}
		return order[a.Status] < order[b.Status]
	})
	out.Summary = map[Status]int{Pass: 0, Fail: 0, Manual: 0, Missing: 0}
	for _, r := range out.Results {
		out.Summary[r.Status]++
	}
	return out
}

// CheckJSON is the JSON boundary used by the WASM build and the CLI.
// rulesJSON may be empty (only plan checks run).
func CheckJSON(inputJSON, rulesJSON []byte) ([]byte, error) {
	var in Input
	if err := json.Unmarshal(inputJSON, &in); err != nil {
		return nil, fmt.Errorf("input: %w", err)
	}
	var rs *RuleSet
	if len(rulesJSON) > 0 {
		rs = &RuleSet{}
		if err := json.Unmarshal(rulesJSON, rs); err != nil {
			return nil, fmt.Errorf("rules: %w", err)
		}
		if err := Validate(rs); err != nil {
			return nil, fmt.Errorf("rules: %w", err)
		}
	}
	return json.Marshal(Check(in, rs))
}

// Validate reports problems in a rule set, so a broken rules/*.json fails
// loudly instead of producing silent passes.
func Validate(rs *RuleSet) error {
	seen := map[string]bool{}
	for i, r := range rs.Rules {
		where := fmt.Sprintf("rule %d (%s)", i, r.ID)
		if r.ID == "" {
			return fmt.Errorf("%s: missing id", where)
		}
		if seen[r.ID] {
			return fmt.Errorf("%s: duplicate id", where)
		}
		seen[r.ID] = true
		switch r.AppliesTo {
		case "room", "window", "door", "building":
		default:
			return fmt.Errorf("%s: appliesTo must be room, window, door or building, got %q", where, r.AppliesTo)
		}
		conds := append([]Condition{}, r.Checks...)
		if r.Check != nil {
			conds = append(conds, *r.Check)
		}
		if r.Manual == "" && len(conds) == 0 {
			return fmt.Errorf("%s: needs check, checks or manual", where)
		}
		for _, c := range append(conds, r.When...) {
			if c.Property == "" {
				return fmt.Errorf("%s: condition property is empty", where)
			}
			switch c.Op {
			case ">=", ">", "<=", "<":
				if _, ok := c.number(); !ok {
					return fmt.Errorf("%s: %s %s needs a numeric value", where, c.Property, c.Op)
				}
			case "==", "in":
			default:
				return fmt.Errorf("%s: op %q not supported", where, c.Op)
			}
		}
		if r.OnFail != "" && r.OnFail != "manual" {
			return fmt.Errorf("%s: onFail must be \"manual\"", where)
		}
		switch r.Quantifier {
		case "", "all", "any":
		default:
			return fmt.Errorf("%s: quantifier must be all or any", where)
		}
		if r.Source.Title == "" {
			return fmt.Errorf("%s: source.title is required", where)
		}
	}
	return nil
}

var (
	checkMu sync.Mutex
	lang    = i18n.EN
)

// tr and trf translate engine messages into the language of the running Check.
func tr(s string) string                 { return lang.T(s) }
func trf(format string, a ...any) string { return lang.F(format, a...) }
func noun(n string, count int) string    { return lang.Noun(n, count) }

// localized returns the rule set with titles and messages in Swedish where
// a rule has them ("sv": {...} in the JSON).
func (rs *RuleSet) localized() *RuleSet {
	c := *rs
	c.Rules = make([]Rule, len(rs.Rules))
	for i, r := range rs.Rules {
		if r.SV != nil {
			if r.SV.Title != "" {
				r.Title = r.SV.Title
			}
			if r.SV.Short != "" {
				r.Short = r.SV.Short
			}
			if r.SV.Manual != "" {
				r.Manual = r.SV.Manual
			}
			if r.SV.FailHint != "" {
				r.FailHint = r.SV.FailHint
			}
		}
		c.Rules[i] = r
	}
	return &c
}
