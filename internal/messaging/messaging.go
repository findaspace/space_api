package messaging

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

type Role string

const (
	RoleGuest Role = "guest"
	RoleHost  Role = "host"
)

type Thread struct {
	ID            uuid.UUID
	SpaceID       uuid.UUID
	GuestID       uuid.UUID
	HostID        uuid.UUID
	LastMessageAt time.Time
	GuestReadAt   *time.Time
	HostReadAt    *time.Time
	Flagged       bool
	CreatedAt     time.Time
}

// RoleOf reports which side of the thread a user is on.
func (t Thread) RoleOf(userID uuid.UUID) (Role, bool) {
	switch userID {
	case t.GuestID:
		return RoleGuest, true
	case t.HostID:
		return RoleHost, true
	}
	return "", false
}

// Unread is computed from the watermark, never stored.
func (t Thread) Unread(userID uuid.UUID) bool {
	role, ok := t.RoleOf(userID)
	if !ok {
		return false
	}
	readAt := t.GuestReadAt
	if role == RoleHost {
		readAt = t.HostReadAt
	}
	return readAt == nil || t.LastMessageAt.After(*readAt)
}

type Message struct {
	ID        uuid.UUID
	ThreadID  uuid.UUID
	SenderID  uuid.UUID
	Body      string
	Redacted  bool
	CreatedAt time.Time
}

var (
	ErrNotFound     = errors.New("messaging: not found")
	ErrOwnListing   = errors.New("messaging: you cannot message yourself")
	ErrEmptyMessage = errors.New("messaging: message is empty")
	ErrTooLong      = errors.New("messaging: message is too long")
)

const MaxBodyLen = 4000
