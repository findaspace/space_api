package listing

import (
	"fmt"
	"math"
	"slices"
	"strings"
)

type FieldKind string

const (
	KindInt    FieldKind = "int"
	KindFloat  FieldKind = "float"
	KindBool   FieldKind = "bool"
	KindEnum   FieldKind = "enum"
	KindString FieldKind = "string"
)

// Field is one attribute definition. It is the single source of truth for
// server validation, the client's form renderer, and the search filter list.
type Field struct {
	Key   string    `json:"key"`
	Label string    `json:"label"`
	Kind  FieldKind `json:"kind"`

	// Weight is how much this field moves the completeness score, and the order
	// in which a host is prompted for what is missing. It gates nothing: a
	// listing publishes without any of them.
	Weight int `json:"weight"`

	Unit    string   `json:"unit,omitempty"`
	Min     *float64 `json:"min,omitempty"`
	Max     *float64 `json:"max,omitempty"`
	Options []string `json:"options,omitempty"`

	// Filterable marks fields the search endpoint accepts as query parameters.
	Filterable bool `json:"filterable"`
}

type TypeSchema struct {
	Type    SpaceType     `json:"type"`
	Periods []PricePeriod `json:"price_periods"`
	Label   string        `json:"label"`
	Modes   []RentalMode  `json:"modes"`
	Fields  []Field       `json:"fields"`
}

type FieldIssue struct {
	Field  string `json:"field"`
	Code   string `json:"code"`
	Detail string `json:"detail,omitempty"`
}

type ValidationError struct{ Issues []FieldIssue }

func (e *ValidationError) Error() string {
	parts := make([]string, 0, len(e.Issues))
	for _, i := range e.Issues {
		parts = append(parts, i.Field+":"+i.Code)
	}
	return "listing: invalid attributes (" + strings.Join(parts, ", ") + ")"
}

func n(v float64) *float64 { return &v }

func join(groups ...[]Field) []Field {
	out := make([]Field, 0, 12)
	for _, g := range groups {
		out = append(out, g...)
	}
	return out
}

// Utilities that a Ghanaian renter asks about before anything else. Mains water
// and grid power are both unreliable enough that omitting these turns every
// enquiry into a phone call, which is why they carry the top weight.
var utilityFields = []Field{
	{Key: "water_source", Label: "Water source", Kind: KindEnum, Weight: 3, Filterable: true,
		Options: []string{"pipe", "borehole", "tanker", "well"}},
	{Key: "power_backup", Label: "Power backup", Kind: KindEnum, Weight: 3, Filterable: true,
		Options: []string{"none", "generator", "inverter", "solar"}},
}

var residentialFields = []Field{
	{Key: "bedrooms", Label: "Bedrooms", Kind: KindInt, Weight: 3, Filterable: true, Min: n(0), Max: n(50)},
	{Key: "bathrooms", Label: "Bathrooms", Kind: KindInt, Weight: 2, Filterable: true, Min: n(0), Max: n(50)},
	{Key: "self_contained", Label: "Self contained", Kind: KindBool, Weight: 3, Filterable: true},
	{Key: "furnishing", Label: "Furnishing", Kind: KindEnum, Weight: 2, Filterable: true,
		Options: []string{"unfurnished", "semi_furnished", "fully_furnished"}},
	{Key: "kitchen", Label: "Kitchen", Kind: KindEnum, Weight: 2, Filterable: true,
		Options: []string{"none", "shared", "private"}},
	{Key: "floor_area_sqm", Label: "Floor area", Kind: KindFloat, Weight: 1, Unit: "m2", Min: n(1), Max: n(100000)},
}

var commercialFields = []Field{
	{Key: "floor_area_sqm", Label: "Floor area", Kind: KindFloat, Weight: 3, Filterable: true, Unit: "m2", Min: n(1), Max: n(500000)},
	{Key: "frontage_m", Label: "Frontage", Kind: KindFloat, Weight: 1, Unit: "m", Min: n(0), Max: n(1000)},
	{Key: "storefront", Label: "Has storefront", Kind: KindBool, Weight: 2, Filterable: true},
	{Key: "ceiling_height_m", Label: "Ceiling height", Kind: KindFloat, Weight: 1, Unit: "m", Min: n(1), Max: n(50)},
	{Key: "power_phase", Label: "Power phase", Kind: KindEnum, Weight: 2, Filterable: true,
		Options: []string{"single", "three"}},
	{Key: "loading_access", Label: "Loading access", Kind: KindEnum, Weight: 1,
		Options: []string{"none", "van", "truck", "container"}},
}

