// Package geo has the small amount of geodesy and 2D geometry the app needs:
// WGS84 ⇄ SWEREF 99 TM, convex hulls, point-in-polygon.
package geo

import "math"

// SWEREF 99 TM (EPSG:3006): transverse Mercator on GRS 80, central meridian
// 15°E, scale 0.9996, false easting 500 000 m. Formulas from Lantmäteriet's
// "Gauss Krüger projection" description; accurate to well below a millimetre
// within Sweden.
const (
	axis       = 6378137.0
	flattening = 1.0 / 298.257222101
	lon0       = 15.0
	k0         = 0.9996
	fn         = 0.0
	fe         = 500000.0
)

var (
	e2   = flattening * (2 - flattening)
	n    = flattening / (2 - flattening)
	aHat = axis / (1 + n) * (1 + n*n/4 + n*n*n*n/64)
)

func rad(d float64) float64 { return d * math.Pi / 180 }
func deg(r float64) float64 { return r * 180 / math.Pi }

// ToSweref converts WGS84 longitude/latitude (degrees) to SWEREF 99 TM
// easting/northing (metres).
func ToSweref(lon, lat float64) (e, nn float64) {
	A := e2
	B := (5*e2*e2 - e2*e2*e2) / 6
	C := (104*e2*e2*e2 - 45*e2*e2*e2*e2) / 120
	D := (1237 * e2 * e2 * e2 * e2) / 1260
	b1 := n/2 - 2*n*n/3 + 5*n*n*n/16 + 41*n*n*n*n/180
	b2 := 13*n*n/48 - 3*n*n*n/5 + 557*n*n*n*n/1440
	b3 := 61*n*n*n/240 - 103*n*n*n*n/140
	b4 := 49561 * n * n * n * n / 161280

	phi := rad(lat)
	lambda := rad(lon)
	s := math.Sin(phi)
	phiStar := phi - s*math.Cos(phi)*(A+B*s*s+C*s*s*s*s+D*s*s*s*s*s*s)
	dl := lambda - rad(lon0)
	xi := math.Atan(math.Tan(phiStar) / math.Cos(dl))
	eta := math.Atanh(math.Cos(phiStar) * math.Sin(dl))

	x := k0*aHat*(xi+b1*math.Sin(2*xi)*math.Cosh(2*eta)+b2*math.Sin(4*xi)*math.Cosh(4*eta)+
		b3*math.Sin(6*xi)*math.Cosh(6*eta)+b4*math.Sin(8*xi)*math.Cosh(8*eta)) + fn
	y := k0*aHat*(eta+b1*math.Cos(2*xi)*math.Sinh(2*eta)+b2*math.Cos(4*xi)*math.Sinh(4*eta)+
		b3*math.Cos(6*xi)*math.Sinh(6*eta)+b4*math.Cos(8*xi)*math.Sinh(8*eta)) + fe
	return y, x
}

// ToWGS84 converts SWEREF 99 TM easting/northing to longitude/latitude.
func ToWGS84(e, nn float64) (lon, lat float64) {
	d1 := n/2 - 2*n*n/3 + 37*n*n*n/96 - n*n*n*n/360
	d2 := n*n/48 + n*n*n/15 - 437*n*n*n*n/1440
	d3 := 17*n*n*n/480 - 37*n*n*n*n/840
	d4 := 4397 * n * n * n * n / 161280
	As := e2 + e2*e2 + e2*e2*e2 + e2*e2*e2*e2
	Bs := -(7*e2*e2 + 17*e2*e2*e2 + 30*e2*e2*e2*e2) / 6
	Cs := (224*e2*e2*e2 + 889*e2*e2*e2*e2) / 120
	Ds := -(4279 * e2 * e2 * e2 * e2) / 1260

	xi := (nn - fn) / (k0 * aHat)
	eta := (e - fe) / (k0 * aHat)
	xiP := xi - d1*math.Sin(2*xi)*math.Cosh(2*eta) - d2*math.Sin(4*xi)*math.Cosh(4*eta) -
		d3*math.Sin(6*xi)*math.Cosh(6*eta) - d4*math.Sin(8*xi)*math.Cosh(8*eta)
	etaP := eta - d1*math.Cos(2*xi)*math.Sinh(2*eta) - d2*math.Cos(4*xi)*math.Sinh(4*eta) -
		d3*math.Cos(6*xi)*math.Sinh(6*eta) - d4*math.Cos(8*xi)*math.Sinh(8*eta)
	phiStar := math.Asin(math.Sin(xiP) / math.Cosh(etaP))
	dl := math.Atan(math.Sinh(etaP) / math.Cos(xiP))
	s := math.Sin(phiStar)
	phi := phiStar + s*math.Cos(phiStar)*(As+Bs*s*s+Cs*s*s*s*s+Ds*s*s*s*s*s*s)
	return lon0 + deg(dl), deg(phi)
}
