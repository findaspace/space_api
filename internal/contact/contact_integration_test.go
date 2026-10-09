package contact

import (
	"context"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"os"
	"sync"
	"testing"
	"time"
)

func TestIntegrationRevealCapAndRepeat(t *testing.T) {
	url := os.Getenv("SPACE_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("SPACE_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	ids := make([]uuid.UUID, 2)
	for i := range ids {
		if err := pool.QueryRow(ctx, "INSERT INTO users(phone_e164,phone_verified_at) VALUES($1,now()) RETURNING id", fmt.Sprintf("+23355%07d", (time.Now().UnixNano()+int64(i))%10000000)).Scan(&ids[i]); err != nil {
			t.Fatal(err)
		}
	}
	defer func() { _, _ = pool.Exec(ctx, "DELETE FROM users WHERE id=ANY($1)", ids) }()
	spaces := make([]uuid.UUID, 2)
	for i := range spaces {
		if err := pool.QueryRow(ctx, "INSERT INTO spaces(host_id,slug,space_type,rental_mode,status,title,base_price_minor,price_period,latitude,longitude,locality,published_at) VALUES($1,$2,'football_pitch','flexible','published','A local sports pitch',10000,'hour',5.6,-0.2,'Madina',now()) RETURNING id", ids[0], uuid.NewString()).Scan(&spaces[i]); err != nil {
			t.Fatal(err)
		}
	}
	defer func() { _, _ = pool.Exec(ctx, "DELETE FROM spaces WHERE id=ANY($1)", spaces) }()
	defer func() { _, _ = pool.Exec(ctx, "DELETE FROM contact_reveals WHERE viewer_id=$1", ids[1]) }()
	svc := NewService(pool, 1)
	var wg sync.WaitGroup
	out := make(chan struct {
		id  uuid.UUID
		err error
	}, 2)
	for _, id := range spaces {
		wg.Add(1)
		go func(id uuid.UUID) {
			defer wg.Done()
			_, err := svc.Reveal(ctx, ids[1], id)
			out <- struct {
				id  uuid.UUID
				err error
			}{id, err}
		}(id)
	}
	wg.Wait()
	close(out)
	var revealed uuid.UUID
	success := 0
	for item := range out {
		if item.err == nil {
			success++
			revealed = item.id
		} else if !errors.Is(item.err, ErrTooMany) {
			t.Fatal(item.err)
		}
	}
	if success != 1 {
		t.Fatalf("concurrent reveal cap exceeded: %d", success)
	}
	if _, err := svc.Reveal(ctx, ids[1], revealed); err != nil {
		t.Fatalf("repeat consumed another allowance: %v", err)
	}
}
