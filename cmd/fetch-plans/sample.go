package main

import (
	"math"

	"github.com/martin/archreg/engine/geo"
	"github.com/martin/archreg/engine/plans"
	"github.com/martin/archreg/engine/rules"
)

// samplePlans returns fictional plans around Stockholm for trying the app
// before real data is fetched. IDs start with "EXEMPEL" and names say so.
// Together they cover every kind of plan regulation the engine checks.
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

	// box is rect in a grid rotated rot degrees anticlockwise about the
	// centre, so plans can follow the street grid.
	box := func(lon, lat, rot, dx, dy, w, h float64) *rules.Geometry {
		e, n := geo.ToSweref(lon, lat)
		s, c := math.Sincos(rot * math.Pi / 180)
		var ring [][2]float64
		for _, p := range [][2]float64{{dx, dy}, {dx + w, dy}, {dx + w, dy + h}, {dx, dy + h}, {dx, dy}} {
			lo, la := geo.ToWGS84(e+p[0]*c-p[1]*s, n+p[0]*s+p[1]*c)
			ring = append(ring, [2]float64{lo, la})
		}
		return plans.FromWGS84Polygon([][][2]float64{ring})
	}
	use := func(id, label, text string) rules.PlanRegulation {
		return rules.PlanRegulation{ID: id, Label: label, Category: "Användning av kvartersmark", Text: text, UseForm: "Kvartersmark"}
	}
	limit := func(id, label, sub, text string) rules.PlanRegulation {
		return rules.PlanRegulation{ID: id, Label: label, Category: "Utformning", Subcategory: sub, Text: text}
	}
	area := func(id, label, text string, g *rules.Geometry) rules.PlanRegulation {
		return rules.PlanRegulation{ID: id, Label: label, Category: "Utformning", Subcategory: "Begränsning av markens utnyttjande", Text: text, Geometry: g}
	}
	const noBuild, outbuildingsOnly = "Marken får inte förses med byggnad.", "Marken får endast förses med komplementbyggnad."

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

	// Plan 4: Södermalm city block with a courtyard, mixed use, storeys.
	lo, la, r := 18.0585, 59.3172, 8.0
	p4 := rules.Plan{
		ID: "EXEMPEL-2019/14", Name: "Exempelplan Hornsgatan, Södermalm (fiktiv)", Status: "laga kraft", LegallyBinding: "2019-09-12",
		Boundary: box(lo, la, r, -80, -45, 160, 90),
		Regulations: []rules.PlanRegulation{
			use("ex4-b", "B", "Bostäder"),
			use("ex4-c", "C", "Centrum"),
			area("ex4-pm", "prickmark", noBuild, box(lo, la, r, -80, -45, 160, 6)),
			area("ex4-km", "korsmark", outbuildingsOnly, box(lo, la, r, -30, -5, 60, 30)),
			limit("ex4-h", "h1", "Höjd på byggnadsverk", "Högsta nockhöjd är 24 meter."),
			limit("ex4-v", "v1", "Höjd på byggnadsverk", "Högsta antal våningar är 7."),
			limit("ex4-e", "e1", "Utnyttjandegrad", "Största byggnadsarea är 4000 m²."),
			limit("ex4-f", "f1", "Utformning", "Bottenvåning mot gata ska ha en våningshöjd om minst 4 meter och användas för centrumändamål."),
		},
	}

	// Plan 5: Kungsholmen, height in metres above sea level (RH 2000).
	lo, la, r = 18.0335, 59.3325, -12
	p5 := rules.Plan{
		ID: "EXEMPEL-2016/03", Name: "Exempelplan Fridhemsplan, Kungsholmen (fiktiv)", Status: "laga kraft", LegallyBinding: "2016-04-21",
		Boundary: box(lo, la, r, -70, -55, 140, 110),
		Regulations: []rules.PlanRegulation{
			use("ex5-b", "B", "Bostäder"),
			use("ex5-c", "C", "Centrum"),
			area("ex5-pm", "prickmark", noBuild, box(lo, la, r, -70, 47, 140, 8)),
			limit("ex5-t", "", "Höjd på byggnadsverk", "Högsta totalhöjd är +52,0 meter över nollplanet."),
			limit("ex5-ba", "e1", "Utnyttjandegrad", "Största bruttoarea är 12000 m²."),
		},
	}

	// Plan 6: Hammarby sjöstad by the water, floor-area limit.
	lo, la, r = 18.1035, 59.3035, 20
	p6 := rules.Plan{
		ID: "EXEMPEL-2021/08", Name: "Exempelplan Hammarby sjöstad (fiktiv)", Status: "laga kraft", LegallyBinding: "2021-11-04",
		Boundary: box(lo, la, r, -75, -50, 150, 100),
		Regulations: []rules.PlanRegulation{
			use("ex6-b", "B", "Bostäder"),
			area("ex6-pm", "prickmark", noBuild+" Kajpromenad.", box(lo, la, r, -75, -50, 150, 12)),
			area("ex6-km", "korsmark", outbuildingsOnly, box(lo, la, r, -25, 0, 50, 25)),
			limit("ex6-h", "h1", "Höjd på byggnadsverk", "Högsta nockhöjd är 28 meter."),
			limit("ex6-v", "v1", "Höjd på byggnadsverk", "Högsta antal våningar är 8."),
			limit("ex6-ba", "e1", "Utnyttjandegrad", "Största bruttoarea är 9000 m²."),
		},
	}

	// Plan 7: Vasastan, adopted under the old PBL (byggnadshöjd, percent).
	lo, la, r = 18.0485, 59.3430, -5
	p7 := rules.Plan{
		ID: "EXEMPEL-1978/22", Name: "Exempelplan Odenplan, Vasastan (fiktiv, äldre plan)", Status: "laga kraft", LegallyBinding: "1978-05-30",
		Boundary: box(lo, la, r, -60, -45, 120, 90),
		Regulations: []rules.PlanRegulation{
			{ID: "ex7-bc", Label: "BC", Text: "Bostäder och centrum"},
			{ID: "ex7-bh", Text: "Högsta byggnadshöjd är 22,0 meter."},
			{ID: "ex7-e", Label: "e", Text: "Största byggnadsarea är 60 % av fastighetsarean."},
			{ID: "ex7-pm", Label: "prickmark", Text: noBuild, Geometry: box(lo, la, r, -60, -45, 120, 8)},
		},
	}

	// Plan 8: Liljeholmen office plan, total height and boundary distance.
	lo, la, r = 18.0230, 59.3105, 30
	p8 := rules.Plan{
		ID: "EXEMPEL-2014/05", Name: "Exempelplan Liljeholmen (fiktiv)", Status: "laga kraft", LegallyBinding: "2014-02-13",
		Boundary: box(lo, la, r, -65, -40, 130, 80),
		Regulations: []rules.PlanRegulation{
			use("ex8-k", "K", "Kontor"),
			area("ex8-pm", "prickmark", noBuild, box(lo, la, r, -65, -40, 10, 80)),
			limit("ex8-t", "", "Höjd på byggnadsverk", "Högsta totalhöjd är 40 meter."),
			limit("ex8-a", "", "Placering", "Byggnad ska placeras minst 4 meter från fastighetsgräns."),
		},
	}

	// Plan 9: Bromma villa area, small houses.
	lo, la, r = 17.9620, 59.3440, 0
	p9 := rules.Plan{
		ID: "EXEMPEL-1999/31", Name: "Exempelplan Ulvsunda villaområde, Bromma (fiktiv)", Status: "laga kraft", LegallyBinding: "1999-06-17",
		Boundary: box(lo, la, r, -150, -100, 300, 200),
		Regulations: []rules.PlanRegulation{
			{ID: "ex9-b", Label: "BF", Text: "Bostäder, friliggande hus"},
			{ID: "ex9-e", Label: "e1", Text: "Största byggnadsarea är 160 m² per fastighet."},
			{ID: "ex9-bh", Text: "Högsta byggnadshöjd är 4,5 meter."},
			{ID: "ex9-a", Text: "Huvudbyggnad ska placeras minst 4,5 meter från fastighetsgräns."},
			{ID: "ex9-pm", Label: "prickmark", Text: noBuild, Geometry: box(lo, la, r, -150, -100, 300, 6)},
		},
	}

	// Plan 10: Gamla stan, a scanned plan.
	p10 := rules.Plan{
		ID: "EXEMPEL-1948/02", Name: "Exempelplan Gamla stan (fiktiv, endast PDF)", Status: "laga kraft", LegallyBinding: "1948-10-08",
		PDFOnly: true, URL: "https://www.boverket.se/sv/PBL-kunskapsbanken/planering/detaljplan/",
		Boundary: box(18.0712, 59.3248, -15, -70, -50, 140, 100),
	}

	return []rules.Plan{p1, p2, p3, p4, p5, p6, p7, p8, p9, p10}
}
