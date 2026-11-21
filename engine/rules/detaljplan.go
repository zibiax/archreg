package rules

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ctessum/polyclip-go"
)

// regKind is what the engine knows how to check for a plan regulation.
type regKind string

const (
	kindPrickmark      regKind = "prickmark"
	kindKorsmark       regKind = "korsmark"
	kindRidgeHeight    regKind = "nockhojd"
	kindTotalHeight    regKind = "totalhojd"
	kindBuildingHeight regKind = "byggnadshojd"
	kindBuildingArea   regKind = "byggnadsarea"
	kindFloorArea      regKind = "bruttoarea"
	kindStoreys        regKind = "vaningar"
	kindBoundaryDist   regKind = "avstand"
	kindUnknown        regKind = ""
)

var numberRe = regexp.MustCompile(`(\d+(?:[.,]\d+)?)`)

// classify maps a planbestämmelse to something checkable, using the
// formulation text (the catalogue wording from Boverket's
// planbestämmelsekatalog) and the value/unit from NGP.
func classify(r PlanRegulation) regKind {
	t := strings.ToLower(r.Text + " " + r.Subcategory)
	switch {
	case strings.Contains(t, "får inte förses med byggnad") || strings.Contains(t, "får inte bebyggas"):
		return kindPrickmark
	case strings.Contains(t, "endast förses med komplementbyggnad") || strings.Contains(t, "endast förses med uthus"):
		return kindKorsmark
	case strings.Contains(t, "nockhöjd"):
		return kindRidgeHeight
	case strings.Contains(t, "totalhöjd"):
		return kindTotalHeight
	case strings.Contains(t, "byggnadshöjd"):
		return kindBuildingHeight
	case strings.Contains(t, "byggnadsarea"):
		return kindBuildingArea
	case strings.Contains(t, "bruttoarea") || strings.Contains(t, "bruttototalarea"):
		return kindFloorArea
	case strings.Contains(t, "antal våningar") || strings.Contains(t, "antal våningsplan"):
		return kindStoreys
	case strings.Contains(t, "från fastighetsgräns"):
		return kindBoundaryDist
	}
	return kindUnknown
}

// isUse: an användningsbestämmelse (B = bostäder, K = kontor, ...).
var useLabelRe = regexp.MustCompile(`^[A-ZÅÄÖ]{1,4}\d?$`)

func isUse(r PlanRegulation) bool {
	c := strings.ToLower(r.Category)
	return strings.Contains(c, "användning") || (r.Category == "" && useLabelRe.MatchString(r.Label) && len(r.Text) < 60)
}

func regValue(r PlanRegulation) (float64, bool) {
	if r.Value != nil {
		return *r.Value, true
	}
	m := numberRe.FindString(r.Text)
	if m == "" {
		return 0, false
	}
	v, err := strconv.ParseFloat(strings.Replace(m, ",", ".", 1), 64)
	return v, err == nil
}

func isMin(r PlanRegulation) bool {
	t := strings.ToLower(r.Text)
	return r.ValueType == "min" || strings.HasPrefix(t, "minsta") || strings.HasPrefix(t, "lägsta") || strings.Contains(t, "minst ")
}

func isPercent(r PlanRegulation) bool {
	return r.Unit == "%" || r.Unit == "procent" || strings.Contains(r.Text, "%") || strings.Contains(strings.ToLower(r.Text), "procent")
}

// isAbsoluteHeight: "+24,5 meter över nollplanet" refers to RH 2000, not to ground.
func isAbsoluteHeight(r PlanRegulation) bool {
	t := strings.ToLower(r.Text + " " + r.Unit)
	return strings.Contains(t, "nollplan") || strings.Contains(t, "rh 2000") || strings.Contains(t, "rh2000")
}

// pbl2011 is when the current PBL took effect (2 May 2011). Plans adopted
// before it use the ÄPBL definitions (e.g. of byggnadshöjd).
var pbl2011 = time.Date(2011, 5, 2, 0, 0, 0, 0, time.UTC)

