package rules

import "fmt"

// Describe returns the regulation's kind ("prickmark", "nockhojd", ... or "")
// and a short plain-language English explanation for the UI.
func Describe(r PlanRegulation) (kind, plain string) {
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
		abs = " above sea level (RH 2000)"
	}
	switch k {
	case kindPrickmark:
		plain = "No buildings allowed on this land (prickmark)."
	case kindKorsmark:
		plain = "Only small outbuildings allowed here (korsmark)."
	case kindRidgeHeight:
		plain = "Max ridge height (nockhöjd)" + num("m") + abs + "."
	case kindTotalHeight:
		plain = "Max total height (totalhöjd)" + num("m") + abs + "."
	case kindBuildingHeight:
		plain = "Max building height (byggnadshöjd)" + num("m") + abs + "."
	case kindBuildingArea:
		if isPercent(r) {
			plain = "Max building footprint (byggnadsarea)" + num("%") + " of the property."
		} else {
			plain = "Max building footprint (byggnadsarea)" + num("m²") + "."
		}
	case kindFloorArea:
		plain = "Max gross floor area (bruttoarea)" + num("m²") + "."
	case kindStoreys:
		if hasV {
			plain = fmt.Sprintf("Max number of storeys: %s.", fmtNum(v))
		} else {
			plain = "Limits the number of storeys."
		}
	case kindBoundaryDist:
		plain = "Minimum distance to the property boundary" + num("m") + "."
	default:
		if isUse(r) {
			plain = "Allowed use: " + r.Text + "."
		} else {
			plain = "Needs to be read and judged by hand."
		}
	}
	return string(k), plain
}
