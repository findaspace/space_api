package listing

import (
	"context"
	// "errors"
	"strings"

	"github.com/google/uuid"
)

const minDescriptionLen = 40

// RoleGranter is declared here, by the consumer, so this package never imports
// identity. identity.Service satisfies it.
type RoleGranter interface {
	GrantHost(ctx context.Context, userID uuid.UUID) error
}

type Router interface {
	EnqueueRoute(ctx context.Context, spaceID uuid.UUID) error
}

type MediaCounter interface {
	CountReady(ctx context.Context, spaceID uuid.UUID) (int, error)
	HasVideo(ctx context.Context, spaceID uuid.UUID) (bool, error)
}

type Service struct {
	store  *Store
	roles  RoleGranter
	media  MediaCounter
	routes Router
}

func NewService(store *Store, roles RoleGranter, media MediaCounter, routes Router) *Service {
	return &Service{store: store, roles: roles, media: media, routes: routes}
}

func (s *Service) Summaries(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]Summary, error) {
	return s.store.Summaries(ctx, ids)
}

// AssertHostOwns satisfies media.SpaceOwnership.
func (s *Service) AssertHostOwns(ctx context.Context, spaceID, hostID uuid.UUID) error {
	_, err := s.store.GetForHost(ctx, spaceID, hostID)
	return err
}

type CreateInput struct {
	HostID         uuid.UUID
	Type           SpaceType
	Mode           RentalMode
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
}

func (s *Service) Create(ctx context.Context, in CreateInput) (Space, error) {
	attrs, err := Validate(in.Type, in.Mode, in.Attributes)
	if err != nil {
		return Space{}, err
	}
	amenities, err := ValidateAmenities(in.Amenities)
	if err != nil {
		return Space{}, err
	}

	if err := ValidateDetails(Space{Type: in.Type, Mode: in.Mode, Title: in.Title, Description: in.Description, BasePriceMinor: in.BasePriceMinor, Currency: strings.ToUpper(defaultString(in.Currency, "GHS")), PricePeriod: in.PricePeriod, DepositMinor: in.DepositMinor, AdvanceMonths: in.AdvanceMonths, MaxOccupancy: max16(in.MaxOccupancy, 1), Location: normalizeLocation(in.Location)}); err != nil {
		return Space{}, err
	}

	// Granted before the insert, not after. If the insert then fails the user
	// holds a spare role, which is harmless; the reverse leaves a host who
	// cannot act as one.
	if err := s.roles.GrantHost(ctx, in.HostID); err != nil {
		return Space{}, err
	}

	sp := Space{
		HostID:         in.HostID,
		Type:           in.Type,
		Mode:           in.Mode,
		Title:          strings.TrimSpace(in.Title),
		Description:    strings.TrimSpace(in.Description),
		BasePriceMinor: in.BasePriceMinor,
		Currency:       strings.ToUpper(defaultString(in.Currency, "GHS")),
		PricePeriod:    in.PricePeriod,
		DepositMinor:   in.DepositMinor,
		AdvanceMonths:  in.AdvanceMonths,
		MaxOccupancy:   max16(in.MaxOccupancy, 1),
		Location:       normalizeLocation(in.Location),
		Amenities:      amenities,
		Attributes:     attrs,
	}
	sp.Slug = Slugify(sp.Title, uuid.New())

	return s.store.Create(ctx, sp)
}

func (s *Service) GetForHost(ctx context.Context, id, hostID uuid.UUID) (Space, error) {
	return s.store.GetForHost(ctx, id, hostID)
}

func (s *Service) GetPublic(ctx context.Context, slug string) (Space, error) {
	return s.store.GetPublishedBySlug(ctx, slug)
}

func (s *Service) ListForHost(ctx context.Context, hostID uuid.UUID, limit, offset int32) ([]Space, error) {
	return s.store.ListForHost(ctx, hostID, limit, offset)
}