type planContext struct {
	plan      *Plan
	model     *Model
	footprint polyclip.Polygon
	fpArea    float64
	approx    bool // footprint overestimates the real building
	source    Source
}

func checkPlan(m *Model, p *Plan) []Result {
	src := Source{Title: planTitle(p), URL: p.URL}
	if p.PDFOnly {
		return []Result{{
			RuleID: "dp.pdfOnly", Group: GroupPlan, Status: Manual, Source: src,
			Title:   tr("Plan only available as a scanned document"),
			Message: tr("This detaljplan isn't digital, so it can't be checked automatically. Read the plan document and check it by hand."),
		}}
	}
	ctx := &planContext{plan: p, model: m, source: src, approx: m.FootprintMethod != "" && m.FootprintMethod != "exact"}
	if len(m.Footprint) >= 3 {
		ctx.footprint = polyclip.Polygon{toContour(m.Footprint)}
		ctx.fpArea = area(ctx.footprint)
	}

	var out []Result
	if r, ok := ctx.withinPlan(); ok {
		out = append(out, r)
	}
	for _, reg := range p.Regulations {
		out = append(out, ctx.checkRegulation(reg))
	}
	if len(p.Regulations) == 0 {
		out = append(out, Result{
			RuleID: "dp.noRegulations", Group: GroupPlan, Status: Missing, Source: src,
			Title:   tr("No regulations found for this plan"),
			Message: tr("The plan data has no regulations, so nothing could be checked. Read the plan document."),
		})
	}
	return out
}

func planTitle(p *Plan) string {
	if p.Name != "" {
		return fmt.Sprintf("Detaljplan %s (%s)", p.ID, p.Name)
	}
	return "Detaljplan " + p.ID
}

func (c *planContext) noFootprint(id, title string, reg PlanRegulation) Result {
	return c.result(id, title, reg, Missing, tr("The model has no footprint placed on the map. Place the building on the site first."))
}

func (c *planContext) result(id, title string, reg PlanRegulation, s Status, msg string) Result {
	src := c.source
	if reg.Label != "" {
		src.Section = reg.Label
	}
	short := Short(reg, lang)
	if reg.Label != "" && reg.Label != "prickmark" && reg.Label != "korsmark" {
		short = reg.Label + " · " + short
	}
	return Result{
		RuleID: id, Group: GroupPlan, Status: s, Title: title, Message: msg, Short: short,
		Source: src, RegulationID: reg.ID, Elements: []string{"footprint"}, Text: reg.Text,
	}
}

func (c *planContext) withinPlan() (Result, bool) {
	if c.plan.Boundary == nil || c.footprint == nil {
		return Result{}, false
	}
	bp, err := c.plan.Boundary.polygons()
	if err != nil {
		return Result{}, false
	}
	inside := intersectionArea(c.footprint, bp)
	r := c.result("dp.withinPlan", tr("Building inside the plan area"), PlanRegulation{}, Pass,
		tr("The whole building is inside this plan's area."))
	r.Short = tr("Inside plan area")
	if outside := c.fpArea - inside; outside > 0.01 {
		r.Status = Manual
		r.Message = trf("%.1f m² of the building is outside this plan's area. Another plan (or none) applies there.", outside)
		if c.approx {
			r.Message += " " + tr("The footprint is approximate, so this may be smaller in reality.")
		}
	}
	return r, true
}

// overlap returns the area of the footprint inside the regulation's area.
// Regulations without geometry apply to the whole plan.
func (c *planContext) overlap(reg PlanRegulation) (float64, error) {
	if reg.Geometry == nil {
		return c.fpArea, nil
	}
	rp, err := reg.Geometry.polygons()
	if err != nil {
		return 0, err
	}
	return intersectionArea(c.footprint, rp), nil
}

