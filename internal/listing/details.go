package listing

import (
	"math"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

// AllowedPeriods keeps advertised units separate from dated booking calendars.
func AllowedPeriods(t SpaceType, mode RentalMode) []PricePeriod {
	switch mode {
	case ModeNightly:
		return []PricePeriod{PeriodNight, PeriodWeek}
	case ModeFlexible:
		return []PricePeriod{PeriodHour, PeriodDay, PeriodWeek}
	case ModeTerm:
		if t == TypeHostelBed {
			return []PricePeriod{PeriodMonth, PeriodYear, PeriodSemester, PeriodAcademicYear}
		}
		return []PricePeriod{PeriodMonth, PeriodYear}
	}
	return nil
}

var digitalAddressShape = regexp.MustCompile(`^[A-Z][A-Z0-9]-[0-9]{3,5}-[0-9]{3,5}$`)

// ValidateDetails returns actionable 422 issues rather than leaking database CHECK errors as 500s.
func ValidateDetails(sp Space) error {
	var issues []FieldIssue
	add := func(field, code string) { issues = append(issues, FieldIssue{Field: field, Code: code}) }
	schema, ok := SchemaFor(sp.Type)
	if !ok {
		add("space_type", "unknown")
	} else if !slices.Contains(schema.Modes, sp.Mode) {
		add("rental_mode", "not_allowed")
	}
	if !slices.Contains(AllowedPeriods(sp.Type, sp.Mode), sp.PricePeriod) {
		add("price_period", "not_allowed")
	}
	titleLen := utf8.RuneCountInString(strings.TrimSpace(sp.Title))
	if titleLen < 8 || titleLen > 120 {
		add("title", "length")
	}
	if utf8.RuneCountInString(sp.Description) > 5000 {
		add("description", "length")
	}
	if sp.BasePriceMinor <= 0 {
		add("base_price_minor", "positive")
	}
	if sp.DepositMinor < 0 {
		add("deposit_minor", "nonnegative")
	}
	if sp.AdvanceMonths < 0 || sp.AdvanceMonths > 6 || (sp.Mode != ModeTerm && sp.AdvanceMonths != 0) {
		add("advance_months", "not_allowed")
	}
	if sp.MaxOccupancy < 1 || sp.MaxOccupancy > 500 {
		add("max_occupancy", "range")
	}
	if sp.Currency != "GHS" {
		add("currency", "not_allowed")
	}
	if sp.Location.Country != "GH" {
		add("location.country", "not_allowed")
	}
	if math.IsNaN(sp.Location.Latitude) || math.IsInf(sp.Location.Latitude, 0) || sp.Location.Latitude < -90 || sp.Location.Latitude > 90 {
		add("location.latitude", "range")
	}
	if math.IsNaN(sp.Location.Longitude) || math.IsInf(sp.Location.Longitude, 0) || sp.Location.Longitude < -180 || sp.Location.Longitude > 180 {
		add("location.longitude", "range")
	}
	if len(sp.Location.Locality) > 80 {
		add("location.locality", "length")
	}
	if len(sp.Location.Region) > 80 {
		add("location.region", "length")
	}
	if len(sp.Location.Landmark) > 120 {
		add("location.landmark", "length")
	}
	if sp.Location.DigitalAddress != "" && !digitalAddressShape.MatchString(sp.Location.DigitalAddress) {
		add("location.digital_address", "invalid")
	}
	if len(issues) > 0 {
		return &ValidationError{Issues: issues}
	}
	return nil
}
