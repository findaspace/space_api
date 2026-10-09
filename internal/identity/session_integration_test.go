package identity

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"testing"
)

func TestIntegrationLiveSessionRevocationAndSuspension(t *testing.T) {
	store, pool := newGraceStore(t)
	hash := seedSession(t, pool)
	ctx := context.Background()
	var userID, sessionID uuid.UUID
	if err := pool.QueryRow(ctx, "SELECT user_id,id FROM sessions WHERE refresh_hash=$1", hash).Scan(&userID, &sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "INSERT INTO role_grants(user_id,role) VALUES($1,'admin')", userID); err != nil {
		t.Fatal(err)
	}
	user, err := store.AuthorizeSession(ctx, userID, sessionID)
	if err != nil || !user.Has(RoleAdmin) {
		t.Fatalf("live roles: %+v %v", user, err)
	}
	if _, err := pool.Exec(ctx, "DELETE FROM role_grants WHERE user_id=$1", userID); err != nil {
		t.Fatal(err)
	}
	user, err = store.AuthorizeSession(ctx, userID, sessionID)
	if err != nil || user.Has(RoleAdmin) {
		t.Fatalf("removed role: %+v %v", user, err)
	}
	if _, err := pool.Exec(ctx, "UPDATE users SET status='suspended' WHERE id=$1", userID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AuthorizeSession(ctx, userID, sessionID); !errors.Is(err, ErrUserSuspended) {
		t.Fatalf("suspension: %v", err)
	}
	if _, err := pool.Exec(ctx, "UPDATE users SET status='active' WHERE id=$1", userID); err != nil {
		t.Fatal(err)
	}
	if err := store.RevokeByRefreshHash(ctx, hash, "test logout"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AuthorizeSession(ctx, userID, sessionID); !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("logout: %v", err)
	}
	if _, err := store.AuthorizeSession(ctx, uuid.New(), sessionID); !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("other user session: %v", err)
	}
}