func (c *planContext) checkRegulation(reg PlanRegulation) Result {
	kind := classify(reg)
	id := "dp." + string(kind)
	if kind == kindUnknown {
		id = "dp.manual"
	}
	title := reg.Text
	if reg.Label != "" {
		title = reg.Label + ": " + reg.Text
	}

	if kind == kindUnknown {
		if isUse(reg) {
			return c.result("dp.use", title, reg, Manual, trf("The land is for: %s. Check that what you're designing is that kind of building.", reg.Text))
		}
		return c.result(id, title, reg, Manual, tr("This regulation can't be checked from a model. Read it and judge whether the design meets it."))
	}
	if c.footprint == nil {
		return c.noFootprint(id, title, reg)
	}
	ov, err := c.overlap(reg)
	if err != nil {
		return c.result(id, title, reg, Manual, trf("The regulation's area couldn't be read (%s). Check it by hand.", err.Error()))
	}
	const eps = 0.01 // m²
	if ov <= eps && kind != kindBoundaryDist {
		return c.result(id, title, reg, Pass, tr("The building isn't in the area this regulation covers."))
	}

	switch kind {
	case kindPrickmark:
		r := c.result(id, title, reg, Fail, trf("%.1f m² of the building is on prickmark (land that may not be built on).", ov))
		r.Measured, r.Required, r.Unit, r.Op, r.What = ptr(round(ov, 1)), ptr(0), "m²", "==", "area on prickmark"
		if c.approx {
			r.Status = Manual
			r.Message += " " + trf("The footprint is approximate (%s), so check the real outline.", c.model.FootprintMethod)
		}
		return r
	case kindKorsmark:
		return c.result(id, title, reg, Manual, trf("%.1f m² of the building is on korsmark, where only komplementbyggnader (sheds, garages and similar) are allowed. Check that this part is one.", ov))
	case kindRidgeHeight:
		return c.height(id, title, reg, c.model.RidgeHeight, tr("nockhöjd (ridge height)"))
	case kindTotalHeight:
		return c.height(id, title, reg, c.model.TotalHeight, tr("totalhöjd (total height)"))
	case kindBuildingHeight:
		return c.buildingHeight(id, title, reg)
	case kindBuildingArea:
		return c.areaLimit(id, title, reg, c.fpArea, "byggnadsarea (BYA)", c.approx)
	case kindFloorArea:
		if c.model.GrossFloorArea == nil {
			return c.result(id, title, reg, Missing, tr("The model has no gross floor area (BTA). Add floor slabs or rooms on every storey."))
		}
		return c.areaLimit(id, title, reg, *c.model.GrossFloorArea, "bruttoarea (BTA)", false)
	case kindStoreys:
		if c.model.Storeys == nil {
			return c.result(id, title, reg, Missing, tr("The model has no storeys."))
		}
		return c.compare(id, title, reg, float64(*c.model.Storeys), "", tr("Number of storeys"), false)
	case kindBoundaryDist:
		return c.boundaryDistance(id, title, reg)
	}
	return c.result(id, title, reg, Manual, tr("Check by hand."))
}

func (c *planContext) height(id, title string, reg PlanRegulation, h *float64, what string) Result {
	if h == nil {
		return c.result(id, title, reg, Missing, trf("The model has no %s.", what))
	}
	v := *h
	if isAbsoluteHeight(reg) {
		if c.model.BaseElevation == nil {
			return c.result(id, title, reg, Missing, tr("This limit is measured above sea level (nollplanet, RH 2000), but the model has no ground elevation. Enter the site's ground level."))
		}
		v += *c.model.BaseElevation
	}
	return c.compare(id, title, reg, v, "m", capitalize(what), false)
}

func (c *planContext) buildingHeight(id, title string, reg PlanRegulation) Result {
	def := tr("the definition in PBF 1 kap. 3 § (plans adopted from 2 May 2011)")
	if d, err := time.Parse("2006-01-02", firstN(c.plan.LegallyBinding, 10)); err == nil && d.Before(pbl2011) {
		def = tr("the older ÄPBL definition (plan adopted before 2 May 2011)")
	} else if err != nil {
		def = tr("the definition in force when the plan was adopted (adoption date unknown)")
	}
	if c.model.BuildingHeight == nil {
		return c.result(id, title, reg, Manual, trf("Byggnadshöjd depends on the roof and facades and can't be computed from the model automatically. Measure it using %s.", def))
	}
	r := c.compare(id, title, reg, *c.model.BuildingHeight, "m", tr("Byggnadshöjd"), false)
	r.Message += " " + trf("Uses %s.", def)
	return r
}