func (s *Service) Update(ctx context.Context, id, hostID uuid.UUID, p Patch) (Space, error) {
	current, err := s.store.GetForHost(ctx, id, hostID)
	if err != nil {
		return Space{}, err
	}

	if current.Status != StatusDraft && current.Status != StatusInReview {
		return Space{}, ErrBadTransition
	}
	next := current
	if p.Title != nil {
		v := strings.TrimSpace(*p.Title)
		p.Title = &v
		next.Title = v
	}
	if p.Description != nil {
		v := strings.TrimSpace(*p.Description)
		p.Description = &v
		next.Description = v
	}
	if p.BasePriceMinor != nil {
		next.BasePriceMinor = *p.BasePriceMinor
	}
	if p.DepositMinor != nil {
		next.DepositMinor = *p.DepositMinor
	}
	if p.AdvanceMonths != nil {
		next.AdvanceMonths = *p.AdvanceMonths
	}
	if p.MaxOccupancy != nil {
		next.MaxOccupancy = *p.MaxOccupancy
	}
	if p.PricePeriod != nil {
		next.PricePeriod = *p.PricePeriod
	}
	if p.RentalMode != nil {
		next.Mode = *p.RentalMode
	}
	if p.Latitude != nil {
		next.Location.Latitude = *p.Latitude
	}
	if p.Longitude != nil {
		next.Location.Longitude = *p.Longitude
	}
	if p.Locality != nil {
		v := strings.TrimSpace(*p.Locality)
		p.Locality = &v
		next.Location.Locality = v
	}
	if p.Region != nil {
		v := strings.TrimSpace(*p.Region)
		p.Region = &v
		next.Location.Region = v
	}
	if p.Landmark != nil {
		v := strings.TrimSpace(*p.Landmark)
		p.Landmark = &v
		next.Location.Landmark = v
	}
	if p.DigitalAddress != nil {
		v := strings.ToUpper(strings.TrimSpace(*p.DigitalAddress))
		p.DigitalAddress = &v
		next.Location.DigitalAddress = v
	}
	if err := ValidateDetails(next); err != nil {
		return Space{}, err
	}
	if _, err := Validate(next.Type, next.Mode, next.Attributes); err != nil && p.Attributes == nil {
		return Space{}, err
	}
	// Attributes are validated as a whole document. A partial attribute patch
	// would let a host drop a required field by omission.
	if p.Attributes != nil {
		attrs, err := Validate(next.Type, next.Mode, p.Attributes)
		if err != nil {
			return Space{}, err
		}
		p.Attributes = attrs
	}
	if p.Amenities != nil {
		amenities, err := ValidateAmenities(p.Amenities)
		if err != nil {
			return Space{}, err
		}
		p.Amenities = amenities
	}
	return s.store.Update(ctx, id, hostID, p)
}

// Submit moves a draft into review after checking it is actually complete.
func (s *Service) Submit(ctx context.Context, id, hostID uuid.UUID) (Space, error) {
	current, err := s.store.GetForHost(ctx, id, hostID)
	if err != nil {
		return Space{}, err
	}
	if !CanTransition(current.Status, StatusInReview) {
		return Space{}, ErrBadTransition
	}
	if err := s.checkReady(ctx, current); err != nil {
		return Space{}, err
	}
	return s.store.SetStatus(ctx, id, current.Status, StatusInReview)
}

func (s *Service) Withdraw(ctx context.Context, id, hostID uuid.UUID) (Space, error) {
	return s.hostTransition(ctx, id, hostID, StatusDraft)
}

func (s *Service) Archive(ctx context.Context, id, hostID uuid.UUID) (Space, error) {
	return s.hostTransition(ctx, id, hostID, StatusArchived)
}

