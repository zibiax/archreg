package rules

import (
	"fmt"
	"strings"
)

// checkRuleSet evaluates hand-written building regulation rules.
func checkRuleSet(m *Model, rs *RuleSet) []Result {
	b := buildingElement(m)
	var out []Result
	for _, rule := range rs.Rules {
		out = append(out, checkRule(m, b, rule)...)
	}
	return out
}

// outcome of evaluating one element against a rule's conditions.
type outcome struct {
	status   Status
	msg      string
	measured *float64
	required *float64
	unit     string
	op       string
	what     string
}

func checkRule(m *Model, building Element, rule Rule) []Result {
	base := Result{
		RuleID: rule.ID, Group: GroupRegulations, Title: rule.Title, Short: rule.Short,
		Source: rule.Source, Draft: !rule.Verified, Text: rule.Text,
	}
	res := func(s Status, msg string, elems []string) Result {
		r := base
		r.Status, r.Message, r.Elements = s, msg, elems
		return r
	}

	// Does the rule apply to this building at all?
	for _, c := range rule.When {
		o := evalCondition(building, c)
		switch o.status {
		case Missing:
			return []Result{res(Missing, fmt.Sprintf("Needs the building's %s to know whether this rule applies. Set it under Project info.", humanProp(c.Property)), nil)}
		case Fail, Manual:
			return nil // not applicable
		}
	}

	var elems []Element
	noun := rule.AppliesTo
	switch rule.AppliesTo {
	case "room":
		elems = m.Rooms
	case "window":
		elems = m.Windows
	case "door":
		elems = m.Doors
	case "building":
		elems = []Element{building}
	default:
		return []Result{res(Manual, fmt.Sprintf("Unknown rule target %q. Fix the rule definition.", rule.AppliesTo), nil)}
	}

	var matching []Element
	for _, e := range elems {
		if matches(e, rule.Where) {
			matching = append(matching, e)
		}
	}

	if rule.Manual != "" {
		return []Result{res(Manual, rule.Manual, ids(matching))}
	}
	if len(elems) == 0 {
		return []Result{res(Missing, fmt.Sprintf("The model has no %ss, so this can't be checked. Make sure %ss are included in the export.", noun, noun), nil)}
	}
	if len(matching) == 0 {
		return []Result{res(Manual, fmt.Sprintf("No %ss matching %s were found in the model. If there should be, set their type or name so the app can recognise them.", noun, describeWhere(rule.Where)), nil)}
	}

	conds := rule.Checks
	if rule.Check != nil {
		conds = append([]Condition{*rule.Check}, conds...)
	}
	binding := rule.Binding == nil || *rule.Binding

	// Evaluate every matching element.
	type evaluated struct {
		e Element
		o outcome
	}
	var evs []evaluated
	for _, e := range matching {
		evs = append(evs, evaluated{e, evalAll(e, conds, label(e, noun))})
	}

	// softenPass turns a pass we can't fully back up into "manual".
	softenPass := func(n int) (Status, string) {
		switch {
		case !binding:
			return Manual, ""
		case rule.Necessary:
			return Manual, fmt.Sprintf("%d %s(s) meet the measurable part of this rule, but that alone doesn't prove compliance. Check the rest by hand.", n, noun)
		case !rule.Verified:
			return Manual, fmt.Sprintf("%d %s(s) meet the rule's value, but this rule hasn't been verified against the regulation text yet.", n, noun)
		}
		return Pass, fmt.Sprintf("%d %s(s) meet this rule.", n, noun)
	}

	if rule.Quantifier == "any" {
		var passed, manual, failed, missing []string
		var firstFail outcome
		for _, ev := range evs {
			switch ev.o.status {
			case Pass:
				passed = append(passed, elemID(ev.e))
			case Manual:
				manual = append(manual, elemID(ev.e))
			case Fail:
				if len(failed) == 0 {
					firstFail = ev.o
				}
				failed = append(failed, elemID(ev.e))
			default:
				missing = append(missing, elemID(ev.e))
			}
		}
		switch {
		case len(passed) > 0:
			s, msg := softenPass(len(passed))
			if !binding {
				msg = fmt.Sprintf("At least one %s meets the reference value. This is guidance, not a binding requirement.", noun)
			} else if s != Pass || rule.Necessary {
				msg = fmt.Sprintf("At least one %s meets the measurable part. Check the rest by hand.", noun)
			} else {
				msg = fmt.Sprintf("At least one %s meets this rule.", noun)
			}
			return []Result{res(s, msg, passed)}
		case len(manual) > 0:
			return []Result{res(Manual, fmt.Sprintf("No %s can be confirmed to meet this rule from the model's data. Check by hand.", noun), manual)}
		case len(failed) > 0 && len(missing) == 0:
			r := res(Fail, fmt.Sprintf("None of the %d %ss meets this rule. For example: %s", len(failed), noun, firstFail.msg), failed)
			r.Measured, r.Required, r.Unit, r.Op, r.What = firstFail.measured, firstFail.required, firstFail.unit, firstFail.op, firstFail.what
			if rule.OnFail == "manual" {
				r.Status = Manual
				r.Message += " " + rule.FailHint
			} else if !binding {
				r.Status = Manual
				r.Message = "None meets the reference value (guidance, not binding). " + firstFail.msg
			}
			return []Result{r}
		default:
			return []Result{res(Missing, fmt.Sprintf("The model lacks the data to check this for %d %s(s).", len(missing), noun), missing)}
		}
	}

	var out []Result
	var passed []string
	for _, ev := range evs {
		o := ev.o
		switch o.status {
		case Pass:
			passed = append(passed, elemID(ev.e))
			continue
		case Fail:
			if rule.OnFail == "manual" {
				o.status = Manual
				o.msg += " " + rule.FailHint
			} else if !binding {
				o.status = Manual
				o.msg += " This value is guidance, not a binding requirement."
			}
		}
		r := res(o.status, o.msg, []string{elemID(ev.e)})
		r.Measured, r.Required, r.Unit, r.Op, r.What = o.measured, o.required, o.unit, o.op, o.what
		out = append(out, r)
	}
	if len(passed) > 0 {
		s, msg := softenPass(len(passed))
		if !binding {
			msg = fmt.Sprintf("%d %s(s) meet the reference value. This is guidance, not a binding requirement.", len(passed), noun)
		}
		out = append(out, res(s, msg, passed))
	}
	return out
}

