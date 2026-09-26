package httpapi

import (
	"context"
	"crypto/subtle"
	"net"
	"net/http"
	"strings"
)

// bffHeader carries the secret space_web shares with this API. A request that
// presents it came from the web app's servers, which have already read the
// real client's address and forwarded it in X-Forwarded-For.
const bffHeader = "X-Space-BFF"

type bffKey struct{}

// TrustBFF marks requests that prove they came from space_web.
//
// Every browser request reaches this API through the web app's servers, so
// without this every user shares the web app's IP address. The per-IP OTP
// limit of fifteen codes would become fifteen codes for the whole country, and
// the auth rate limiter ten sign-ins a minute for everyone combined.
//
// X-Forwarded-For alone cannot be trusted: any client can send it. The web app
// runs on a platform whose outbound addresses change constantly and cannot be
// allowlisted, so a shared secret is what distinguishes it from a stranger.
func TrustBFF(secret string) Middleware {
	want := []byte(secret)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got := r.Header.Get(bffHeader)

			// Removed whether it matched or not, so nothing further down can
			// read it, log it, or echo it back.
			r.Header.Del(bffHeader)

			// Constant time, so the response timing does not reveal how many
			// leading characters of a guess were right.
			if len(want) > 0 && got != "" && subtle.ConstantTimeCompare([]byte(got), want) == 1 {
				r = r.WithContext(context.WithValue(r.Context(), bffKey{}, true))
			}
			next.ServeHTTP(w, r)
		})
	}
}

func fromBFF(r *http.Request) bool {
	v, _ := r.Context().Value(bffKey{}).(bool)
	return v
}

// ClientIP returns the caller's address. X-Forwarded-For is consulted only
// when the deployment sits behind its own proxy, or when the request proved it
// came from space_web. Anyone else's X-Forwarded-For is ignored, since
// honouring it would let every client choose its own rate-limit bucket.
func ClientIP(r *http.Request, trustProxy bool) string {
	if trustProxy || fromBFF(r) {
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
