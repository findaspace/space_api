package trust

import (
	"context"
	"path"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/samnart1/space_api/internal/platform/postgres"
)

// DeleteAccount removes profile data and access atomically. Transactional
// records remain pseudonymous; deleting those would break the booking ledger.
// Storage cleanup waits for issued upload URLs and in-flight transcodes to
// expire, and is durable across worker restarts.
func (s *Store) DeleteAccount(ctx context.Context, actor uuid.UUID) error {
	return postgres.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		var phone *string
		if err := tx.QueryRow(ctx, "SELECT phone_e164 FROM users WHERE id=$1 AND status='active' FOR UPDATE", actor).Scan(&phone); err != nil {
			return err
		}
		var obligations bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM role_grants WHERE user_id=$1 AND role IN ('admin','support')) OR
   EXISTS(SELECT 1 FROM bookings WHERE (host_id=$1 OR guest_id=$1) AND status IN ('held','approved','confirmed','active'))`, actor).Scan(&obligations); err != nil {
			return err
		}
		if obligations {
			return ErrObligations
		}
		// Lock owned spaces so concurrent uploads cannot slip past this inventory.
		rows, err := tx.Query(ctx, "SELECT id FROM spaces WHERE host_id=$1 ORDER BY id FOR UPDATE", actor)
		if err != nil {
			return err
		}
		for rows.Next() {
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		keys := []string{}
		rows, err = tx.Query(ctx, `SELECT m.object_key,COALESCE(m.card_key,'') FROM space_media m JOIN spaces s ON s.id=m.space_id WHERE s.host_id=$1`, actor)
		if err != nil {
			return err
		}
		for rows.Next() {
			var raw, card string
			if err = rows.Scan(&raw, &card); err != nil {
				rows.Close()
				return err
			}
			keys = append(keys, raw, card, strings.TrimSuffix(raw, path.Ext(raw))+"-card.jpg")
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		rows, err = tx.Query(ctx, `SELECT v.upload_key,COALESCE(v.video_key,''),COALESCE(v.poster_key,'') FROM space_videos v JOIN spaces s ON s.id=v.space_id WHERE s.host_id=$1`, actor)
		if err != nil {
			return err
		}
		for rows.Next() {
			var raw, video, poster string
			if err = rows.Scan(&raw, &video, &poster); err != nil {
				rows.Close()
				return err
			}
			base := strings.TrimSuffix(strings.TrimSuffix(raw, path.Ext(raw)), "-raw")
			keys = append(keys, raw, video, poster, base+".mp4", base+"-poster.jpg")
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, key := range keys {
			if key != "" {
				if _, err = tx.Exec(ctx, "INSERT INTO storage_deletions(object_key) VALUES($1) ON CONFLICT DO NOTHING", key); err != nil {
					return err
				}
			}
		}
		commands := []string{
			"UPDATE spaces SET status='archived',title='Removed listing',description='',landmark='',digital_address='' WHERE host_id=$1",
			"DELETE FROM space_media WHERE space_id IN (SELECT id FROM spaces WHERE host_id=$1)",
			"DELETE FROM space_videos WHERE space_id IN (SELECT id FROM spaces WHERE host_id=$1)",
			"UPDATE messages SET body='[Message removed after account deletion]',redacted=true WHERE sender_id=$1",
			"UPDATE reviews SET body='' WHERE author_id=$1",
			"UPDATE listing_reports SET details='' WHERE reporter_id=$1",
			"DELETE FROM notifications WHERE user_id=$1",
			"DELETE FROM notification_preferences WHERE user_id=$1",
			"DELETE FROM external_identities WHERE user_id=$1",
			"DELETE FROM role_grants WHERE user_id=$1",
			"UPDATE sessions SET revoked_at=COALESCE(revoked_at,now()),revoked_reason='account deleted',user_agent='',request_ip='' WHERE user_id=$1",
			"UPDATE users SET status='deleted',phone_e164=NULL,email=NULL,display_name='Deleted account',phone_verified_at=NULL WHERE id=$1",
		}
		for _, query := range commands {
			if _, err = tx.Exec(ctx, query, actor); err != nil {
				return err
			}
		}
		if phone != nil {
			if _, err = tx.Exec(ctx, "DELETE FROM otp_challenges WHERE phone_e164=$1", *phone); err != nil {
				return err
			}
		}
		return audit(ctx, tx, actor, "account_deleted", actor)
	})
}

type ObjectDeleter interface {
	Delete(context.Context, string) error
}
type Cleaner struct {
	store   *Store
	objects ObjectDeleter
}

func NewCleaner(store *Store, objects ObjectDeleter) *Cleaner {
	return &Cleaner{store: store, objects: objects}
}

// A row lock prevents overlapping sweepers. A failed object delete leaves the
// row pending and returns an error so River retries rather than losing work.
func (c *Cleaner) Sweep(ctx context.Context) error {
	for range 100 {
		found := false
		err := postgres.InTx(ctx, c.store.pool, func(tx pgx.Tx) error {
			var key string
			err := tx.QueryRow(ctx, "SELECT object_key FROM storage_deletions WHERE deleted_at IS NULL AND not_before<=now() ORDER BY not_before,object_key FOR UPDATE SKIP LOCKED LIMIT 1").Scan(&key)
			if err == pgx.ErrNoRows {
				return nil
			}
			if err != nil {
				return err
			}
			found = true
			if err = c.objects.Delete(ctx, key); err != nil {
				return err
			}
			_, err = tx.Exec(ctx, "UPDATE storage_deletions SET deleted_at=now() WHERE object_key=$1", key)
			return err
		})
		if err != nil {
			return err
		}
		if !found {
			return nil
		}
	}
	return nil
}
