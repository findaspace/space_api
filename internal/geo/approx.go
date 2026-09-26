package geo

import "math"

// ApproxGrid is the size of the square a public location is snapped to, in
// degrees. 0.005 degrees is about 550 metres north to south in Ghana: enough
// to show which neighbourhood a listing is in, too coarse to find the house.
const ApproxGrid = 0.005

// Approximate snaps a coordinate to the centre of its grid square.
//
// This is what a public listing and a search result show until someone has a
// confirmed booking. The exact point stays server-side, where walk times and
// distances are still computed from it.
//
// A fixed grid rather than random noise, deliberately. Noise that changes on
// each request can be averaged away: fetch the listing a few hundred times and
// the mean converges on the real house. A grid gives the same answer every
// time, so there is nothing to average.
func Approximate(lat, lng float64) (float64, float64) {
	return snap(lat), snap(lng)
}

func snap(v float64) float64 {
	cell := math.Floor(v / ApproxGrid)
	centre := (cell + 0.5) * ApproxGrid
	// Six decimals is well past the grid's precision, and keeps the output
	// free of float noise like 5.682499999999999.
	return math.Round(centre*1e6) / 1e6
}
