package search

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

type Sort string

const (
	SortRelevance Sort = "relevance"
	SortPriceAsc  Sort = "price_asc"
	SortPriceDesc Sort = "price_desc"
	SortDistance  Sort = "distance"
	SortNewest    Sort = "newest"
)

const (
	DefaultLimit   = 20
	MaxLimit       = 50
	DefaultRadiusM = 5000
	MaxRadiusM     = 100000
)

var (
	ErrBadCursor = errors.New("search: invalid cursor")
	ErrBadSort   = errors.New("search: unknown sort")
	ErrBadRange  = errors.New("search: check-out must be after check-in")
)

// Cursor is the last row of the previous page. Both halves are needed: the sort
// value alone is not unique, and ties would skip or repeat rows across pages.
type Cursor struct {
	Value int64
	ID    uuid.UUID
}

func (c Cursor) IsZero() bool { return c.ID == uuid.Nil }

func (c Cursor) Encode() string {
	if c.IsZero() {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(
		[]byte(strconv.FormatInt(c.Value, 10) + ":" + c.ID.String()))
}

func ParseCursor(raw string) (Cursor, error) {
	if raw == "" {
		return Cursor{}, nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return Cursor{}, ErrBadCursor
	}

	value, idPart, ok := strings.Cut(string(decoded), ":")
	if !ok {
		return Cursor{}, ErrBadCursor
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return Cursor{}, ErrBadCursor
	}
	id, err := uuid.Parse(idPart)
	if err != nil {
		return Cursor{}, ErrBadCursor
	}
	return Cursor{Value: n, ID: id}, nil
}

type Query struct {
	Text       string
	Types      []string
	Mode       string
	Locality   string
	MinPrice   *int64
	MaxPrice   *int64
	Guests     int
	Amenities  []string
	Attributes map[string]any

	Lat     *float64
	Lng     *float64
	RadiusM int

	CheckIn  *time.Time
	CheckOut *time.Time

	Sort   Sort
	Cursor Cursor
	Limit  int32
}

// Normalize clamps the query into a shape the SQL can assume. Every caller runs
// it, so the builder never has to defend against a negative limit or a radius
// large enough to match the planet.
func (q *Query) Normalize() error {
	if q.Sort == "" {
		q.Sort = SortRelevance
	}
	if _, ok := sortSpecs[q.Sort]; !ok {
		return ErrBadSort
	}
	if q.Sort == SortDistance && (q.Lat == nil || q.Lng == nil) {
		// Sorting by distance from nowhere is meaningless; fall back rather
		// than return an error the client cannot act on.
		q.Sort = SortRelevance
	}

	if q.Limit <= 0 {
		q.Limit = DefaultLimit
	}
	if q.Limit > MaxLimit {
		q.Limit = MaxLimit
	}
	if q.RadiusM <= 0 {
		q.RadiusM = DefaultRadiusM
	}
	if q.RadiusM > MaxRadiusM {
		q.RadiusM = MaxRadiusM
	}
	if q.Guests < 0 {
		q.Guests = 0
	}

	q.Text = strings.TrimSpace(q.Text)
	q.Locality = strings.TrimSpace(q.Locality)

	if q.CheckIn != nil && q.CheckOut != nil {
		if !q.CheckOut.After(*q.CheckIn) {
			return ErrBadRange
		}
	}
	return nil
}

type Result struct {
	ID          uuid.UUID `json:"-"`
	Slug        string    `json:"slug"`
	Title       string    `json:"title"`
	SpaceType   string    `json:"space_type"`
	RentalMode  string    `json:"rental_mode"`
	PriceMinor  int64     `json:"price_minor"`
	Currency    string    `json:"currency"`
	PricePeriod string    `json:"price_period"`
	Locality    string    `json:"locality"`
	Latitude    float64   `json:"latitude"`
	Longitude   float64   `json:"longitude"`
	PhotoCount  int       `json:"photo_count"`
	CoverKey    string    `json:"-"`
	CoverURL    string    `json:"cover_url,omitempty"`
	Rating      float64   `json:"rating,omitempty"`
	ReviewCount int       `json:"review_count"`
	DistanceM   *int      `json:"distance_m,omitempty"`
	Score       int64     `json:"-"`
}

type Page struct {
	Results    []Result `json:"results"`
	NextCursor string   `json:"next_cursor,omitempty"`
}

// sortSpec pairs the SQL expression a sort orders by with its direction. The
// map is the whitelist: nothing reaches ORDER BY that is not a value here.
type sortSpec struct {
	expr string
	desc bool
}

var sortSpecs = map[Sort]sortSpec{
	SortRelevance: {"score", true},
	SortPriceAsc:  {"price_minor", false},
	SortPriceDesc: {"price_minor", true},
	SortDistance:  {"distance_m", false},
	SortNewest:    {"published_epoch", true},
}

// builder accumulates SQL and its arguments. It has exactly one way to
// introduce a value, and that way always produces a placeholder, so no caller
// can concatenate user input into the statement.
type builder struct {
	args []any
}

func (b *builder) arg(v any) string {
	b.args = append(b.args, v)
	return "$" + strconv.Itoa(len(b.args))
}

func (b *builder) argf(format string, values ...any) string {
	placeholders := make([]any, len(values))
	for i, v := range values {
		placeholders[i] = b.arg(v)
	}
	return fmt.Sprintf(format, placeholders...)
}
