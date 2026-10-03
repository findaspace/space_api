package search

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestCursorRoundTrip(t *testing.T) {
	c := Cursor{Value: -4207, ID: uuid.New()}

	parsed, err := ParseCursor(c.Encode())
	if err != nil {
		t.Fatal(err)
	}
	if parsed != c {
		t.Errorf("round trip lost data: %+v", parsed)
	}

	if got, _ := ParseCursor(""); !got.IsZero() {
		t.Error("empty cursor should be zero")
	}
	for _, bad := range []string{"!!!", "bm90LWEtY3Vyc29y", "MTIz"} {
		if _, err := ParseCursor(bad); err == nil {
			t.Errorf("ParseCursor(%q) should have failed", bad)
		}
	}
}

// Nothing a caller controls may appear in the SQL text. If this test ever
// fails, the builder has grown a string concatenation it should not have.
func TestNoUserInputReachesTheSQL(t *testing.T) {
	q := Query{
		Text:       "'; DROP TABLE spaces; --",
		Locality:   "East Legon'); DELETE FROM users; --",
		Types:      []string{"apartment'; --"},
		Mode:       "nightly",
		Amenities:  []string{"wifi'); --"},
		Attributes: map[string]any{"bedrooms": 2},
	}
	if err := q.Normalize(); err != nil {
		t.Fatal(err)
	}

	sql, args := build(q)

	for _, needle := range []string{"DROP TABLE", "DELETE FROM", "1'='1", "--"} {
		if strings.Contains(sql, needle) {
			t.Errorf("%q leaked into the SQL", needle)
		}
	}
	if len(args) == 0 {
		t.Fatal("no arguments; the values went somewhere they should not have")
	}
}

func TestGeoClausesOnlyWithCoordinates(t *testing.T) {
	var q Query
	if err := q.Normalize(); err != nil {
		t.Fatal(err)
	}
	sql, _ := build(q)
	if strings.Contains(sql, "ST_DWithin") {
		t.Error("a radius filter appeared without coordinates")
	}

	lat, lng := 5.6363, -0.1580
	q = Query{Lat: &lat, Lng: &lng, RadiusM: 3000}
	if err := q.Normalize(); err != nil {
		t.Fatal(err)
	}
	sql, _ = build(q)
	if !strings.Contains(sql, "ST_DWithin") {
		t.Error("coordinates given but no radius filter")
	}
	// The expression must match migration 00004 exactly or the index is unused.
	if !strings.Contains(sql, geoExpr) {
		t.Error("the indexed geo expression was not used verbatim")
	}
}

func TestAvailabilityClausesOnlyWithDates(t *testing.T) {
	in := time.Date(2027, 3, 1, 0, 0, 0, 0, time.UTC)
	out := in.AddDate(0, 0, 3)

	q := Query{CheckIn: &in, CheckOut: &out}
	if err := q.Normalize(); err != nil {
		t.Fatal(err)
	}
	sql, _ := build(q)

	if !strings.Contains(sql, "FROM bookings bk") || !strings.Contains(sql, "availability_blocks ab") {
		t.Error("date range given but availability was not checked")
	}
	// Must agree with the EXCLUDE predicate in migration 00008.
	for _, status := range []string{"'held'", "'approved'", "'confirmed'", "'active'"} {
		if !strings.Contains(sql, status) {
			t.Errorf("%s missing from the live-booking list", status)
		}
	}
}

func TestNormalizeClamps(t *testing.T) {
	q := Query{Limit: 9999, RadiusM: 9_000_000, Guests: -3}
	if err := q.Normalize(); err != nil {
		t.Fatal(err)
	}
	if q.Limit != MaxLimit || q.RadiusM != MaxRadiusM || q.Guests != 0 {
		t.Errorf("not clamped: %+v", q)
	}
}

// Distance sorting without a location is meaningless, so it degrades rather
// than erroring at a client that cannot fix it.
func TestDistanceSortFallsBackWithoutCoordinates(t *testing.T) {
	q := Query{Sort: SortDistance}
	if err := q.Normalize(); err != nil {
		t.Fatal(err)
	}
	if q.Sort != SortRelevance {
		t.Errorf("sort = %s, want relevance", q.Sort)
	}
}

func TestUnknownSortRejected(t *testing.T) {
	q := Query{Sort: "cheapest; DROP TABLE spaces"}
	if err := q.Normalize(); err != ErrBadSort {
		t.Errorf("err = %v, want ErrBadSort", err)
	}
}

func TestCursorDirectionMatchesSort(t *testing.T) {
	cases := map[Sort]string{
		SortRelevance: "<",
		SortPriceDesc: "<",
		SortNewest:    "<",
		SortPriceAsc:  ">",
	}
	for sort, op := range cases {
		q := Query{Sort: sort, Cursor: Cursor{Value: 100, ID: uuid.New()}}
		if err := q.Normalize(); err != nil {
			t.Fatal(err)
		}
		sql, _ := build(q)
		spec := sortSpecs[sort]
		want := "(q." + spec.expr + ", q.id) " + op
		if !strings.Contains(sql, want) {
			t.Errorf("%s: expected %q in the cursor clause", sort, want)
		}
	}
}

func TestPricePeriodAndInvalidMode(t *testing.T) {
	q := Query{Mode: "flexible", Period: "hour"}
	if err := q.Normalize(); err != nil {
		t.Fatal(err)
	}
	sql, args := build(q)
	if !strings.Contains(sql, "s.price_period = $1::price_period") || len(args) < 2 || args[0] != "hour" {
		t.Fatalf("period filter missing: %s %v", sql, args)
	}
	for _, q := range []Query{{Mode: "nightly' OR true"}, {Period: "invented"}} {
		if q.Normalize() != ErrBadFilter {
			t.Fatal("invalid filter accepted")
		}
	}
}
