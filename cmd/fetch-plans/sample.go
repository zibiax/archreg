package main

import (
	"github.com/martin/archreg/engine/geo"
	"github.com/martin/archreg/engine/plans"
	"github.com/martin/archreg/engine/rules"
)

// samplePlans returns fictional plans in Östermalm for trying the app before
// real data is fetched. IDs start with "EXEMPEL" and names say so.
func samplePlans() []rules.Plan {
	f := func(v float64) *float64 { return &v }

	// rect builds a lon/lat rectangle of w × h metres with its south-west
	// corner offset (dx, dy) metres from the centre (lon, lat).
	rect := func(lon, lat, dx, dy, w, h float64) *rules.Geometry {
		e, n := geo.ToSweref(lon, lat)
		var ring [][2]float64
		for _, c := range [][2]float64{{dx, dy}, {dx + w, dy}, {dx + w, dy + h}, {dx, dy + h}, {dx, dy}} {
			lo, la := geo.ToWGS84(e+c[0], n+c[1])
			ring = append(ring, [2]float64{lo, la})
		}
		return plans.FromWGS84Polygon([][][2]float64{ring})
	}

	// Plan 1: new digital plan by Valhallavägen.
	lon1, lat1 := 18.0835, 59.3468
	p1 := rules.Plan{
		ID: "EXEMPEL-2023/01", Name: "Exempelplan Valhallavägen (fiktiv)", Status: "laga kraft", LegallyBinding: "2023-06-15",
		Boundary: rect(lon1, lat1, -90, -60, 180, 120),
		Regulations: []rules.PlanRegulation{
			{ID: "ex1-b1", Label: "B", Category: "Användning av kvartersmark", Text: "Bostäder", UseForm: "Kvartersmark"},
			{ID: "ex1-pm", Label: "prickmark", Category: "Utformning", Subcategory: "Begränsning av markens utnyttjande",
				Text: "Marken får inte förses med byggnad.", Geometry: rect(lon1, lat1, -90, -60, 180, 10)},
			{ID: "ex1-km", Label: "korsmark", Category: "Utformning", Subcategory: "Begränsning av markens utnyttjande",
				Text: "Marken får endast förses med komplementbyggnad.", Geometry: rect(lon1, lat1, 60, 30, 30, 30)},
			{ID: "ex1-e1", Label: "e1", Category: "Utformning", Subcategory: "Utnyttjandegrad",
				Text: "Största byggnadsarea är 1200 m².", Value: f(1200), ValueType: "max", Unit: "m2"},
			{ID: "ex1-h1", Label: "h1", Category: "Utformning", Subcategory: "Höjd på byggnadsverk",
				Text: "Högsta nockhöjd är 16 meter.", Value: f(16), ValueType: "max", Unit: "meter"},
			{ID: "ex1-v1", Label: "v1", Category: "Utformning", Subcategory: "Höjd på byggnadsverk",
				Text: "Högsta antal våningar är 4.", Value: f(4), ValueType: "max"},
			{ID: "ex1-f1", Label: "f1", Category: "Utformning", Subcategory: "Utformning",
				Text: "Fasader ska utföras i tegel och anpassas till omgivande bebyggelse."},
		},
	}

	// Plan 2: older plan (byggnadshöjd, percentage of the property).
	lon2, lat2 := 18.0745, 59.3445
	p2 := rules.Plan{
		ID: "EXEMPEL-1985/07", Name: "Exempelplan Östermalm (fiktiv, äldre plan)", Status: "laga kraft", LegallyBinding: "1985-03-01",
		Boundary: rect(lon2, lat2, -110, -80, 220, 160),
		Regulations: []rules.PlanRegulation{
			{ID: "ex2-b", Label: "BK", Text: "Bostäder och kontor"},
			{ID: "ex2-bh", Label: "", Text: "Högsta byggnadshöjd är 18,5 meter.", Value: f(18.5), Unit: "meter"},
			{ID: "ex2-e", Label: "e", Text: "Största byggnadsarea är 40 % av fastighetsarean.", Value: f(40), Unit: "%"},
			{ID: "ex2-pm", Label: "prickmark", Text: "Marken får inte förses med byggnad.", Geometry: rect(lon2, lat2, -110, 70, 220, 10)},
		},
	}

	// Plan 3: scanned plan, can't be checked.
	p3 := rules.Plan{
		ID: "EXEMPEL-1962/12", Name: "Exempelplan Gärdet (fiktiv, endast PDF)", Status: "laga kraft", LegallyBinding: "1962-11-30",
		PDFOnly: true, URL: "https://www.boverket.se/sv/PBL-kunskapsbanken/planering/detaljplan/",
		Boundary: rect(18.0930, 59.3445, -100, -70, 200, 140),
	}
	return []rules.Plan{p1, p2, p3}
}