var sportsFields = []Field{
	{Key: "sport", Label: "Sport", Kind: KindEnum, Weight: 3, Filterable: true, Options: []string{"football", "basketball", "tennis", "volleyball", "badminton", "multi_sport", "other"}},
	{Key: "surface", Label: "Playing surface", Kind: KindEnum, Weight: 3, Filterable: true, Options: []string{"grass", "artificial_turf", "hard_court", "clay", "indoor", "other"}},
	{Key: "capacity", Label: "Capacity", Kind: KindInt, Weight: 2, Min: n(1), Max: n(100000)},
	{Key: "floodlights", Label: "Floodlights", Kind: KindBool, Weight: 2, Filterable: true},
	{Key: "changing_rooms", Label: "Changing rooms", Kind: KindBool, Weight: 1},
	{Key: "opening_hours", Label: "Opening hours", Kind: KindString, Weight: 2},
}
var schemas = map[SpaceType]TypeSchema{
	TypeFootballPitch:  {Label: "Football pitch", Modes: []RentalMode{ModeFlexible}, Fields: sportsFields},
	TypeSportsCourt:    {Label: "Sports court", Modes: []RentalMode{ModeFlexible}, Fields: sportsFields},
	TypeSportsFacility: {Label: "Sports facility", Modes: []RentalMode{ModeFlexible}, Fields: sportsFields},
	TypeStudio:         {Label: "Studio", Modes: []RentalMode{ModeFlexible, ModeTerm}, Fields: join(utilityFields, []Field{{Key: "capacity", Label: "Capacity", Kind: KindInt, Weight: 3, Min: n(1), Max: n(1000)}, {Key: "studio_use", Label: "Studio use", Kind: KindEnum, Weight: 2, Options: []string{"photography", "music", "dance", "film", "multi_use"}}})},
	TypeRoom: {Label: "Room", Modes: []RentalMode{ModeNightly, ModeTerm},
		Fields: join(residentialFields, utilityFields)},
	TypeSelfContained: {Label: "Self contained", Modes: []RentalMode{ModeNightly, ModeTerm},
		Fields: join(residentialFields, utilityFields)},
	TypeChamberAndHall: {Label: "Chamber and hall", Modes: []RentalMode{ModeNightly, ModeTerm},
		Fields: join(residentialFields, utilityFields)},
	TypeApartment: {Label: "Apartment", Modes: []RentalMode{ModeNightly, ModeTerm},
		Fields: join(residentialFields, utilityFields)},
	TypeHouse: {Label: "House", Modes: []RentalMode{ModeNightly, ModeTerm},
		Fields: join(residentialFields, utilityFields)},
	TypeBoysQuarters: {Label: "Boys quarters", Modes: []RentalMode{ModeNightly, ModeTerm},
		Fields: join(residentialFields, utilityFields)},

	TypeHostelBed: {Label: "Hostel bed", Modes: []RentalMode{ModeNightly, ModeTerm},
		Fields: join(utilityFields, []Field{
			{Key: "beds_in_room", Label: "Beds in room", Kind: KindInt, Weight: 3, Filterable: true, Min: n(1), Max: n(20)},
			{Key: "gender_policy", Label: "Gender policy", Kind: KindEnum, Weight: 3, Filterable: true,
				Options: []string{"mixed", "male_only", "female_only"}},
		})},

	TypeShop:      {Label: "Shop", Modes: []RentalMode{ModeTerm}, Fields: join(commercialFields, utilityFields)},
	TypeOffice:    {Label: "Office", Modes: []RentalMode{ModeTerm, ModeFlexible}, Fields: join(commercialFields, utilityFields)},
	TypeWarehouse: {Label: "Warehouse", Modes: []RentalMode{ModeTerm}, Fields: join(commercialFields, utilityFields)},

	TypeEventSpace: {Label: "Event space", Modes: []RentalMode{ModeNightly, ModeFlexible},
		Fields: join(utilityFields, []Field{
			{Key: "capacity", Label: "Capacity", Kind: KindInt, Weight: 3, Filterable: true, Min: n(1), Max: n(100000)},
			{Key: "indoor", Label: "Indoor", Kind: KindBool, Weight: 2, Filterable: true},
			{Key: "has_stage", Label: "Has stage", Kind: KindBool, Weight: 1},
			{Key: "parking_spaces", Label: "Parking spaces", Kind: KindInt, Weight: 1, Min: n(0), Max: n(5000)},
		})},

	TypeLand: {Label: "Land", Modes: []RentalMode{ModeTerm},
		Fields: []Field{
			{Key: "plot_size_acres", Label: "Plot size", Kind: KindFloat, Weight: 3, Filterable: true, Unit: "acres", Min: n(0.01), Max: n(10000)},
			{Key: "title_type", Label: "Title type", Kind: KindEnum, Weight: 3, Filterable: true,
				Options: []string{"freehold", "leasehold", "family", "stool"}},
			{Key: "serviced", Label: "Serviced", Kind: KindBool, Weight: 2, Filterable: true},
			{Key: "zoning", Label: "Zoning", Kind: KindEnum, Weight: 2, Filterable: true,
				Options: []string{"residential", "commercial", "agricultural", "industrial", "mixed"}},
		}},

	TypeParking: {Label: "Parking", Modes: []RentalMode{ModeNightly, ModeTerm, ModeFlexible},
		Fields: []Field{
			{Key: "covered", Label: "Covered", Kind: KindBool, Weight: 3, Filterable: true},
			{Key: "vehicle_size", Label: "Largest vehicle", Kind: KindEnum, Weight: 3, Filterable: true,
				Options: []string{"motorbike", "saloon", "suv", "van", "truck"}},
		}},
}

