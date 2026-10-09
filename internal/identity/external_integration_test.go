package identity

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"testing"
	"time"
)

func TestIntegrationExternalIdentityNeverMergesByEmailOrReassigns(t *testing.T) {
	store, pool := newGraceStore(t)
	ctx := context.Background()
	ids := []uuid.UUID{}
	t.Cleanup(func() {
		for _, id := range ids {
			_, _ = pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", id)
		}
	})
	who := ExternalIdentity{Provider: "google", Issuer: "https://accounts.google.com", Subject: uuid.NewString(), Email: "same@example.com", ExpiresAt: time.Now().Add(time.Hour)}
	raw := uuid.NewString()
	first, _, err := store.ExternalLogin(ctx, who, raw, uuid.Nil, []byte(uuid.NewString()), time.Hour, "", "")
	if err != nil {
		t.Fatal(err)
	}
	ids = append(ids, first.ID)
	if first.Phone != "" {
		t.Fatal("provider signup invented a phone")
	}
	if _, _, err := store.ExternalLogin(ctx, who, raw, uuid.Nil, []byte(uuid.NewString()), time.Hour, "", ""); !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("token replay: %v", err)
	}
	again, _, err := store.ExternalLogin(ctx, who, uuid.NewString(), uuid.Nil, []byte(uuid.NewString()), time.Hour, "", "")
	if err != nil || again.ID != first.ID {
		t.Fatalf("duplicate identity: %+v %v", again, err)
	}
	other := who
	other.Subject = uuid.NewString()
	second, _, err := store.ExternalLogin(ctx, other, uuid.NewString(), uuid.Nil, []byte(uuid.NewString()), time.Hour, "", "")
	if err != nil {
		t.Fatal(err)
	}
	ids = append(ids, second.ID)
	if second.ID == first.ID {
		t.Fatal("accounts merged by matching email")
	}
	if _, _, err := store.ExternalLogin(ctx, who, uuid.NewString(), second.ID, []byte(uuid.NewString()), time.Hour, "", ""); !errors.Is(err, ErrIdentityConflict) {
		t.Fatalf("identity reassigned: %v", err)
	}
	microsoft := who
	microsoft.Provider = "microsoft"
	microsoft.Issuer = "https://login.microsoftonline.com/test/v2.0"
	microsoft.Subject = uuid.NewString()
	linked, _, err := store.ExternalLogin(ctx, microsoft, uuid.NewString(), first.ID, []byte(uuid.NewString()), time.Hour, "", "")
	if err != nil || linked.ID != first.ID {
		t.Fatalf("explicit link failed: %v", err)
	}
	var same int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM external_identities WHERE user_id=$1", first.ID).Scan(&same); err != nil || same != 2 {
		t.Fatalf("methods %d: %v", same, err)
	}
}

func TestIntegrationProviderAccountPhoneProofAndFreshLinking(t *testing.T) {
	store, pool := newGraceStore(t)
	ctx := context.Background()
	hasher, err := NewHasher(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(store, hasher, nil, nil, nil, ServiceConfig{})
	who := ExternalIdentity{Provider: "apple", Issuer: "https://appleid.apple.com", Subject: uuid.NewString(), ExpiresAt: time.Now().Add(time.Hour)}
	user, session, err := store.ExternalLogin(ctx, who, uuid.NewString(), uuid.Nil, []byte(uuid.NewString()), time.Hour, "", "")
	if err != nil {
		t.Fatal(err)
	}
	var spaceID uuid.UUID
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DELETE FROM spaces WHERE id=$1", spaceID)
		_, _ = pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", user.ID)
	})
	recent, err := service.RecentSession(ctx, user.ID, session.ID)
	if err != nil || !recent {
		t.Fatalf("fresh session: %v %v", recent, err)
	}
	if _, err := pool.Exec(ctx, "UPDATE sessions SET created_at=now()-interval '20 minutes' WHERE id=$1", session.ID); err != nil {
		t.Fatal(err)
	}
	recent, err = service.RecentSession(ctx, user.ID, session.ID)
	if err != nil || recent {
		t.Fatalf("old session accepted for linking: %v %v", recent, err)
	}
	if err := pool.QueryRow(ctx, "INSERT INTO spaces(host_id,slug,space_type,rental_mode,title,base_price_minor,price_period,latitude,longitude,locality) VALUES($1,$2,'football_pitch','flexible','A local sports pitch',10000,'hour',5.6,-0.2,'Madina') RETURNING id", user.ID, uuid.NewString()).Scan(&spaceID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "UPDATE spaces SET status='in_review' WHERE id=$1", spaceID); err == nil {
		t.Fatal("unverified provider host submitted a listing")
	}
	phone := "+233244123456"
	var existingID uuid.UUID
	if err := pool.QueryRow(ctx, "INSERT INTO users(phone_e164,phone_verified_at) VALUES($1,now()) RETURNING id", phone).Scan(&existingID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", existingID) })
	code := "123456"
	if _, err := store.CreateChallenge(ctx, phone, "", hasher.Hash(phone, code), time.Minute, 5); err != nil {
		t.Fatal(err)
	}
	if _, err := service.LinkPhone(ctx, user.ID, phone, code); !errors.Is(err, ErrIdentityConflict) {
		t.Fatalf("phone account was merged: %v", err)
	}
	if _, err := pool.Exec(ctx, "DELETE FROM users WHERE id=$1", existingID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateChallenge(ctx, phone, "", hasher.Hash(phone, code), time.Minute, 5); err != nil {
		t.Fatal(err)
	}
	if _, err := service.LinkPhone(ctx, user.ID, phone, "654321"); !errors.Is(err, ErrOTPMismatch) {
		t.Fatalf("unproved phone attached: %v", err)
	}
	linked, err := service.LinkPhone(ctx, user.ID, phone, code)
	if err != nil || linked.ID != user.ID || linked.PhoneVerifiedAt == nil {
		t.Fatalf("phone proof: %+v %v", linked, err)
	}
	if _, err := pool.Exec(ctx, "UPDATE spaces SET status='in_review' WHERE id=$1", spaceID); err != nil {
		t.Fatalf("verified host rejected: %v", err)
	}
	if _, err := service.LinkPhone(ctx, user.ID, phone, code); err == nil {
		t.Fatal("SMS challenge was reusable")
	}
}
