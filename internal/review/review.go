package review

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// Window is how long after a stay ends either side may review. It is also the
// double-blind deadline: after it, whatever was submitted is revealed.
const Window = 14 * 24 * time.Hour

type Role string

const (
	RoleGuest Role = "guest"
	RoleHost  Role = "host"
)

type Review struct {
	ID         uuid.UUID
	BookingID  uuid.UUID
	SpaceID    uuid.UUID
	AuthorID   uuid.UUID
	SubjectID  uuid.UUID
	AuthorRole Role
	Rating     int
	Body       string
	RevealedAt *time.Time
	CreatedAt  time.Time
}

type Stay struct {
	BookingID uuid.UUID
	SpaceID   uuid.UUID
	GuestID   uuid.UUID
	HostID    uuid.UUID
	Status    string
	EndsOn    time.Time
}

var (
	ErrNotFound       = errors.New("review: not found")
	ErrNotEligible    = errors.New("review: this stay cannot be reviewed")
	ErrWindowClosed   = errors.New("review: the review window has closed")
	ErrAlreadyWritten = errors.New("review: already reviewed")
	ErrBadRating      = errors.New("review: rating must be between 1 and 5")
	ErrNotParty       = errors.New("review: not part of this booking")
)

// Eligible decides whether a review may be written now. Only completed stays
// qualify: reviewing a stay that has not happened is how fake reviews get in.
func Eligible(s Stay, now time.Time) error {
	if s.Status != "completed" {
		return ErrNotEligible
	}
	if now.After(s.EndsOn.Add(Window)) {
		return ErrWindowClosed
	}
	return nil
}

// Author works out who is writing and about whom from the booking itself, never
// from the request. A client-supplied role would let a guest write the host's
// review of themselves.
func Author(s Stay, userID uuid.UUID) (role Role, subject uuid.UUID, err error) {
	switch userID {
	case s.GuestID:
		return RoleGuest, s.HostID, nil
	case s.HostID:
		return RoleHost, s.GuestID, nil
	}
	return "", uuid.Nil, ErrNotParty
}

func ValidRating(r int) bool { return r >= 1 && r <= 5 }
