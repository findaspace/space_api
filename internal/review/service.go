package review

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
)

type Service struct {
	store *Store
	now   func() time.Time
}

func NewService(store *Store) *Service { return &Service{store: store, now: time.Now} }

func (s *Service) Write(ctx context.Context, bookingID, userID uuid.UUID, rating int, body string) (Review, error) {
	if !ValidRating(rating) {
		return Review{}, ErrBadRating
	}

	st, err := s.store.Stay(ctx, bookingID)
	if err != nil {
		return Review{}, err
	}

	role, subject, err := Author(st, userID)
	if err != nil {
		return Review{}, ErrNotFound // a stranger learns nothing about the booking
	}
	if err := Eligible(st, s.now()); err != nil {
		return Review{}, err
	}

	return s.store.Submit(ctx, Review{
		BookingID: st.BookingID, SpaceID: st.SpaceID,
		AuthorID: userID, SubjectID: subject, AuthorRole: role,
		Rating: rating, Body: strings.TrimSpace(body),
	})
}

func (s *Service) ForSpace(ctx context.Context, spaceID uuid.UUID, limit int32) ([]Review, Summary, error) {
	return s.store.ForSpace(ctx, spaceID, limit)
}

func (s *Service) RevealExpired(ctx context.Context) (int64, error) {
	return s.store.RevealExpired(ctx)
}
