package httpapi

import (
	"errors"
	"github.com/google/uuid"
	"github.com/samnart1/space_api/internal/identity"
	"net/http"
)

type externalHandler struct {
	verifier *identity.ExternalVerifier
	users    *identity.Service
}

func (h externalHandler) login(w http.ResponseWriter, r *http.Request) { h.exchange(w, r, uuid.Nil) }
func (h externalHandler) link(w http.ResponseWriter, r *http.Request) {
	p, ok := PrincipalFrom(r.Context())
	if !ok {
		unauthorized(w, r, "missing session")
		return
	}
	if !h.recent(w, r, p) {
		return
	}
	h.exchange(w, r, p.UserID)
}
func (h externalHandler) exchange(w http.ResponseWriter, r *http.Request, target uuid.UUID) {
	w.Header().Set("Cache-Control", "private, no-store")
	if !fromBFF(r) || h.verifier == nil {
		WriteError(w, r, 403, "sign-in must start on Findaspace")
		return
	}
	var body struct {
		Provider string `json:"provider"`
		IDToken  string `json:"id_token"`
		Nonce    string `json:"nonce"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	who, err := h.verifier.Verify(r.Context(), body.Provider, body.IDToken, body.Nonce)
	if err != nil {
		unauthorized(w, r, "provider sign-in could not be verified")
		return
	}
	pair, err := h.users.ExternalLogin(r.Context(), who, body.IDToken, target, r.UserAgent(), ClientIP(r, false))
	if errors.Is(err, identity.ErrIdentityConflict) {
		WriteError(w, r, 409, "this sign-in method is already connected to another account")
		return
	}
	if err != nil {
		writeIdentityError(w, r, err)
		return
	}
	WriteJSON(w, r, 200, toTokenResponse(pair))
}
func (h externalHandler) methods(w http.ResponseWriter, r *http.Request) {
	p, _ := PrincipalFrom(r.Context())
	methods, err := h.users.SignInMethods(r.Context(), p.UserID)
	if err != nil {
		WriteError(w, r, 503, "sign-in methods could not be loaded")
		return
	}
	WriteJSON(w, r, 200, map[string]any{"methods": methods})
}

func (h externalHandler) phone(w http.ResponseWriter, r *http.Request) {
	p, _ := PrincipalFrom(r.Context())
	if !h.recent(w, r, p) {
		return
	}
	var body struct {
		Phone string `json:"phone"`
		Code  string `json:"code"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	user, err := h.users.LinkPhone(r.Context(), p.UserID, body.Phone, body.Code)
	if errors.Is(err, identity.ErrIdentityConflict) {
		WriteError(w, r, 409, "this phone belongs to another account; sign in to that account to connect your sign-in methods")
		return
	}
	if err != nil {
		writeIdentityError(w, r, err)
		return
	}
	WriteJSON(w, r, 200, toUserResponse(user))
}

func (h externalHandler) recent(w http.ResponseWriter, r *http.Request, p Principal) bool {
	recent, err := h.users.RecentSession(r.Context(), p.UserID, p.SessionID)
	if err != nil {
		WriteError(w, r, 503, "could not check sign-in freshness")
		return false
	}
	if !recent {
		WriteProblem(w, r, Problem{Type: "urn:findaspace:reauthentication-required", Status: 403, Title: "Sign in again before connecting a sign-in method"})
		return false
	}
	return true
}
