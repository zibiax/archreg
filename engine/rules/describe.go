package rules

import (
	"fmt"

	"github.com/martin/archreg/engine/i18n"
)

// Describe returns the regulation's kind ("prickmark", "nockhojd", ... or "")
// and a short plain-language English explanation for the UI.
func Describe(r PlanRegulation, l i18n.Lang) (kind, plain string) {
	k := classify(r)
	v, hasV := regValue(r)
	num := func(unit string) string {
		if !hasV {
			return ""
		}
		return fmt.Sprintf(" %s %s", fmtNum(v), unit)
	}
	abs := ""
	if isAbsoluteHeight(r) {
		abs = " " + l.T("above sea level (RH 2000)")
	}
	switch k {
	case kindPrickmark:
		plain = l.T("No buildings allowed on this land (prickmark).")
	case kindKorsmark:
		plain = l.T("Only small outbuildings allowed here (korsmark).")
	case kindRidgeHeight:
		plain = l.T("Max ridge height (nockhöjd)") + num("m") + abs + "."
	case kindTotalHeight:
		plain = l.T("Max total height (totalhöjd)") + num("m") + abs + "."
	case kindBuildingHeight:
		plain = l.T("Max building height (byggnadshöjd)") + num("m") + abs + "."
	case kindBuildingArea:
		if isPercent(r) {
			plain = l.T("Max building footprint (byggnadsarea)") + num("%") + " " + l.T("of the property") + "."
		} else {
			plain = l.T("Max building footprint (byggnadsarea)") + num("m²") + "."
		}
	case kindFloorArea:
		plain = l.T("Max gross floor area (bruttoarea)") + num("m²") + "."
	case kindStoreys:
		if hasV {
			plain = l.F("Max number of storeys: %s.", fmtNum(v))
		} else {
			plain = l.T("Limits the number of storeys.")
		}
	case kindBoundaryDist:
		plain = l.T("Minimum distance to the property boundary") + num("m") + "."
	default:
		if isUse(r) {
			plain = l.F("Allowed use: %s.", r.Text)
		} else {
			plain = l.T("Needs to be read and judged by hand.")
		}
	}
	return string(k), l.Dec(plain)
}

// Short is a few words for compact lists: "Ridge ≤ 16 m", "No building".
func Short(r PlanRegulation, l i18n.Lang) string { return l.Dec(short(r, l)) }

func short(r PlanRegulation, l i18n.Lang) string {
	v, hasV := regValue(r)
	val := func(op, unit string) string {
		if !hasV {
			return ""
		}
		s := " " + op + " " + fmtNum(v)
		if unit != "" {
			s += " " + unit
		}
		return s
	}
	sea := ""
	if isAbsoluteHeight(r) {
		sea = " " + l.T("above sea level")
	}
	switch classify(r) {
	case kindPrickmark:
		return l.T("No building (prickmark)")
	case kindKorsmark:
		return l.T("Outbuildings only (korsmark)")
	case kindRidgeHeight:
		return l.T("Ridge height") + val("≤", "m") + sea
	case kindTotalHeight:
		return l.T("Total height") + val("≤", "m") + sea
	case kindBuildingHeight:
		return l.T("Building height") + val("≤", "m") + sea
	case kindBuildingArea:
		if isPercent(r) {
			return l.T("Footprint") + val("≤", l.T("% of plot"))
		}
		return l.T("Footprint") + val("≤", "m²")
	case kindFloorArea:
		return l.T("Floor area") + val("≤", "m²")
	case kindStoreys:
		return l.T("Storeys") + val("≤", "")
	case kindBoundaryDist:
		return l.T("To boundary") + val("≥", "m")
	}
	if isUse(r) {
		return l.F("Use: %s", r.Text)
	}
	t := []rune(r.Text)
	if len(t) > 48 {
		return string(t[:46]) + "…"
	}
	return r.Text
}
