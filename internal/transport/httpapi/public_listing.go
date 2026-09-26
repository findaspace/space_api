package httpapi

import (
	"time"

	"github.com/samnart1/space_api/internal/geo"
	"github.com/samnart1/space_api/internal/listing"
)

// publicLocation is what a visitor sees before booking: the neighbourhood,
// never the house.
//
// The digital address is left out entirely. A GhanaPost GPS code pins a
// location to a few metres, so publishing it is publishing the host's front
// door. It is also exactly what lets someone skip the platform and settle with
// the landlord directly, the side deal that message redaction exists to stop.
type publicLocation struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	Locality  string  `json:"locality"`
	Region    string  `json:"region"`
	Country   string  `json:"country"`
	Landmark  string  `json:"landmark,omitempty"`

	// Approximate is always true here. It is in the payload so a client can
	// draw a circle rather than a pin, and never mistake this for the address.
	Approximate bool `json:"approximate"`
}

// publicSpaceResponse is the listing as any visitor may see it. It is a
// separate type from the host's view on purpose: adding a field to what a host
// sees must never silently publish it.
//
// The listing's own id stays: booking and messaging are addressed by it, and
// it identifies a listing, not a person.
//
// Left out, deliberately: the host's user id, which is a stable handle for
// tracking one person across all their listings; the exact coordinates and
// digital address; the moderation status, which is always published here; and
// the internal timestamps.
type publicSpaceResponse struct {
	ID             string              `json:"id"`
	Slug           string              `json:"slug"`
	SpaceType      listing.SpaceType   `json:"space_type"`
	RentalMode     listing.RentalMode  `json:"rental_mode"`
	Title          string              `json:"title"`
	Description    string              `json:"description"`
	BasePriceMinor int64               `json:"base_price_minor"`
	Currency       string              `json:"currency"`
	PricePeriod    listing.PricePeriod `json:"price_period"`
	DepositMinor   int64               `json:"deposit_minor"`
	AdvanceMonths  int16               `json:"advance_months"`
	MaxOccupancy   int16               `json:"max_occupancy"`
	Location       publicLocation      `json:"location"`
	Amenities      []string            `json:"amenities"`
	Attributes     map[string]any      `json:"attributes"`
	PublishedAt    *time.Time          `json:"published_at"`
}

func toPublicSpaceResponse(s listing.Space) publicSpaceResponse {
	lat, lng := geo.Approximate(s.Location.Latitude, s.Location.Longitude)

	amenities := s.Amenities
	if amenities == nil {
		amenities = []string{}
	}
	attributes := s.Attributes
	if attributes == nil {
		attributes = map[string]any{}
	}

	return publicSpaceResponse{
		ID: s.ID.String(), Slug: s.Slug, SpaceType: s.Type, RentalMode: s.Mode,
		Title: s.Title, Description: s.Description,
		BasePriceMinor: s.BasePriceMinor, Currency: s.Currency,
		PricePeriod: s.PricePeriod, DepositMinor: s.DepositMinor,
		AdvanceMonths: s.AdvanceMonths, MaxOccupancy: s.MaxOccupancy,
		Location: publicLocation{
			Latitude: lat, Longitude: lng,
			Locality: s.Location.Locality, Region: s.Location.Region,
			Country: s.Location.Country, Landmark: s.Location.Landmark,
			Approximate: true,
		},
		Amenities:   amenities,
		Attributes:  attributes,
		PublishedAt: s.PublishedAt,
	}
}
