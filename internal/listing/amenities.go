package listing

import "slices"

// Amenities are a closed vocabulary. They live in a text[] with a GIN index
// rather than a join table: they never carry attributes of their own and are
// never joined to anything, so a table would buy nothing but a join.
var amenityVocabulary = []string{
	"air_conditioning", "ceiling_fan", "wifi", "dstv", "hot_water",
	"washing_machine", "gated_community", "security_guard", "cctv",
	"parking", "elevator", "balcony", "garden", "swimming_pool",
	"prepaid_meter", "water_storage_tank", "wheelchair_access",
	"furnished_kitchen", "generator_shared", "street_light",
}

func ValidateAmenities(in []string) ([]string, error) {
	var issues []FieldIssue
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))

	for _, a := range in {
		if !slices.Contains(amenityVocabulary, a) {
			issues = append(issues, FieldIssue{Field: "amenities", Code: "unknown", Detail: a})
			continue
		}
		if _, dup := seen[a]; dup {
			continue
		}
		seen[a] = struct{}{}
		out = append(out, a)
	}

	if len(issues) > 0 {
		return nil, &ValidationError{Issues: issues}
	}
	slices.Sort(out) // stable order makes the @> containment index predictable
	return out, nil
}

func Amenities() []string { return slices.Clone(amenityVocabulary) }
