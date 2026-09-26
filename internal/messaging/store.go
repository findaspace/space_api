package messaging

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/samnart1/space_api/internal/platform/postgres"
)

type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

const threadColumns = `id, space_id, guest_id, host_id, last_message_at,
	guest_read_at, host_read_at, flagged, created_at`

func scanThread(row pgx.Row) (Thread, error) {
	var t Thread
	err := row.Scan(&t.ID, &t.SpaceID, &t.GuestID, &t.HostID, &t.LastMessageAt,
		&t.GuestReadAt, &t.HostReadAt, &t.Flagged, &t.CreatedAt)
	return t, err
}

// LastMessages returns the newest message body of each thread, in one query
// for a whole inbox rather than one per conversation. Bodies are stored
// already redacted, so a preview reveals nothing the conversation would not.
func (s *Store) LastMessages(ctx context.Context, threadIDs []uuid.UUID) (map[uuid.UUID]string, error) {
	out := make(map[uuid.UUID]string, len(threadIDs))
	if len(threadIDs) == 0 {
		return out, nil
	}
	rows, err := s.pool.Query(ctx, `
		SELECT DISTINCT ON (thread_id) thread_id, body
		FROM messages
		WHERE thread_id = ANY($1::uuid[])
		ORDER BY thread_id, id DESC`, threadIDs)
	if err != nil {
		return nil, fmt.Errorf("last messages: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var body string
		if err := rows.Scan(&id, &body); err != nil {
			return nil, fmt.Errorf("scan last message: %w", err)
		}
		out[id] = body
	}
	return out, rows.Err()
}

// OpenThread returns the guest's existing thread for a space or creates it. The
// unique constraint makes this safe under concurrent first messages.
func (s *Store) OpenThread(ctx context.Context, spaceID, guestID, hostID uuid.UUID) (Thread, error) {
	t, err := scanThread(s.pool.QueryRow(ctx, `
		INSERT INTO threads (space_id, guest_id, host_id)
		VALUES ($1, $2, $3)
		ON CONFLICT (space_id, guest_id) DO UPDATE SET space_id = EXCLUDED.space_id
		RETURNING `+threadColumns, spaceID, guestID, hostID))
	if err != nil {
		return Thread{}, fmt.Errorf("open thread: %w", err)
	}
	return t, nil
}

// ForParticipant returns a thread only to one of its two members.
func (s *Store) ForParticipant(ctx context.Context, threadID, userID uuid.UUID) (Thread, error) {
	t, err := scanThread(s.pool.QueryRow(ctx,
		`SELECT `+threadColumns+` FROM threads
		 WHERE id = $1 AND (guest_id = $2 OR host_id = $2)`, threadID, userID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Thread{}, ErrNotFound
	}
	if err != nil {
		return Thread{}, fmt.Errorf("get thread: %w", err)
	}
	return t, nil
}

func (s *Store) Inbox(ctx context.Context, userID uuid.UUID, limit int32) ([]Thread, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+threadColumns+` FROM threads
		 WHERE guest_id = $1 OR host_id = $1
		 ORDER BY last_message_at DESC LIMIT $2`, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("list threads: %w", err)
	}
	defer rows.Close()

	out := []Thread{}
	for rows.Next() {
		t, err := scanThread(rows)
		if err != nil {
			return nil, fmt.Errorf("scan thread: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Append writes a message and bumps the thread in one transaction, so the inbox
// order never disagrees with what is actually in the thread.
func (s *Store) Append(ctx context.Context, threadID, senderID uuid.UUID, body string, redacted bool) (Message, error) {
	var m Message
	err := postgres.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `
			INSERT INTO messages (thread_id, sender_id, body, redacted)
			VALUES ($1, $2, $3, $4)
			RETURNING id, thread_id, sender_id, body, redacted, created_at`,
			threadID, senderID, body, redacted).
			Scan(&m.ID, &m.ThreadID, &m.SenderID, &m.Body, &m.Redacted, &m.CreatedAt)
		if err != nil {
			return fmt.Errorf("insert message: %w", err)
		}

		// The sender has necessarily read their own thread up to now.
		_, err = tx.Exec(ctx, `
			UPDATE threads SET
				last_message_at = $2,
				flagged = flagged OR $3,
				guest_read_at = CASE WHEN guest_id = $4 THEN $2 ELSE guest_read_at END,
				host_read_at  = CASE WHEN host_id  = $4 THEN $2 ELSE host_read_at  END
			WHERE id = $1`, threadID, m.CreatedAt, redacted, senderID)
		if err != nil {
			return fmt.Errorf("bump thread: %w", err)
		}
		return nil
	})
	return m, err
}

// Page returns messages newest first. before is the id of the oldest message
// the client already holds; uuid.Nil starts from the newest.
func (s *Store) Page(ctx context.Context, threadID, before uuid.UUID, limit int32) ([]Message, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, thread_id, sender_id, body, redacted, created_at
		FROM messages
		WHERE thread_id = $1
		  AND ($2::uuid = '00000000-0000-0000-0000-000000000000' OR id < $2)
		ORDER BY id DESC
		LIMIT $3`, threadID, before, limit)
	if err != nil {
		return nil, fmt.Errorf("page messages: %w", err)
	}
	defer rows.Close()

	out := []Message{}
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.ID, &m.ThreadID, &m.SenderID, &m.Body, &m.Redacted, &m.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan message: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) MarkRead(ctx context.Context, threadID, userID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE threads SET
			guest_read_at = CASE WHEN guest_id = $2 THEN now() ELSE guest_read_at END,
			host_read_at  = CASE WHEN host_id  = $2 THEN now() ELSE host_read_at  END
		WHERE id = $1 AND (guest_id = $2 OR host_id = $2)`, threadID, userID)
	if err != nil {
		return fmt.Errorf("mark read: %w", err)
	}
	return nil
}

// HasConfirmedBooking decides whether contact details may flow. A live, paid
// booking between these two people on this space means they genuinely need to
// coordinate.
func (s *Store) HasConfirmedBooking(ctx context.Context, spaceID, guestID uuid.UUID) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM bookings
			WHERE space_id = $1 AND guest_id = $2
			  AND status IN ('confirmed', 'active', 'completed')
		)`, spaceID, guestID).Scan(&ok)
	if err != nil {
		return false, fmt.Errorf("check booking: %w", err)
	}
	return ok, nil
}
