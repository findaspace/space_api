package httpapi

import (
	"context"
	"net"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/samnart1/space_api/internal/identity"
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

// RequireAuth validates the bearer token and does not touch the database. That
// is the trade JWTs buy: no per-request query, at the cost of a suspended user
// keeping access until their access token expires. Keep the TTL short.
func RequireAuth(tokens *identity.TokenIssuer) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

			p := Principal{UserID: userID, SessionID: sessionID, Roles: claims.Roles}
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

// ClientIP returns the caller's address. X-Forwarded-For is only consulted
// when the deployment is actually behind a proxy, because any client can send
// that header and otherwise every rate limit becomes trivially bypassable.
func ClientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			if first, _, ok := strings.Cut(xff, ","); ok {
				return strings.TrimSpace(first)
			}
			return strings.TrimSpace(xff)
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
