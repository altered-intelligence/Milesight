// Package geo has the small amount of geodesy Milesight needs (ADR-011: plain
// lat/lon columns, no PostGIS).
package geo

import (
	"math"

	"github.com/altered-intelligence/milesight/internal/models"
)

const earthRadiusM = 6371008.8

// DistanceM returns the great-circle (haversine) distance in meters.
func DistanceM(lat1, lon1, lat2, lon2 float64) float64 {
	φ1, φ2 := rad(lat1), rad(lat2)
	dφ, dλ := rad(lat2-lat1), rad(lon2-lon1)
	a := math.Sin(dφ/2)*math.Sin(dφ/2) + math.Cos(φ1)*math.Cos(φ2)*math.Sin(dλ/2)*math.Sin(dλ/2)
	return 2 * earthRadiusM * math.Asin(math.Min(1, math.Sqrt(a)))
}

// BearingDeg returns the initial bearing from point 1 to point 2 in [0, 360).
func BearingDeg(lat1, lon1, lat2, lon2 float64) float64 {
	φ1, φ2 := rad(lat1), rad(lat2)
	dλ := rad(lon2 - lon1)
	y := math.Sin(dλ) * math.Cos(φ2)
	x := math.Cos(φ1)*math.Sin(φ2) - math.Sin(φ1)*math.Cos(φ2)*math.Cos(dλ)
	return math.Mod(deg(math.Atan2(y, x))+360, 360)
}

// Offset moves a point by distance meters along bearing degrees.
func Offset(lat, lon, bearingDeg, distanceM float64) (float64, float64) {
	δ := distanceM / earthRadiusM
	θ := rad(bearingDeg)
	φ1, λ1 := rad(lat), rad(lon)
	φ2 := math.Asin(math.Sin(φ1)*math.Cos(δ) + math.Cos(φ1)*math.Sin(δ)*math.Cos(θ))
	λ2 := λ1 + math.Atan2(math.Sin(θ)*math.Sin(δ)*math.Cos(φ1), math.Cos(δ)-math.Sin(φ1)*math.Sin(φ2))
	return deg(φ2), math.Mod(deg(λ2)+540, 360) - 180
}

// Match returns the smallest geofence containing the point, or nil.
func Match(fences []models.Geofence, lat, lon float64) *models.Geofence {
	var best *models.Geofence
	for i := range fences {
		f := &fences[i]
		if DistanceM(lat, lon, f.Lat, f.Lon) <= f.RadiusM {
			if best == nil || f.RadiusM < best.RadiusM {
				best = f
			}
		}
	}
	return best
}

// Unit conversions (Tesla reports imperial; Milesight stores SI — ADR-009).
const kmPerMile = 1.609344

// MilesToKm converts miles to kilometers.
func MilesToKm(mi float64) float64 { return mi * kmPerMile }

// KmToMiles converts kilometers to miles.
func KmToMiles(km float64) float64 { return km / kmPerMile }

// Round rounds to n decimal places.
func Round(v float64, n int) float64 {
	p := math.Pow(10, float64(n))
	return math.Round(v*p) / p
}

func rad(d float64) float64 { return d * math.Pi / 180 }
func deg(r float64) float64 { return r * 180 / math.Pi }
