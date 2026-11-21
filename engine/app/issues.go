package app

import (
	"fmt"
	"strings"

	"github.com/martin/archreg/engine/rules"
)

// The Issues view lists one row per rule, not per element: three escape
// windows that fail the same way are one row "Escape window ×3". Rows are
// sorted worst first; passes and "check by hand" are folded away.

// issue is one row: the results of one rule (or plan regulation) that
// share a status.
type issue struct {
	Key      string
	I        int // index of the first result, for selecting
	Status   rules.Status
	Title    string
	Metric   string // "1.1 m · needs ≥ 1.5 m"
	Count    int    // elements affected; shown as ×N when > 1
	Tag      string // "Plan" or the regulation, "BFS 2024:12"
	Selected bool
	R        rules.Result   // first result: quote, source, draft flag
	Members  []issueMember  // each element with its own figure
	List     bool           // show Members as a list (several elements with figures)
	results  []rules.Result // all results in the row
}

type issueMember struct {
	Name, Metric, Message string
}

type issueSection struct {
	Status rules.Status
	Label  string
	Issues []issue
	Open   bool
}

func issueKey(r rules.Result) string {
	return r.RuleID + "|" + r.RegulationID + "|" + string(r.Status)
}

// issues groups the output into sections: must fix, missing info, check
// by hand, passed.
func (a *App) issues() []issueSection {
	if a.Output == nil {
		return nil
	}
	names := a.elementNames()
	sel := ""
	if r := a.selectedResult(); r != nil {
		sel = issueKey(*r)
	}
	byKey := map[string]*issue{}
	var order []string
	for i, r := range a.Output.Results {
		k := issueKey(r)
		is, ok := byKey[k]
		if !ok {
			title := r.Short
			if title == "" {
				title = r.Title
			}
			tag := "Plan"
			if r.Group != rules.GroupPlan {
				tag = r.Source.Title
				if i := strings.Index(tag, " "); i > 0 {
					if j := strings.Index(tag[i+1:], " "); j > 0 {
						tag = tag[:i+1+j] // "BFS 2024:12"
					}
				}
			}
			is = &issue{Key: k, I: i, Status: r.Status, Title: title, Tag: tag, R: r, Selected: k == sel, Metric: metric(r)}
			if r.Status == rules.Missing && is.Metric == "" {
				is.Metric = missingHint(r)
			}
			byKey[k] = is
			order = append(order, k)
		}
		is.results = append(is.results, r)
		var ns []string
		for _, id := range r.Elements {
			if n, ok := names[id]; ok && n != "" {
				ns = append(ns, n)
			}
		}
		is.Members = append(is.Members, issueMember{Name: strings.Join(ns, ", "), Metric: metric(r), Message: r.Message})
	}
	// ×N counts results that are about one element each (three windows),
	// not a single finding that names many ("none of the 6 rooms…"), nor
	// the summary line "6 rooms meet the reference value".
	for _, is := range byKey {
		n := 0
		for _, r := range is.results {
			if r.Measured != nil {
				n++
			}
		}
		if n == 0 {
			n = len(is.results)
		}
		is.Count = n
		// List the elements only when they differ; three identical
		// "Fönster sovrum · 1.1 m" lines add nothing to "×3".
		distinct := map[string]bool{}
		for _, m := range is.Members {
			if m.Metric != "" {
				distinct[m.Name+"|"+m.Metric] = true
			}
		}
		is.List = len(distinct) > 1
	}

	sections := []issueSection{
		{Status: rules.Fail, Label: "Must fix", Open: true},
		{Status: rules.Missing, Label: "Missing info", Open: true},
		{Status: rules.Manual, Label: "Check by hand"},
		{Status: rules.Pass, Label: "Passed"},
	}
	for _, k := range order {
		is := byKey[k]
		for i := range sections {
			if sections[i].Status == is.Status {
				sections[i].Issues = append(sections[i].Issues, *is)
				if is.Selected {
					sections[i].Open = true
				}
			}
		}
	}
	return sections
}

// selectedIssue returns every result in the selected row.
func (a *App) selectedIssue() []rules.Result {
	r := a.selectedResult()
	if r == nil {
		return nil
	}
	k := issueKey(*r)
	var out []rules.Result
	for _, x := range a.Output.Results {
		if issueKey(x) == k {
			out = append(out, x)
		}
	}
	return out
}

// metric is the one figure that matters: "1.2 m · needs ≥ 1.3 m".
func metric(r rules.Result) string {
	if r.Measured == nil || r.Required == nil {
		return ""
	}
	n := func(v float64) string {
		f := "%.2f"
		if r.Unit == "m²" && v >= 100 {
			f = "%.0f"
		}
		s := strings.TrimRight(strings.TrimRight(fmt.Sprintf(f, v), "0"), ".")
		if f == "%.0f" {
			s = fmt.Sprintf(f, v)
		}
		if r.Unit != "" {
			s += " " + r.Unit
		}
		return s
	}
	what := ""
	if r.Group != rules.GroupPlan && r.What != "" {
		what = strings.NewReplacer("opening ", "", " plus ", " + ", "minimum ", "min ").Replace(r.What) + " "
	}
	m, q := *r.Measured, *r.Required
	switch r.Op {
	case "==":
		if r.What == "area on prickmark" {
			return n(m) + " on prickmark"
		}
		return what + n(m)
	case "<=", "<":
		return fmt.Sprintf("%s%s · max %s", what, n(m), n(q))
	case ">=", ">":
		return fmt.Sprintf("%s%s · needs ≥ %s", what, n(m), n(q))
	}
	return what + n(m)
}

// missingHint says where to supply what a check lacks.
func missingHint(r rules.Result) string {
	switch {
	case r.Group == rules.GroupPlan:
		return "Needs the building's outline or height in the model"
	case len(r.Elements) == 1 && r.Elements[0] == "building", strings.Contains(r.Message, "Project"):
		return "Add it in Model › Project details"
	}
	return "Not in the model export"
}
