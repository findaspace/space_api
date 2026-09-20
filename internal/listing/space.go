package listing

import (
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

type SpaceType string

const (
	TypeRoom           SpaceType = "room"
	TypeSelfContained  SpaceType = "self_contained"
	TypeChamberAndHall SpaceType = "chamber_and_hall"
	TypeApartment      SpaceType = "apartment"
	TypeHouse          SpaceType = "house"
	TypeBoysQuarters   SpaceType = "boys_quarters"
	TypeHostelBed      SpaceType = "hostel_bed"
	TypeShop           SpaceType = "shop"
	TypeOffice         SpaceType = "office"
	TypeWarehouse      SpaceType = "warehouse"
	TypeEventSpace     SpaceType = "event_space"
	TypeLand           SpaceType = "land"
	TypeParking        SpaceType = "parking"
)

type RentalMode string

const (
	ModeNightly RentalMode = "nightly"
	ModeTerm    RentalMode = "term"
)

type PricePeriod string

const (
	PeriodNight PricePeriod = "night"
	PeriodWeek  PricePeriod = "week"
	PeriodMonth PricePeriod = "month"
	PeriodYear  PricePeriod = "year"
)

type Status string

const (
	StatusDraft     Status = "draft"
	StatusInReview  Status = "in_review"
	StatusPublished Status = "published"
	StatusSuspended Status = "suspended"
	StatusArchived  Status = "archived"
)

type Location struct {
	Latitude       float64 `json:"latitude"`
	Longitude      float64 `json:"longitude"`
	Locality       string  `json:"locality"`
	Region         string  `json:"region"`
	Country        string  `json:"country"`
	Landmark       string  `json:"landmark"`
	DigitalAddress string  `json:"digital_address"`
}

type Space struct {
	ID             uuid.UUID
	HostID         uuid.UUID
	Slug           string
	Type           SpaceType
	Mode           RentalMode
	Status         Status
	Title          string
	Description    string
	BasePriceMinor int64
	Currency       string
	PricePeriod    PricePeriod
	DepositMinor   int64
	AdvanceMonths  int16
	MaxOccupancy   int16
	Location       Location
	Amenities      []string
	Attributes     map[string]any
	PublishedAt    *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

var slugStrip = regexp.MustCompile(`[^a-z0-9]+`)

// Slugify builds the URL segment space_web serves at /spaces/{slug}. The id
// suffix keeps two "2 bedroom apartment in east legon" listings distinct
// without exposing a guessable sequential key.
func Slugify(title string, id uuid.UUID) string {
	base := slugStrip.ReplaceAllString(strings.ToLower(title), "-")
	base = strings.Trim(base, "-")

	const maxBase = 60
	if len(base) > maxBase {
		base = strings.Trim(base[:maxBase], "-")
	}
	if base == "" {
		base = "space"
	}

	hex := strings.ReplaceAll(id.String(), "-", "")
	return base + "-" + hex[len(hex)-6:]
}
