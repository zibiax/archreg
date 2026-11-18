package app

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/martin/archreg/engine/rules"
)

// Rule-based answers (README option 1): explain checks and plan regulations
// from the data the app already has. No language model, works offline.

var glossary = []struct {
	re  *regexp.Regexp
	ans string
}{
	{regexp.MustCompile(`(?i)prick`), "Prickmark (dotted land on the plan map) is land that may not be built on. Small things like fences may still be allowed, but no buildings."},
	{regexp.MustCompile(`(?i)kors`), "Korsmark (cross-hatched land) may only have komplementbyggnader: sheds, garages, carports and similar — not the main building."},
	{regexp.MustCompile(`(?i)nock`), "Nockhöjd (ridge height) is the height from the average ground level around the building to the top of the roof ridge. Chimneys and similar don't count."},
	{regexp.MustCompile(`(?i)totalh`), "Totalhöjd (total height) is the height to the building's highest point, including everything on the roof."},
	{regexp.MustCompile(`(?i)byggnadsh`), "Byggnadshöjd is measured to where the facade meets the roof plane. Its exact definition depends on when the plan was adopted: plans from before 2 May 2011 use the older ÄPBL definition."},
	{regexp.MustCompile(`(?i)\bbya\b|byggnadsarea`), "Byggnadsarea (BYA) is the area the building covers on the ground, including overhanging parts more than a certain size. The app uses the footprint of the model."},
	{regexp.MustCompile(`(?i)\bbta\b|bruttoarea`), "Bruttoarea (BTA) is the total floor area of all storeys measured to the outside of the walls."},
	{regexp.MustCompile(`(?i)laga kraft`), "Laga kraft means the plan is legally binding: it can no longer be appealed."},
	{regexp.MustCompile(`(?i)detaljplan`), "A detaljplan is the municipality's legally binding plan for an area: what may be built, where, how high and for what use."},
	{regexp.MustCompile(`(?i)utrymning|escape`), "An escape window (fönster för utrymning, BFS 2024:7 2 kap. 26 §) must have a free opening at least 0.50 m wide and 0.60 m high, width + height at least 1.50 m, and its lower edge at most 1.20 m above the floor. Mark escape windows in the Model tab."},
	{regexp.MustCompile(`(?i)dagsljus|daylight`), "Dwellings need a daylight factor of at least 1.0 % for at least half the area of rooms where people stay more than briefly (BFS 2024:8 4 kap. 1 §). That needs a daylight calculation."},
	{regexp.MustCompile(`(?i)rumshöjd|ceiling|takhöjd`), "The new rules (BFS 2024:8 5 kap. 1 §) don't give a number for room height: it must be 'sufficient'. The app compares with the old BBR value of 2.40 m as a reference only."},
	{regexp.MustCompile(`(?i)hiss|elevator|lift`), "BFS 2024:12 2 kap. 2 §: if a communication space has a level difference of more than 1 m there must be a lift. Buildings with more than ten storeys need more than one."},
	{regexp.MustCompile(`(?i)cykel|bike|bicycle`), "Since 1 July 2026 (BFS 2026:4), an apartment building needs at least two bicycle parking spaces per apartment. Enter the numbers under Model → Project info."},
	{regexp.MustCompile(`(?i)ladd|charg`), "BFS 2026:4: apartment buildings with more than 3 car parking spaces need at least one charging point plus cabling for half the spaces; non-residential buildings with more than 5 need one charging point per five spaces."},
	{regexp.MustCompile(`(?i)energi|energy`), "Energy rules are in BFS 2026:9 (from 1 October 2026, replacing BBR chapter 9). They need an energy calculation, so the app marks them as 'check manually'."},
}

