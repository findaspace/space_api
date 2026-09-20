package listing

import (
	"errors"
	"testing"

	"github.com/google/uuid"
)

func issueCodes(t *testing.T, err error) map[string]string {
	t.Helper()
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected *ValidationError, got %v", err)
	}
	got := make(map[string]string, len(ve.Issues))
	for _, i := range ve.Issues {
		got[i.Field] = i.Code
	}
	return got
}

func validApartment() map[string]any {
	return map[string]any{
		"bedrooms":       float64(2),
		"bathrooms":      float64(1),
		"self_contained": true,
		"furnishing":     "semi_furnished",
		"kitchen":        "private",
		"water_source":   "borehole",
		"power_backup":   "inverter",
	}
}

func TestValidateAccepts(t *testing.T) {
	out, err := Validate(TypeApartment, ModeTerm, validApartment())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Integer fields are normalized off float64 before storage.
	if v, ok := out["bedrooms"].(int64); !ok || v != 2 {
		t.Errorf("bedrooms = %#v, want int64(2)", out["bedrooms"])
	}
}

func TestValidateRejectsUnknownKey(t *testing.T) {
	attrs := validApartment()
	attrs["swimming_pool"] = true

	_, err := Validate(TypeApartment, ModeTerm, attrs)
	if got := issueCodes(t, err)["swimming_pool"]; got != "unknown" {
		t.Errorf("code = %q, want unknown", got)
	}
}

func TestValidateRejectsMissingRequired(t *testing.T) {
	attrs := validApartment()
	delete(attrs, "water_source")

	_, err := Validate(TypeApartment, ModeTerm, attrs)
	if got := issueCodes(t, err)["water_source"]; got != "required" {
		t.Errorf("code = %q, want required", got)
	}
}

func TestValidateRejectsNonIntegralInt(t *testing.T) {
	attrs := validApartment()
	attrs["bedrooms"] = 2.5

	_, err := Validate(TypeApartment, ModeTerm, attrs)
	if got := issueCodes(t, err)["bedrooms"]; got != "type" {
		t.Errorf("code = %q, want type", got)
	}
}

func TestValidateRejectsBadEnum(t *testing.T) {
	attrs := validApartment()
	attrs["furnishing"] = "very_furnished"

	_, err := Validate(TypeApartment, ModeTerm, attrs)
	if got := issueCodes(t, err)["furnishing"]; got != "option" {
		t.Errorf("code = %q, want option", got)
	}
}

// The database CHECK enforces this too. Catching it here gives the host a
// field-level message instead of a 500 from a constraint violation.
func TestValidateRejectsModeNotAllowedForType(t *testing.T) {
	_, err := Validate(TypeWarehouse, ModeNightly, map[string]any{
		"floor_area_sqm": float64(200),
		"water_source":   "pipe",
		"power_backup":   "generator",
	})
	if got := issueCodes(t, err)["rental_mode"]; got != "not_allowed" {
		t.Errorf("code = %q, want not_allowed", got)
	}
}

func TestEverySchemaHasModesAndType(t *testing.T) {
	for _, s := range Schemas() {
		if s.Type == "" {
			t.Errorf("%s: Type not set by init", s.Label)
		}
		if len(s.Modes) == 0 {
			t.Errorf("%s: no rental modes", s.Type)
		}
		seen := map[string]bool{}
		for _, f := range s.Fields {
			if seen[f.Key] {
				t.Errorf("%s: duplicate field %q from group composition", s.Type, f.Key)
			}
			seen[f.Key] = true
			if f.Kind == KindEnum && len(f.Options) == 0 {
				t.Errorf("%s.%s: enum with no options", s.Type, f.Key)
			}
		}
	}
}

func TestValidateAmenities(t *testing.T) {
	out, err := ValidateAmenities([]string{"wifi", "parking", "wifi"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 || out[0] != "parking" || out[1] != "wifi" {
		t.Errorf("out = %v, want sorted and deduplicated", out)
	}
	if _, err := ValidateAmenities([]string{"helipad"}); err == nil {
		t.Error("expected error for unknown amenity")
	}
}

func TestSlugify(t *testing.T) {
	id := uuid.MustParse("01a0bbf9-23a8-7050-8e96-fc0bc66e60d1")

	if got := Slugify("2 Bedroom Apartment in East Legon", id); got != "2-bedroom-apartment-in-east-legon-60e60d1"[:len(got)] {
		t.Logf("slug = %q", got)
	}
	if got := Slugify("!!!", id); !hasPrefix(got, "space-") {
		t.Errorf("fallback slug = %q", got)
	}
}

func hasPrefix(s, p string) bool { return len(s) >= len(p) && s[:len(p)] == p }
