package httpapi

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/samnart1/space_api/internal/identity"
	"github.com/samnart1/space_api/internal/platform/telemetry"
)

type authHandler struct {
	svc        *identity.Service
	trustProxy bool
}

type otpRequest struct {
	Phone string `json:"phone"`
}

type verifyRequest struct {
	Phone string `json:"phone"`
	Code  string `json:"code"`
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

type userResponse struct {
	ID          string          `json:"id"`
	Phone       string          `json:"phone"`
	DisplayName string          `json:"display_name"`
	Roles       []identity.Role `json:"roles"`
	CreatedAt   time.Time       `json:"created_at"`
}

type tokenResponse struct {
	User             userResponse `json:"user"`
	AccessToken      string       `json:"access_token"`
	TokenType        string       `json:"token_type"`
	ExpiresAt        time.Time    `json:"expires_at"`
	RefreshToken     string       `json:"refresh_token"`
	RefreshExpiresAt time.Time    `json:"refresh_expires_at"`
}

func toUserResponse(u identity.User) userResponse {
	return userResponse{
		ID:          u.ID.String(),
		Phone:       u.Phone,
		DisplayName: u.DisplayName,
		Roles:       u.Roles,
		CreatedAt:   u.CreatedAt,
	}
}

func toTokenResponse(p identity.TokenPair) tokenResponse {
	return tokenResponse{
		User:             toUserResponse(p.User),
		AccessToken:      p.AccessToken,
		TokenType:        "Bearer",
		ExpiresAt:        p.AccessExpiresAt,
		RefreshToken:     p.RefreshToken,
		RefreshExpiresAt: p.RefreshExpiresAt,
	}
}

func (h authHandler) requestOTP(w http.ResponseWriter, r *http.Request) {
	var body otpRequest
	if !decodeJSON(w, r, &body) {
		return
	}

	res, err := h.svc.RequestOTP(r.Context(), body.Phone, ClientIP(r, h.trustProxy))
	if err != nil {
		writeIdentityError(w, r, err)
		return
	}

	WriteJSON(w, r, http.StatusAccepted, map[string]any{
		"phone":      identity.MaskPhone(res.Phone),
		"expires_at": res.ExpiresAt,
	})
}

func (h authHandler) verifyOTP(w http.ResponseWriter, r *http.Request) {
	var body verifyRequest
	if !decodeJSON(w, r, &body) {
		return
	}

	pair, err := h.svc.VerifyOTP(r.Context(), body.Phone, body.Code,
		r.UserAgent(), ClientIP(r, h.trustProxy))
	if err != nil {
		writeIdentityError(w, r, err)
		return
	}
	WriteJSON(w, r, http.StatusOK, toTokenResponse(pair))
}

func (h authHandler) refresh(w http.ResponseWriter, r *http.Request) {
	var body refreshRequest
	if !decodeJSON(w, r, &body) {
		return
	}

	pair, err := h.svc.Refresh(r.Context(), body.RefreshToken,
		r.UserAgent(), ClientIP(r, h.trustProxy))
	if err != nil {
		writeIdentityError(w, r, err)
		return
	}
	WriteJSON(w, r, http.StatusOK, toTokenResponse(pair))
}

func (h authHandler) logout(w http.ResponseWriter, r *http.Request) {
	var body refreshRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	if err := h.svc.Logout(r.Context(), body.RefreshToken); err != nil {
		writeIdentityError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h authHandler) me(w http.ResponseWriter, r *http.Request) {
	p, ok := PrincipalFrom(r.Context())
	if !ok {
		unauthorized(w, r, "missing bearer token")
		return
	}

	user, err := h.svc.Me(r.Context(), p.UserID)
	if err != nil {
		writeIdentityError(w, r, err)
		return
	}
	WriteJSON(w, r, http.StatusOK, toUserResponse(user))
}

// decodeJSON rejects unknown fields so a client typo fails loudly instead of
// silently sending a field the server ignores.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()

	if err := dec.Decode(dst); err != nil {
		WriteProblem(w, r, Problem{
			Status: http.StatusBadRequest,
			Title:  "malformed request body",
			Detail: err.Error(),
		})
		return false
	}
	return true
}

func publicKeyHandler(t *identity.TokenIssuer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		WriteJSON(w, r, http.StatusOK, map[string]any{
			"keys": []map[string]string{{
				"kty": "OKP",
				"crv": "Ed25519",
				"alg": "EdDSA",
				"use": "sig",
				"kid": t.KeyId(),
				"x":   base64.RawURLEncoding.EncodeToString(t.PublicKey()),
			}},
		})
	}
}

// writeIdentityError is the one place domain errors become status codes.
func writeIdentityError(w http.ResponseWriter, r *http.Request, err error) {
	var p Problem

	switch {
	case errors.Is(err, identity.ErrInvalidPhone):
		p = Problem{Status: http.StatusUnprocessableEntity, Title: "invalid phone number",
			Errors: []FieldError{{Field: "phone", Code: "invalid"}}}

	case errors.Is(err, identity.ErrTooManyOTP):
		p = Problem{Status: http.StatusTooManyRequests, Title: "too many requests",
			Detail: "wait before requesting another code"}

	case errors.Is(err, identity.ErrOTPNotFound):
		p = Problem{Status: http.StatusUnprocessableEntity, Title: "no active code",
			Detail: "request a new code"}

	case errors.Is(err, identity.ErrOTPExpired):
		p = Problem{Status: http.StatusUnprocessableEntity, Title: "code expired"}

	case errors.Is(err, identity.ErrOTPExhausted):
		p = Problem{Status: http.StatusTooManyRequests, Title: "too many attempts",
			Detail: "request a new code"}

	case errors.Is(err, identity.ErrOTPMismatch):
		p = Problem{Status: http.StatusUnprocessableEntity, Title: "incorrect code",
			Errors: []FieldError{{Field: "code", Code: "mismatch"}}}

	case errors.Is(err, identity.ErrSessionReuse):
		p = Problem{Status: http.StatusUnauthorized, Title: "session revoked",
			Detail: "this token was already used; sign in again"}

	case errors.Is(err, identity.ErrSessionInvalid):
		p = Problem{Status: http.StatusUnauthorized, Title: "invalid session"}

	case errors.Is(err, identity.ErrUserSuspended):
		p = Problem{Status: http.StatusForbidden, Title: "account suspended"}

	case errors.Is(err, identity.ErrTooManyOTP):
		var limited *identity.RateLimitedError
		if errors.As(err, &limited) {
			w.Header().Set("Retry-After", strconv.Itoa(int(limited.RetryAfter.Seconds())))
		}
		p = Problem{Status: http.StatusTooManyRequests, Title: "too many requests",
			Detail: "wait before requesting another code"}

	default:
		// Unmapped means a bug or an infrastructure failure. The client gets
		// nothing useful; the log gets everything.
		telemetry.Logger(r.Context()).Error("unhandled identity error", slog.Any("error", err))
		p = Problem{Status: http.StatusInternalServerError, Title: "internal server error"}
	}

	WriteProblem(w, r, p)
}
