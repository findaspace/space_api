package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/samnart1/space_api/internal/identity"
)

type liveSessions struct {
	user identity.User
	err  error
}

func (s liveSessions) AuthorizeSession(context.Context, uuid.UUID, uuid.UUID) (identity.User, error) {
	return s.user, s.err
}

func TestAuthorizationUsesLiveRolesAndFailsClosed(t *testing.T) {
	issuer, err := identity.NewTokenIssuer(make([]byte, 32), "test", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	user := identity.User{ID: uuid.New(), Roles: []identity.Role{identity.RoleAdmin}}
	token, _, err := issuer.Issue(user, uuid.New(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name     string
		sessions liveSessions
		want     int
	}{
		{"removed role", liveSessions{user: identity.User{Roles: []identity.Role{identity.RoleGuest}}}, 403},
		{"live admin", liveSessions{user: user}, 204},
		{"revoked session", liveSessions{err: identity.ErrSessionInvalid}, 401},
		{"suspended account", liveSessions{err: identity.ErrUserSuspended}, 403},
		{"database outage", liveSessions{err: errors.New("unavailable")}, 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := RequireAuth(issuer, tc.sessions)(RequireRole(identity.RoleAdmin)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })))
			req := httptest.NewRequest("GET", "/private", nil)
			req.Header.Set("Authorization", "Bearer "+token)
			res := httptest.NewRecorder()
			h.ServeHTTP(res, req)
			if res.Code != tc.want {
				t.Fatalf("status %d, want %d", res.Code, tc.want)
			}
			if res.Code == 204 && res.Header().Get("Cache-Control") != "private, no-store" {
				t.Fatal("private response can be cached")
			}
		})
	}
}
