package httpapi

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/samnart1/space_api/internal/listing"
)

func madinaListing() listing.Space {
	return listing.Space{
		ID: uuid.New(), HostID: uuid.New(), Slug: "chamber-and-hall-in-madina-04a845",
		Type: "chamber_and_hall", Mode: "term", Status: "published",
		Title: "Chamber and hall in Madina", BasePriceMinor: 130000, Currency: "GHS",
		PricePeriod: "month", AdvanceMonths: 12, MaxOccupancy: 3,
		Location: listing.Location{
			Latitude: 5.683612, Longitude: -0.166734,
			Locality: "Madina", Region: "Greater Accra", Country: "GH",
			Landmark: "Near Madina Market", DigitalAddress: "GA-183-8164",
		},
	}
}

// The test reads the JSON a visitor actually receives, not the Go struct, so
// a field added later with the wrong tag is still caught.
func TestPublicListingHidesTheAddressAndTheHost(t *testing.T) {
	sp := madinaListing()
	raw, err := json.Marshal(toPublicSpaceResponse(sp))
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)

	for _, leak := range []string{
		"GA-183-8164",      // the digital address itself
		"digital_address",  // or even the key
		sp.HostID.String(), // the host's user id
		"host_id",
		"5.683612", "-0.166734", // the exact point
	} {
		if strings.Contains(body, leak) {
			t.Errorf("public listing contains %q", leak)
		}
	}
}

func TestPublicListingKeepsWhatAVisitorNeeds(t *testing.T) {
	var got map[string]any
	raw, _ := json.Marshal(toPublicSpaceResponse(madinaListing()))
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}

	loc := got["location"].(map[string]any)
	if loc["locality"] != "Madina" || loc["landmark"] != "Near Madina Market" {
		t.Errorf("location = %v, want the neighbourhood and landmark kept", loc)
	}
	if loc["approximate"] != true {
		t.Error("approximate flag missing; a client could draw it as an exact pin")
	}
	// The listing id is kept: booking and messaging are addressed by it.
	if got["id"] == nil || got["id"] == "" {
		t.Error("listing id missing; the web app could not book or message")
	}
	if got["base_price_minor"].(float64) != 130000 {
		t.Errorf("price = %v", got["base_price_minor"])
	}
}

// Empty collections serialise as [] and {}, never null, so the web client
// never has to guard against null where it expects a list.
func TestEmptyCollectionsAreNotNull(t *testing.T) {
	sp := madinaListing()
	sp.Amenities, sp.Attributes = nil, nil
	raw, _ := json.Marshal(toPublicSpaceResponse(sp))
	if strings.Contains(string(raw), "null") && !strings.Contains(string(raw), `"published_at":null`) {
		t.Errorf("nil collection serialised as null: %s", raw)
	}
}
