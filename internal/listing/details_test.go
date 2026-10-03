package listing

import (
	"math"
	"testing"
)

func validDetails() Space {
	return Space{Type: TypeApartment, Mode: ModeTerm, Title: "A bright apartment", Currency: "GHS", BasePriceMinor: 120000, PricePeriod: PeriodMonth, MaxOccupancy: 2, Location: Location{Country: "GH", Locality: "Madina", Latitude: 5.6, Longitude: -0.2}}
}
func TestPricingAndDetailsValidation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Space)
		valid  bool
	}{
		{"monthly", func(*Space) {}, true},
		{"hourly pitch", func(s *Space) { s.Type = TypeFootballPitch; s.Mode = ModeFlexible; s.PricePeriod = PeriodHour }, true},
		{"academic hostel", func(s *Space) { s.Type = TypeHostelBed; s.PricePeriod = PeriodAcademicYear }, true},
		{"semester hostel", func(s *Space) { s.Type = TypeHostelBed; s.PricePeriod = PeriodSemester }, true},
		{"wrong residential hour", func(s *Space) { s.PricePeriod = PeriodHour }, false},
		{"wrong sports term", func(s *Space) { s.Type = TypeSportsCourt }, false},
		{"negative deposit", func(s *Space) { s.DepositMinor = -1 }, false},
		{"blank title", func(s *Space) { s.Title = " " }, false},
		{"nonfinite coordinate", func(s *Space) { s.Location.Latitude = math.NaN() }, false},
		{"coordinate range", func(s *Space) { s.Location.Longitude = 181 }, false},
		{"bad digital address", func(s *Space) { s.Location.DigitalAddress = "wrong" }, false},
		{"valid digital address", func(s *Space) { s.Location.DigitalAddress = "GA-123-4567" }, true},
		{"nightly advance", func(s *Space) { s.Mode = ModeNightly; s.PricePeriod = PeriodNight; s.AdvanceMonths = 1 }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := validDetails()
			tc.change(&s)
			if (ValidateDetails(s) == nil) != tc.valid {
				t.Fatalf("validation = %v", ValidateDetails(s))
			}
		})
	}
}
func TestSportsSchemaRejectsUnsupportedAttributes(t *testing.T) {
	if _, err := Validate(TypeFootballPitch, ModeFlexible, map[string]any{"surface": "artificial_turf", "floodlights": true}); err != nil {
		t.Fatal(err)
	}
	if _, err := Validate(TypeFootballPitch, ModeFlexible, map[string]any{"surface": "made_up"}); err == nil {
		t.Fatal("accepted unsupported surface")
	}
}