// evalAll combines conditions: any fail → fail, then missing, then manual.
func evalAll(e Element, conds []Condition, name string) outcome {
	worst := outcome{status: Pass}
	rank := map[Status]int{Pass: 0, Manual: 1, Missing: 2, Fail: 3}
	for _, c := range conds {
		o := evalCondition(e, c)
		if rank[o.status] > rank[worst.status] {
			worst = o
		}
	}
	if worst.status != Pass {
		worst.msg = name + ": " + worst.msg
	}
	return worst
}

func evalCondition(e Element, c Condition) outcome {
	prop := humanProp(c.Property)
	if c.Op == "in" || c.Op == "==" {
		v, ok := e[c.Property]
		if !ok {
			return outcome{status: Missing, msg: "has no " + prop + "."}
		}
		allowed, isList := c.Value.([]any)
		if !isList {
			allowed = []any{c.Value}
		}
		for _, a := range allowed {
			if equalLoose(v, a) {
				return outcome{status: Pass}
			}
		}
		return outcome{status: Fail, msg: fmt.Sprintf("%s is %v.", prop, v)}
	}

	want, ok := c.number()
	if !ok {
		return outcome{status: Manual, msg: "rule value for " + prop + " isn't a number."}
	}
	o := outcome{required: ptr(want), unit: c.Unit, op: c.Op, what: prop}
	v, ok := number(e, c.Property)
	if !ok {
		if c.Upper != "" {
			if up, ok := number(e, c.Upper); ok {
				o.measured = ptr(round(up, 2))
				o.what = humanProp(c.Upper)
				if (c.Op == ">=" || c.Op == ">") && !compareOp(up, c.Op, want) {
					o.status = Fail
					o.msg = fmt.Sprintf("%s is %s %s, so the %s is certainly below %s %s.", humanProp(c.Upper), fmtNum(up), c.Unit, prop, fmtNum(want), c.Unit)
					return o
				}
				o.status = Manual
				o.msg = fmt.Sprintf("%s is %s %s, but the %s (needs to be %s %s %s) isn't in the model. Measure it.", humanProp(c.Upper), fmtNum(up), c.Unit, prop, opWord(c.Op), fmtNum(want), c.Unit)
				return o
			}
		}
		o.status = Missing
		o.msg = "has no " + prop + "."
		return o
	}
	o.measured = ptr(round(v, 2))
	if compareOp(v, c.Op, want) {
		o.status = Pass
		return o
	}
	o.status = Fail
	o.msg = fmt.Sprintf("%s is %s %s, needs to be %s %s %s.", prop, fmtNum(v), c.Unit, opWord(c.Op), fmtNum(want), c.Unit)
	return o
}