// Publish is the reviewer's action. Ownership is deliberately not checked: the
// caller is an admin, and the HTTP layer gates it on the role.
func (s *Service) Publish(ctx context.Context, id uuid.UUID) (Space, error) {
	current, err := s.store.GetAny(ctx, id)
	if err != nil {
		return Space{}, err
	}
	if !CanTransition(current.Status, StatusPublished) {
		return Space{}, ErrBadTransition
	}
	if err := s.checkReady(ctx, current); err != nil {
		return Space{}, err
	}
	if s.routes != nil {
		if err := s.routes.EnqueueRoute(ctx, id); err != nil {
			_ = err
		}
	}
	return s.store.SetStatus(ctx, id, current.Status, StatusPublished)
}

func (s *Service) hostTransition(ctx context.Context, id, hostID uuid.UUID, to Status) (Space, error) {
	current, err := s.store.GetForHost(ctx, id, hostID)
	if err != nil {
		return Space{}, err
	}
	if !CanTransition(current.Status, to) {
		return Space{}, ErrBadTransition
	}
	return s.store.SetStatus(ctx, id, current.Status, to)
}

// checkReady is the minimum a listing needs to be worth showing to a stranger:
// somewhere to go, a price, and one photograph of it. Everything else the old
// version demanded now feeds the completeness score instead, because a
// half-finished real listing beats a perfect one nobody created.
func (s *Service) checkReady(ctx context.Context, sp Space) error {
	var issues []FieldIssue

	if sp.Location.Locality == "" {
		issues = append(issues, FieldIssue{Field: "location.locality", Code: "required"})
	}
	if sp.BasePriceMinor <= 0 {
		issues = append(issues, FieldIssue{Field: "base_price_minor", Code: "required"})
	}

	photos, err := s.media.CountReady(ctx, sp.ID)
	if err != nil {
		return err
	}
	if photos < 1 {
		issues = append(issues, FieldIssue{
			Field: "photos", Code: "required",
			Detail: "add at least one photo before publishing",
		})
	}

	if len(issues) > 0 {
		return &ValidationError{Issues: issues}
	}
	return nil
}

func (s *Service) Completeness(ctx context.Context, id, hostID uuid.UUID) (int, []Gap, error) {
	sp, err := s.store.GetForHost(ctx, id, hostID)
	if err != nil {
		return 0, nil, err
	}
	photos, err := s.media.CountReady(ctx, sp.ID)
	if err != nil {
		return 0, nil, err
	}
	hasVideo, err := s.media.HasVideo(ctx, sp.ID)
	if err != nil {
		return 0, nil, err
	}

	score, gaps := Score(Inputs{
		Type: sp.Type, Attributes: sp.Attributes,
		Description: sp.Description, PhotoCount: photos, HasVideo: hasVideo,
	})
	return score, gaps, nil
}

func defaultString(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}

func max16(v, floor int16) int16 {
	if v < floor {
		return floor
	}
	return v
}

func normalizeLocation(l Location) Location {
	l.Locality = strings.TrimSpace(l.Locality)
	l.Region = strings.TrimSpace(l.Region)
	l.Landmark = strings.TrimSpace(l.Landmark)
	l.DigitalAddress = strings.ToUpper(strings.TrimSpace(l.DigitalAddress))
	l.Country = strings.ToUpper(defaultString(l.Country, "GH"))
	return l
}

// GetAnyPublished fetches a listing by id with no ownership check. Only
// published listings come back, so the booking path can call it safely.
func (s *Service) GetAnyPublished(ctx context.Context, id uuid.UUID) (Space, error) {
	sp, err := s.store.GetAny(ctx, id)
	if err != nil {
		return Space{}, err
	}
	if sp.Status != StatusPublished {
		return Space{}, ErrNotFound
	}
	return sp, nil
}

// HostOf satisfies messaging.SpaceLookup. Only published listings can be
// messaged about; a draft has no public page to enquire from.
func (s *Service) HostOf(ctx context.Context, spaceID uuid.UUID) (uuid.UUID, error) {
	sp, err := s.GetAnyPublished(ctx, spaceID)
	if err != nil {
		return uuid.Nil, err
	}
	return sp.HostID, nil
}