func (a *App) answer(q string) string {
	ql := strings.ToLower(q)

	// "What does e1 mean?" — look for a plan regulation label.
	if p := a.plan(); p != nil {
		for _, r := range p.Regulations {
			if r.Label != "" && regexp.MustCompile(`(?i)(^|\W)`+regexp.QuoteMeta(r.Label)+`($|\W)`).MatchString(q) {
				_, plain := rules.Describe(r)
				return fmt.Sprintf("%s in plan %s: \"%s\" — %s", r.Label, p.ID, r.Text, plain)
			}
		}
	}

	why := strings.Contains(ql, "why") || strings.Contains(ql, "varför") || strings.Contains(ql, "fail") || strings.Contains(ql, "wrong") || strings.Contains(ql, "fel")
	if why && a.Output != nil {
		// Look for a result mentioning a word from the question.
		var hits []rules.Result
		for _, r := range a.Output.Results {
			if r.Status != rules.Fail && r.Status != rules.Missing {
				continue
			}
			text := strings.ToLower(r.Title + " " + r.Message)
			for _, w := range strings.Fields(ql) {
				w = strings.Trim(w, "?.,!\"")
				if len(w) >= 4 && w != "does" && w != "fail" && w != "fails" && w != "varför" && strings.Contains(text, strings.TrimSuffix(w, "s")) {
					hits = append(hits, r)
					break
				}
			}
		}
		if len(hits) == 0 {
			if r := a.selectedResult(); r != nil {
				hits = []rules.Result{*r}
			}
		}
		if len(hits) > 0 {
			var b strings.Builder
			for i, r := range hits {
				if i == 3 {
					fmt.Fprintf(&b, " …and %d more.", len(hits)-3)
					break
				}
				if i > 0 {
					b.WriteString(" ")
				}
				fmt.Fprintf(&b, "%s: %s (%s%s).", r.Title, r.Message, r.Source.Title, sect(r.Source.Section))
			}
			return b.String()
		}
		fails := 0
		for _, r := range a.Output.Results {
			if r.Status == rules.Fail {
				fails++
			}
		}
		if fails == 0 {
			return "Nothing fails right now. Some things still need checking by hand — see the 👁 items in Results."
		}
		return fmt.Sprintf("%d checks fail. Open the Results tab and click one to see the rule text and source, or ask about something specific, e.g. \"why does the window fail?\".", fails)
	}

	for _, g := range glossary {
		if g.re.MatchString(q) {
			return g.ans
		}
	}

	if strings.Contains(ql, "height") || strings.Contains(ql, "höjd") || strings.Contains(ql, "how high") || strings.Contains(ql, "hur hög") {
		if p := a.plan(); p != nil {
			var hs []string
			for _, r := range p.Regulations {
				if k, plain := rules.Describe(r); strings.HasSuffix(k, "hojd") {
					hs = append(hs, plain)
				}
			}
			if len(hs) > 0 {
				return "In plan " + p.ID + ": " + strings.Join(hs, " ")
			}
			return "Plan " + p.ID + " has no height limit I can read. Check the plan document."
		}
		return "Drop a pin first so I know which plan applies."
	}

	if strings.Contains(ql, "pass") || strings.Contains(ql, "klarar") || strings.Contains(ql, "ok") || strings.Contains(ql, "summary") || strings.Contains(ql, "sammanfatt") {
		if a.Output == nil {
			return "Import a model first, then I can tell you how it does."
		}
		s := a.Output.Summary
		return fmt.Sprintf("%d fail, %d need checking by hand, %d lack data, %d pass. The app never shows a pass it can't back up.", s[rules.Fail], s[rules.Manual], s[rules.Missing], s[rules.Pass])
	}

	return "I can explain plan regulations (\"what does e1 mean?\", \"what is prickmark?\"), why something fails (\"why does the bathroom fail?\"), height limits, and the main building rules (escape windows, daylight, lifts, bike parking). I'm rule-based, not an AI, so I only know what's in the app."
}

func sect(s string) string {
	if s == "" {
		return ""
	}
	return " " + s
}
