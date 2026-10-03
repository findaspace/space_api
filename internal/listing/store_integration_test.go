package listing_test

import (
	"context"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samnart1/space_api/internal/listing"
	"github.com/samnart1/space_api/internal/media"
	"os"
	"testing"
	"time"
)

type roles struct{}

func (roles) GrantHost(context.Context, uuid.UUID) error { return nil }

type photos struct{}

func (photos) CountReady(context.Context, uuid.UUID) (int, error) { return 1, nil }
func (photos) HasVideo(context.Context, uuid.UUID) (bool, error)  { return false, nil }
func TestIntegrationSpaceLifecycleAndPhotoGuard(t *testing.T) {
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
	var host, other uuid.UUID
	for i, id := range []*uuid.UUID{&host, &other} {
		if err := pool.QueryRow(ctx, "INSERT INTO users(phone_e164) VALUES($1) RETURNING id", fmt.Sprintf("+23324%07d", (time.Now().UnixNano()+int64(i))%10000000)).Scan(id); err != nil {
			t.Fatal(err)
		}
	}
	defer pool.Exec(ctx, "DELETE FROM users WHERE id=ANY($1)", []uuid.UUID{host, other})
	svc := listing.NewService(listing.NewStore(pool), roles{}, photos{}, nil)
	sp, err := svc.Create(ctx, listing.CreateInput{HostID: host, Type: listing.TypeFootballPitch, Mode: listing.ModeFlexible, PricePeriod: listing.PeriodHour, Title: "A floodlit pitch in Madina", BasePriceMinor: 15000, MaxOccupancy: 22, Location: listing.Location{Country: "GH", Locality: "Madina", Latitude: 5.6, Longitude: -0.2}, Attributes: map[string]any{"floodlights": true}})
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Exec(ctx, "DELETE FROM spaces WHERE id=$1", sp.ID)
	if _, err := svc.GetForHost(ctx, sp.ID, other); !errors.Is(err, listing.ErrNotFound) {
		t.Fatalf("ownership leak: %v", err)
	}
	title := "An updated football pitch"
	day := listing.PeriodDay
	if _, err := svc.Update(ctx, sp.ID, other, listing.Patch{Title: &title}); !errors.Is(err, listing.ErrNotFound) {
		t.Fatalf("cross-host patch: %v", err)
	}
	sp, err = svc.Update(ctx, sp.ID, host, listing.Patch{Title: &title, PricePeriod: &day})
	if err != nil || sp.Title != title || sp.PricePeriod != day {
		t.Fatalf("patch: %+v %v", sp, err)
	}
	if _, err := svc.Submit(ctx, sp.ID, host); err != nil {
		t.Fatal(err)
	}
	sp, err = svc.Update(ctx, sp.ID, host, listing.Patch{Title: &title})
	if err != nil || sp.Status != listing.StatusDraft {
		t.Fatalf("review edit must become draft: %+v %v", sp, err)
	}
	if _, err := svc.Submit(ctx, sp.ID, host); err != nil {
		t.Fatal(err)
	}
	var mid uuid.UUID
	if err := pool.QueryRow(ctx, "INSERT INTO space_media(space_id,object_key,content_type,status,width,height,ready_at) VALUES($1,$2,'image/jpeg','ready',20,20,now()) RETURNING id", sp.ID, uuid.NewString()+".jpg").Scan(&mid); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Publish(ctx, sp.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Update(ctx, sp.ID, host, listing.Patch{Title: &title}); !errors.Is(err, listing.ErrBadTransition) {
		t.Fatalf("published edit accepted: %v", err)
	}
	ms := media.NewStore(pool)
	if _, err := ms.Delete(ctx, mid, sp.ID); !errors.Is(err, media.ErrLastPhoto) {
		t.Fatalf("last live photo removed: %v", err)
	}
	if _, err := svc.Withdraw(ctx, sp.ID, host); err != nil {
		t.Fatal(err)
	}
	if _, err := ms.Delete(ctx, mid, sp.ID); err != nil {
		t.Fatal(err)
	}
}
