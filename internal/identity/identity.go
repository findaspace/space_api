package identity

import (
	"errors"
	"slices"
	"time"

	"github.com/google/uuid"
)

type Role string

const (
	RoleGuest   Role = "guest"
	RoleHost    Role = "host"
	RoleAdmin   Role = "admin"
	RoleSupport Role = "support"
)

type Status string

const (
	StatusActive    Status = "active"
	StatusSuspended Status = "suspended"
	StatusDeleted   Status = "deleted"
)

type User struct {
	ID              uuid.UUID
	Phone           string
	Email           *string
	DisplayName     string
	Status          Status
	PhoneVerifiedAt *time.Time
	Roles           []Role
	CreatedAt       time.Time
}

func (u User) Has(r Role) bool { return slices.Contains(u.Roles, r) }

var (
	ErrInvalidPhone   = errors.New("identity: invalid phone number")
	ErrOTPNotFound    = errors.New("identity: no active challenge")
	ErrOTPExpired     = errors.New("identity: challenge expired")
	ErrOTPMismatch    = errors.New("identity: incorrect code")
	ErrOTPExhausted   = errors.New("identity: too many attempts")
	ErrTooManyOTP     = errors.New("identity: too many requests")
	ErrSessionInvalid = errors.New("identity: session invalid")
	ErrSessionReuse   = errors.New("identity: refresh token reused")
	ErrUserSuspended  = errors.New("identity: account suspended")
)
