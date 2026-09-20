package listing

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
)

const minDescriptionLen = 40

// RoleGranter is declared here, by the consumer, so this package never imports
// identity. identity.Service satisfies it.
type RoleGranter interface {
	GrantHost(ctx context.Context, userID uuid.UUID) error
}

type Service struct {
	store *Store
	roles RoleGranter
}

func NewService(store *Store, roles RoleGranter) *Service {
	return &Service{store: store, roles: roles}
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

	// Attributes are validated as a whole document. A partial attribute patch
	// would let a host drop a required field by omission.
	if p.Attributes != nil {
		attrs, err := Validate(current.Type, current.Mode, p.Attributes)
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
	if err := checkReady(current); err != nil {
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
	if err := checkReady(current); err != nil {
		return Space{}, err
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

// checkReady runs at submit and again at publish. A draft can sit for weeks
// while the attribute schema gains a required field, so validating only at
// write time publishes listings that no longer satisfy their own schema.
// Phase 3c adds the photo requirement here.
func checkReady(sp Space) error {
	var issues []FieldIssue

	if len(strings.TrimSpace(sp.Description)) < minDescriptionLen {
		issues = append(issues, FieldIssue{
			Field: "description", Code: "too_short",
			Detail: "describe the space in at least 40 characters",
		})
	}
	if sp.Location.Locality == "" {
		issues = append(issues, FieldIssue{Field: "location.locality", Code: "required"})
	}
	if _, err := Validate(sp.Type, sp.Mode, sp.Attributes); err != nil {
		var ve *ValidationError
		if errors.As(err, &ve) {
			issues = append(issues, ve.Issues...)
		} else {
			return err
		}
	}

	if len(issues) > 0 {
		return &ValidationError{Issues: issues}
	}
	return nil
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
