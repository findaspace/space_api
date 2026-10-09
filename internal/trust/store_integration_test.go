package trust

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestIntegrationTrustBoundaries(t *testing.T) {
	url := os.Getenv("SPACE_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("SPACE_TEST_DATABASE_URL not set; run make test-integration")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	host, guest, stranger := uuid.New(), uuid.New(), uuid.New()
	for i, id := range []uuid.UUID{host, guest, stranger} {
		if _, err = pool.Exec(ctx, "INSERT INTO users(id,phone_e164,phone_verified_at) VALUES($1,$2,now())", id, fmt.Sprintf("+23355%07d", (time.Now().UnixNano()+int64(i))%10000000)); err != nil {
			t.Fatal(err)
		}
	}
	var space, thread uuid.UUID
	if err = pool.QueryRow(ctx, "INSERT INTO spaces(host_id,slug,space_type,rental_mode,status,title,base_price_minor,price_period,latitude,longitude,locality,published_at) VALUES($1,$2,'house','term','published','Integration home',10000,'month',5.6,-0.2,'Accra',now()) RETURNING id", host, uuid.NewString()).Scan(&space); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, "INSERT INTO threads(space_id,guest_id,host_id) VALUES($1,$2,$3) RETURNING id", space, guest, host).Scan(&thread); err != nil {
		t.Fatal(err)
	}
	store := NewStore(pool)
	reportID, err := store.Report(ctx, guest, space, "suspected_scam", "Evidence")
	if err != nil {
		t.Fatal(err)
	}
	again, err := store.Report(ctx, guest, space, "other", "Duplicate")
	if err != nil || again != reportID {
		t.Fatalf("duplicate report: %s %v", again, err)
	}
	if _, err = store.Boundary(ctx, stranger, thread); !errors.Is(err, ErrNotFound) {
		t.Fatalf("nonparticipant: %v", err)
	}
	if err = store.Block(ctx, guest, thread, true); err != nil {
		t.Fatal(err)
	}
	for _, id := range []uuid.UUID{guest, host} {
		b, err := store.Boundary(ctx, id, thread)
		if err != nil || b.MessagingAllowed {
			t.Fatalf("block bypass: %+v %v", b, err)
		}
		_, err = pool.Exec(ctx, "INSERT INTO messages(thread_id,sender_id,body) VALUES($1,$2,'Blocked')", thread, id)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.ConstraintName != "messages_blocked" {
			t.Fatalf("database block: %v", err)
		}
	}
	if err = store.Block(ctx, guest, thread, false); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, "INSERT INTO messages(thread_id,sender_id,body) VALUES($1,$2,'Allowed')", thread, host); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, "UPDATE trust_audit SET action='user_unblocked' WHERE actor_id=$1", guest); err == nil {
		t.Fatal("audit was mutable")
	}
	if _, err = pool.Exec(ctx, "INSERT INTO role_grants(user_id,role) VALUES($1,'admin')", stranger); err != nil {
		t.Fatal(err)
	}
	if err = store.DeleteAccount(ctx, stranger); !errors.Is(err, ErrObligations) {
		t.Fatalf("admin closure: %v", err)
	}
	if err = store.DeleteAccount(ctx, guest); err != nil {
		t.Fatal(err)
	}
	var scrubbed bool
	if err = pool.QueryRow(ctx, "SELECT status='deleted' AND phone_e164 IS NULL AND email IS NULL FROM users WHERE id=$1", guest).Scan(&scrubbed); err != nil || !scrubbed {
		t.Fatalf("profile not scrubbed: %v", err)
	}
	if err = store.Review(ctx, stranger, reportID, "resolved", "Confirmed invalid listing", true); err != nil {
		t.Fatal(err)
	}
	var hidden bool
	if err = pool.QueryRow(ctx, "SELECT status='suspended' FROM spaces WHERE id=$1", space).Scan(&hidden); err != nil || !hidden {
		t.Fatalf("withdrawal: %v", err)
	}
	if _, err = pool.Exec(ctx, "INSERT INTO space_media(space_id,object_key,content_type) VALUES($1,$2,'image/jpeg')", space, "spaces/"+space.String()+"/test.jpg"); err != nil {
		t.Fatal(err)
	}
	if err = store.DeleteAccount(ctx, host); err != nil {
		t.Fatal(err)
	}
	var pending int
	if err = pool.QueryRow(ctx, "SELECT count(*) FROM storage_deletions WHERE object_key LIKE $1 AND deleted_at IS NULL", "spaces/"+space.String()+"/%").Scan(&pending); err != nil || pending != 2 {
		t.Fatalf("durable raw/card cleanup: %d %v", pending, err)
	}
	// Audit evidence deliberately remains. The entire database is disposable.
}
