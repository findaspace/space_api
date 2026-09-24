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

// Nothing is mandatory any more. A host posts with photos, a price and a
// location; the attributes are a completeness score, not a gate.
func TestValidateAcceptsNothingAtAll(t *testing.T) {
	out, err := Validate(TypeApartment, ModeTerm, map[string]any{})
	if err != nil {
		t.Fatalf("an empty attribute set must be valid: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("out = %v, want empty", out)
	}
}

func TestValidateAcceptsAPartialSet(t *testing.T) {
	out, err := Validate(TypeApartment, ModeTerm, map[string]any{"bedrooms": float64(2)})
	if err != nil {
		t.Fatalf("a partial attribute set must be valid: %v", err)
	}
	if len(out) != 1 {
		t.Errorf("out = %v, want just bedrooms", out)
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

func TestValidateRejectsOutOfRange(t *testing.T) {
	attrs := validApartment()
	attrs["bedrooms"] = float64(99)

	_, err := Validate(TypeApartment, ModeTerm, attrs)
	if got := issueCodes(t, err)["bedrooms"]; got != "range" {
		t.Errorf("code = %q, want range", got)
	}
}

// The database CHECK enforces this too. Catching it here gives the host a
// field-level message instead of a 500 from a constraint violation.
func TestValidateRejectsModeNotAllowedForType(t *testing.T) {
	_, err := Validate(TypeWarehouse, ModeNightly, map[string]any{
		"floor_area_sqm": float64(200),
	})
	if got := issueCodes(t, err)["rental_mode"]; got != "not_allowed" {
		t.Errorf("code = %q, want not_allowed", got)
	}
}

func TestEverySchemaIsWellFormed(t *testing.T) {
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
			if f.Weight < 0 || f.Weight > 3 {
				t.Errorf("%s.%s: weight %d outside 0 to 3", s.Type, f.Key, f.Weight)
			}
			if f.Label == "" {
				t.Errorf("%s.%s: no label, so it cannot be prompted for", s.Type, f.Key)
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

	got := Slugify("2 Bedroom Apartment in East Legon", id)
	if !hasPrefix(got, "2-bedroom-apartment-in-east-legon-") {
		t.Errorf("slug = %q", got)
	}
	if fallback := Slugify("!!!", id); !hasPrefix(fallback, "space-") {
		t.Errorf("fallback slug = %q", fallback)
	}
}

func hasPrefix(s, p string) bool { return len(s) >= len(p) && s[:len(p)] == p }
