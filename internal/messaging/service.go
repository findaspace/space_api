package messaging

import (
	"context"
	"strings"

	"github.com/google/uuid"
)

type SpaceLookup interface {
	HostOf(ctx context.Context, spaceID uuid.UUID) (uuid.UUID, error)
}

type Service struct {
	store  *Store
	spaces SpaceLookup
}

func NewService(store *Store, spaces SpaceLookup) *Service {
	return &Service{store: store, spaces: spaces}
}

func (s *Service) LastMessages(ctx context.Context, threadIDs []uuid.UUID) (map[uuid.UUID]string, error) {
	return s.store.LastMessages(ctx, threadIDs)
}

// Send posts a message as a guest opening or continuing a conversation about a
// space. Hosts reply through Reply, which needs an existing thread.
func (s *Service) Send(ctx context.Context, spaceID, guestID uuid.UUID, body string) (Thread, Message, error) {
	hostID, err := s.spaces.HostOf(ctx, spaceID)
	if err != nil {
		return Thread{}, Message{}, err
	}
	if hostID == guestID {
		return Thread{}, Message{}, ErrOwnListing
	}

	t, err := s.store.OpenThread(ctx, spaceID, guestID, hostID)
	if err != nil {
		return Thread{}, Message{}, err
	}

	m, err := s.post(ctx, t, guestID, body)
	return t, m, err
}

func (s *Service) Reply(ctx context.Context, threadID, userID uuid.UUID, body string) (Message, error) {
	t, err := s.store.ForParticipant(ctx, threadID, userID)
	if err != nil {
		return Message{}, err
	}
	return s.post(ctx, t, userID, body)
}

func (s *Service) post(ctx context.Context, t Thread, senderID uuid.UUID, body string) (Message, error) {
	body = strings.TrimSpace(body)
	if body == "" {
		return Message{}, ErrEmptyMessage
	}
	if len(body) > MaxBodyLen {
		return Message{}, ErrTooLong
	}

	confirmed, err := s.store.HasConfirmedBooking(ctx, t.SpaceID, t.GuestID)
	if err != nil {
		return Message{}, err
	}

	redacted := false
	if !confirmed {
		// Only the redacted text is ever stored. Keeping the original "for
		// moderation" would put the phone number right back in the database
		// and in every data export.
		body, redacted = Redact(body)
	}
	return s.store.Append(ctx, t.ID, senderID, body, redacted)
}

func (s *Service) Inbox(ctx context.Context, userID uuid.UUID, limit int32) ([]Thread, error) {
	return s.store.Inbox(ctx, userID, limit)
}

func (s *Service) Messages(ctx context.Context, threadID, userID, before uuid.UUID, limit int32) (Thread, []Message, error) {
	t, err := s.store.ForParticipant(ctx, threadID, userID)
	if err != nil {
		return Thread{}, nil, err
	}
	msgs, err := s.store.Page(ctx, threadID, before, limit)
	return t, msgs, err
}

func (s *Service) MarkRead(ctx context.Context, threadID, userID uuid.UUID) error {
	if _, err := s.store.ForParticipant(ctx, threadID, userID); err != nil {
		return err
	}
	return s.store.MarkRead(ctx, threadID, userID)
}