func init() {
	// The map key is the type, so setting it in every literal above would just
	// be a chance to get them out of sync.
	for t, s := range schemas {
		s.Type = t
		for _, mode := range s.Modes {
			s.Periods = append(s.Periods, AllowedPeriods(t, mode)...)
		}
		schemas[t] = s
	}
}

func SchemaFor(t SpaceType) (TypeSchema, bool) {
	s, ok := schemas[t]
	return s, ok
}

// Schemas returns every type in a stable order, for GET /v1/space-types.
func Schemas() []TypeSchema {
	out := make([]TypeSchema, 0, len(schemas))
	for _, s := range schemas {
		out = append(out, s)
	}
	slices.SortFunc(out, func(a, b TypeSchema) int { return strings.Compare(string(a.Type), string(b.Type)) })
	return out
}

// Validate checks whatever attributes were supplied and returns the normalized
// map to store. Nothing is mandatory: a host publishes with none of them, and
// what they skip lowers their completeness score instead of blocking them.
//
// Unknown keys are still an error. Silently dropping a field the client sent
// means the host thinks they set something they did not.
func Validate(t SpaceType, mode RentalMode, attrs map[string]any) (map[string]any, error) {
	schema, ok := SchemaFor(t)
	if !ok {
		return nil, &ValidationError{Issues: []FieldIssue{{Field: "space_type", Code: "unknown"}}}
	}
	if !slices.Contains(schema.Modes, mode) {
		return nil, &ValidationError{Issues: []FieldIssue{{
			Field:  "rental_mode",
			Code:   "not_allowed",
			Detail: fmt.Sprintf("%s cannot be rented %s", schema.Label, mode),
		}}}
	}

	var issues []FieldIssue
	known := make(map[string]Field, len(schema.Fields))
	out := make(map[string]any, len(schema.Fields))

	for _, f := range schema.Fields {
		known[f.Key] = f

		raw, present := attrs[f.Key]
		if !present || raw == nil {
			continue
		}

		value, issue := coerce(f, raw)
		if issue != nil {
			issues = append(issues, *issue)
			continue
		}
		out[f.Key] = value
	}

	for key := range attrs {
		if _, ok := known[key]; !ok {
			issues = append(issues, FieldIssue{Field: key, Code: "unknown"})
		}
	}

	if len(issues) > 0 {
		slices.SortFunc(issues, func(a, b FieldIssue) int { return strings.Compare(a.Field, b.Field) })
		return nil, &ValidationError{Issues: issues}
	}
	return out, nil
}

// coerce validates one value and returns what should be stored. JSON numbers
// arrive as float64 regardless of how they were written, so integer fields are
// checked for integrality rather than for Go type.
func coerce(f Field, raw any) (any, *FieldIssue) {
	bad := func(code, detail string) (any, *FieldIssue) {
		return nil, &FieldIssue{Field: f.Key, Code: code, Detail: detail}
	}

	switch f.Kind {
	case KindBool:
		v, ok := raw.(bool)
		if !ok {
			return bad("type", "expected a boolean")
		}
		return v, nil

	case KindInt, KindFloat:
		v, ok := raw.(float64)
		if !ok {
			return bad("type", "expected a number")
		}
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return bad("type", "expected a finite number")
		}
		if f.Kind == KindInt && v != math.Trunc(v) {
			return bad("type", "expected a whole number")
		}
		if f.Min != nil && v < *f.Min {
			return bad("range", fmt.Sprintf("minimum is %g", *f.Min))
		}
		if f.Max != nil && v > *f.Max {
			return bad("range", fmt.Sprintf("maximum is %g", *f.Max))
		}
		if f.Kind == KindInt {
			return int64(v), nil
		}
		return v, nil

	case KindEnum:
		v, ok := raw.(string)
		if !ok {
			return bad("type", "expected a string")
		}
		if !slices.Contains(f.Options, v) {
			return bad("option", "must be one of "+strings.Join(f.Options, ", "))
		}
		return v, nil

	case KindString:
		v, ok := raw.(string)
		if !ok {
			return bad("type", "expected a string")
		}
		v = strings.TrimSpace(v)
		if len(v) > 500 {
			return bad("range", "maximum length is 500")
		}
		return v, nil
	}
	return bad("type", "unsupported field kind")
}
