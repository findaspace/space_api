package geo

import (
	"math"
	"testing"
)

// metres between two points, near enough at these distances.
func metres(lat1, lng1, lat2, lng2 float64) float64 {
	const r = 6_371_000
	dLat := (lat2 - lat1) * math.Pi / 180
	dLng := (lng2 - lng1) * math.Pi / 180 * math.Cos(lat1*math.Pi/180)
	return r * math.Hypot(dLat, dLng)
}

func TestApproximateHidesTheHouse(t *testing.T) {
	// A house in Madina.
	lat, lng := 5.683612, -0.166734
	aLat, aLng := Approximate(lat, lng)

	moved := metres(lat, lng, aLat, aLng)
	if moved < 1 {
		t.Fatalf("approximate point is the exact point")
	}
	// Never further than half a cell's diagonal: still the right neighbourhood.
	if limit := metres(0, 0, ApproxGrid/2, ApproxGrid/2); moved > limit {
		t.Errorf("moved %.0fm, more than half a cell (%.0fm)", moved, limit)
	}
}

// The whole reason for a grid over random noise: the same listing gives the
// same answer every time, so repeated requests reveal nothing.
func TestApproximateIsStable(t *testing.T) {
	lat, lng := 5.683612, -0.166734
	firstLat, firstLng := Approximate(lat, lng)
	for range 1000 {
		if gotLat, gotLng := Approximate(lat, lng); gotLat != firstLat || gotLng != firstLng {
			t.Fatal("approximation changed between calls; averaging could recover the house")
		}
	}
}

// Every house in the same square shares one public point, so a public point
// cannot be traced back to a single house.
func TestNeighboursShareAPoint(t *testing.T) {
	aLat, aLng := Approximate(5.6811, -0.1681)
	bLat, bLng := Approximate(5.6849, -0.1655)
	if aLat != bLat || aLng != bLng {
		t.Errorf("two houses in one cell got different points: (%v,%v) vs (%v,%v)", aLat, aLng, bLat, bLng)
	}
}

// West of Greenwich, longitudes are negative. Floor rather than truncation
// keeps cells the same size on both sides of zero.
func TestNegativeLongitudesSnapCorrectly(t *testing.T) {
	_, lng := Approximate(5.6, -0.0001)
	if lng != -0.0025 {
		t.Errorf("lng = %v, want -0.0025, the centre of the cell just west of zero", lng)
	}
	_, lng = Approximate(5.6, 0.0001)
	if lng != 0.0025 {
		t.Errorf("lng = %v, want 0.0025", lng)
	}
}

func TestOutputHasNoFloatNoise(t *testing.T) {
	lat, _ := Approximate(5.683612, -0.166734)
	if lat != math.Round(lat*1e6)/1e6 {
		t.Errorf("lat = %v carries float noise", lat)
	}
}