// buildingElement merges user-entered project facts with values derived
// from the model, so building-level rules can refer to them.
func buildingElement(m *Model) Element {
	e := Element{"id": "building", "name": "Building"}
	for k, v := range m.Building {
		e[k] = v
	}
	set := func(k string, v *float64) {
		if v != nil {
			e[k] = *v
		}
	}
	set("ridgeHeight", m.RidgeHeight)
	set("totalHeight", m.TotalHeight)
	set("grossFloorArea", m.GrossFloorArea)
	if m.Storeys != nil {
		e["storeys"] = float64(*m.Storeys)
	}
	if len(m.Rooms) > 0 {
		counts := map[string]float64{}
		for _, r := range m.Rooms {
			if t, ok := r["type"].(string); ok {
				counts[strings.ToLower(t)]++
			}
		}
		for _, t := range []string{"bathroom", "kitchen", "bedroom", "living"} {
			e[t+"Count"] = counts[t]
		}
	}
	if c, ok := toFloat(e["carParking"]); ok && c > 0 {
		if p, ok := toFloat(e["chargingPoints"]); ok {
			e["chargingPointsPerCarParking"] = p / c
		}
	}
	if a, ok := toFloat(e["apartments"]); ok && a > 0 {
		if b, ok := toFloat(e["bikeParking"]); ok {
			e["bikeParkingPerApartment"] = b / a
		}
	}
	return e
}

func matches(e Element, where map[string][]any) bool {
	for k, allowed := range where {
		v, ok := e[k]
		if !ok {
			return false
		}
		hit := false
		for _, a := range allowed {
			if equalLoose(v, a) {
				hit = true
				break
			}
		}
		if !hit {
			return false
		}
	}
	return true
}

func equalLoose(a, b any) bool {
	as, aok := a.(string)
	bs, bok := b.(string)
	if aok && bok {
		return strings.EqualFold(as, bs)
	}
	af, aok := toFloat(a)
	bf, bok := toFloat(b)
	if aok && bok {
		return af == bf
	}
	ab, aok := a.(bool)
	bb, bok := b.(bool)
	return aok && bok && ab == bb
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	}
	return 0, false
}

// number reads a numeric property, deriving a few common ones.
func number(e Element, prop string) (float64, bool) {
	if v, ok := toFloat(e[prop]); ok {
		return v, true
	}
	w, okW := toFloat(e["width"])
	h, okH := toFloat(e["height"])
	switch prop {
	case "area":
		if okW && okH {
			return w * h, true
		}
	case "widthPlusHeight":
		if okW && okH {
			return w + h, true
		}
	case "openingWidthPlusHeight":
		ow, ok1 := toFloat(e["openingWidth"])
		oh, ok2 := toFloat(e["openingHeight"])
		if ok1 && ok2 {
			return ow + oh, true
		}
	}
	return 0, false
}

func compareOp(v float64, op string, x float64) bool {
	const eps = 1e-9
	switch op {
	case ">=":
		return v >= x-eps
	case ">":
		return v > x
	case "<=":
		return v <= x+eps
	case "<":
		return v < x
	case "==":
		return abs(v-x) < eps
	}
	return false
}

func opWord(op string) string {
	switch op {
	case ">=":
		return "at least"
	case ">":
		return "more than"
	case "<=":
		return "at most"
	case "<":
		return "less than"
	case "==":
		return "exactly"
	}
	return op
}

func humanProp(p string) string {
	var b strings.Builder
	for i, r := range p {
		if i > 0 && r >= 'A' && r <= 'Z' {
			b.WriteByte(' ')
			r += 'a' - 'A'
		}
		b.WriteRune(r)
	}
	return b.String()
}

func describeWhere(w map[string][]any) string {
	if len(w) == 0 {
		return "the rule"
	}
	var parts []string
	for k, v := range w {
		var vs []string
		for _, x := range v {
			vs = append(vs, fmt.Sprint(x))
		}
		parts = append(parts, k+" = "+strings.Join(vs, " / "))
	}
	return strings.Join(parts, ", ")
}

func elemID(e Element) string {
	if s, ok := e["id"].(string); ok {
		return s
	}
	return fmt.Sprint(e["id"])
}

func label(e Element, noun string) string {
	name, _ := e["name"].(string)
	if name == "" {
		name = elemID(e)
	}
	return capitalize(noun) + " " + name
}

func ids(es []Element) []string {
	out := make([]string, 0, len(es))
	for _, e := range es {
		out = append(out, elemID(e))
	}
	return out
}