func (c *planContext) areaLimit(id, title string, reg PlanRegulation, a float64, what string, approx bool) Result {
	if isPercent(reg) {
		if c.plan.Property == nil || c.plan.Property.Area == nil {
			return c.result(id, title, reg, Missing, tr("This limit is a share of the property (fastighet) area, which isn't known. Enter the property area."))
		}
		pct := 100 * a / *c.plan.Property.Area
		return c.compare(id, title, reg, pct, "%", trf("%s as share of the property", capitalize(what)), approx)
	}
	return c.compare(id, title, reg, a, "m²", capitalize(what), approx)
}

func (c *planContext) boundaryDistance(id, title string, reg PlanRegulation) Result {
	if c.plan.Property == nil || c.plan.Property.Boundary == nil {
		return c.result(id, title, reg, Missing, tr("The property (fastighet) boundary isn't known, so the distance can't be measured."))
	}
	pb, err := c.plan.Property.Boundary.polygons()
	if err != nil {
		return c.result(id, title, reg, Manual, tr("The property boundary couldn't be read. Check by hand."))
	}
	// Distance from the building to the property's boundary line, measured
	// from inside: use the boundary rings as lines.
	d := minDistanceToEdges(c.footprint, pb)
	return c.compare(id, title, reg, d, "m", tr("Distance to property boundary"), c.approx)
}

func minDistanceToEdges(a, b polyclip.Polygon) float64 {
	best := -1.0
	for _, ca := range a {
		for _, cb := range b {
			for i := range ca {
				for j := range cb {
					d := segmentDistance(ca[i], ca[(i+1)%len(ca)], cb[j], cb[(j+1)%len(cb)])
					if best < 0 || d < best {
						best = d
					}
				}
			}
		}
	}
	return best
}

// compare checks a measured value against the regulation's limit.
func (c *planContext) compare(id, title string, reg PlanRegulation, v float64, unit, what string, approx bool) Result {
	limit, ok := regValue(reg)
	if !ok {
		return c.result(id, title, reg, Manual, tr("The plan doesn't give a number for this regulation. Read it and check by hand."))
	}
	min := isMin(reg) || isDistance(reg)
	pass := v <= limit+1e-9
	word := tr("maximum")
	if min {
		pass = v >= limit-1e-9
		word = tr("minimum")
	}
	r := c.result(id, title, reg, Pass, lang.Dec(fmt.Sprintf("%s %s %s, %s %s %s.", what, fmtNum(v), unit, word, fmtNum(limit), unit)))
	r.Measured, r.Required, r.Unit, r.What = ptr(round(v, 2)), ptr(limit), unit, strings.ToLower(what)
	r.Op = "<="
	if min {
		r.Op = ">="
	}
	if !pass {
		r.Status = Fail
		too := tr("too much")
		if min {
			too = tr("too little")
		}
		r.Message = lang.Dec(fmt.Sprintf("%s %s %s, %s %s %s (%s %s %s).", what, fmtNum(v), unit, word, fmtNum(limit), unit,
			fmtNum(abs(v-limit)), unit, too))
		if approx {
			r.Status = Manual
			r.Message += " " + trf("The footprint is approximate (%s) and may overestimate, so check the real outline.", c.model.FootprintMethod)
		}
	}
	return r
}

func isDistance(r PlanRegulation) bool { return classify(r) == kindBoundaryDist }

func ptr(v float64) *float64 { return &v }

func round(v float64, d int) float64 {
	p := 1.0
	for range d {
		p *= 10
	}
	if v < 0 {
		return -float64(int64(-v*p+0.5)) / p
	}
	return float64(int64(v*p+0.5)) / p
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

func fmtNum(v float64) string {
	return strconv.FormatFloat(round(v, 2), 'f', -1, 64)
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	return strings.ToUpper(string(r[0])) + string(r[1:])
}

func firstN(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
