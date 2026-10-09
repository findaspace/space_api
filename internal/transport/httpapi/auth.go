package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/samnart1/space_api/internal/identity"
	"github.com/samnart1/space_api/internal/platform/telemetry"
)

type Principal struct {
	UserID    uuid.UUID
	SessionID uuid.UUID
	Roles     []identity.Role
}

func (p Principal) Has(r identity.Role) bool {
	for _, got := range p.Roles {
		if got == r {
			return true
		}
	}
	return false
}

type principalKey struct{}

func PrincipalFrom(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(Principal)
	return p, ok
}

type sessionAuthorizer interface {
	AuthorizeSession(context.Context, uuid.UUID, uuid.UUID) (identity.User, error)
}

// RequireAuth verifies the signature, then loads the live session and roles.
func RequireAuth(tokens *identity.TokenIssuer, sessions sessionAuthorizer) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "private, no-store")
			raw, ok := bearerToken(r)
			if !ok {
				unauthorized(w, r, "missing bearer token")
				return
			}

			claims, err := tokens.Verify(raw)
			if err != nil {
				unauthorized(w, r, "invalid or expired token")
				return
			}

			userID, err := uuid.Parse(claims.Subject)
			if err != nil {
				unauthorized(w, r, "malformed token subject")
				return
			}
			sessionID, err := uuid.Parse(claims.ID)
			if err != nil {
				unauthorized(w, r, "malformed token id")
				return
			}

			user, err := sessions.AuthorizeSession(r.Context(), userID, sessionID)
			if err != nil {
				switch {
				case errors.Is(err, identity.ErrSessionInvalid):
					unauthorized(w, r, "session revoked or expired")
				case errors.Is(err, identity.ErrUserSuspended):
					WriteProblem(w, r, Problem{Status: http.StatusForbidden, Title: "account unavailable"})
				default:
					telemetry.Logger(r.Context()).Error("session authorization failed", slog.Any("error", err))
					w.Header().Set("Retry-After", "5")
					WriteProblem(w, r, Problem{Status: http.StatusServiceUnavailable, Title: "session verification unavailable"})
				}
				return
			}
			p := Principal{UserID: userID, SessionID: sessionID, Roles: user.Roles}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalKey{}, p)))
		})
	}
}

// RequireRole must be chained after RequireAuth.
func RequireRole(roles ...identity.Role) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p, ok := PrincipalFrom(r.Context())
			if !ok {
				unauthorized(w, r, "missing bearer token")
				return
			}
			for _, role := range roles {
				if p.Has(role) {
					next.ServeHTTP(w, r)
					return
				}
			}
			WriteProblem(w, r, Problem{
				Status: http.StatusForbidden,
				Title:  "insufficient role",
			})
		})
	}
}

func bearerToken(r *http.Request) (string, bool) {
	h := r.Header.Get("Authorization")
	if len(h) < 7 || !strings.EqualFold(h[:7], "bearer ") {
		return "", false
	}
	tok := strings.TrimSpace(h[7:])
	return tok, tok != ""
}

func unauthorized(w http.ResponseWriter, r *http.Request, detail string) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="space_api"`)
	WriteProblem(w, r, Problem{
		Status: http.StatusUnauthorized,
		Title:  "authentication required",
		Detail: detail,
	})
}
