package httpapi

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/samnart1/space_api/internal/identity"
	"github.com/samnart1/space_api/internal/platform/telemetry"
	"github.com/samnart1/space_api/internal/trust"
)

type trustHandler struct {
	store *trust.Store
	users *identity.Service
}

func (h trustHandler) report(w http.ResponseWriter, r *http.Request) {
	p, id, ok := principalAndID(w, r)
	if !ok {
		return
	}
	var body struct {
		Reason  string `json:"reason"`
		Details string `json:"details"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	reportID, err := h.store.Report(r.Context(), p.UserID, id, body.Reason, body.Details)
	if err != nil {
		writeTrustError(w, r, err)
		return
	}
	WriteJSON(w, r, 201, map[string]any{"id": reportID, "status": "open"})
}
func (h trustHandler) reports(w http.ResponseWriter, r *http.Request) {
	out, err := h.store.Reports(r.Context())
	if err != nil {
		writeTrustError(w, r, err)
		return
	}
	WriteJSON(w, r, 200, map[string]any{"reports": out})
}
func (h trustHandler) review(w http.ResponseWriter, r *http.Request) {
	p, id, ok := principalAndID(w, r)
	if !ok {
		return
	}
	var body struct {
		Status   string `json:"status"`
		Note     string `json:"note"`
		Withdraw bool   `json:"withdraw_listing"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if err := h.store.Review(r.Context(), p.UserID, id, body.Status, body.Note, body.Withdraw); err != nil {
		writeTrustError(w, r, err)
		return
	}
	w.WriteHeader(204)
}
func (h trustHandler) boundary(w http.ResponseWriter, r *http.Request) {
	p, id, ok := principalAndID(w, r)
	if !ok {
		return
	}
	b, err := h.store.Boundary(r.Context(), p.UserID, id)
	if err != nil {
		writeTrustError(w, r, err)
		return
	}
	WriteJSON(w, r, 200, b)
}
func (h trustHandler) block(w http.ResponseWriter, r *http.Request) {
	p, id, ok := principalAndID(w, r)
	if !ok {
		return
	}
	if err := h.store.Block(r.Context(), p.UserID, id, r.Method == http.MethodPost); err != nil {
		writeTrustError(w, r, err)
		return
	}
	w.WriteHeader(204)
}
func (h trustHandler) deleteAccount(w http.ResponseWriter, r *http.Request) {
	p, _ := PrincipalFrom(r.Context())
	recent, err := h.users.RecentSession(r.Context(), p.UserID, p.SessionID)
	if err != nil {
		WriteError(w, r, 503, "Could not check your session. Try again.")
		return
	}
	if !recent {
		WriteProblem(w, r, Problem{Type: "urn:findaspace:reauthentication-required", Status: 403, Title: "Sign in again before deleting your account"})
		return
	}
	var body struct {
		Confirmation string `json:"confirmation"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Confirmation != "DELETE" {
		WriteError(w, r, 422, "Type DELETE to confirm.")
		return
	}
	if err := h.store.DeleteAccount(r.Context(), p.UserID); err != nil {
		writeTrustError(w, r, err)
		return
	}
	w.WriteHeader(204)
}
func writeTrustError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, trust.ErrNotFound):
		WriteError(w, r, 404, "Not found.")
	case errors.Is(err, trust.ErrInvalid):
		WriteError(w, r, 422, "Choose a reason and keep your explanation under 2,000 characters.")
	case errors.Is(err, trust.ErrObligations):
		WriteProblem(w, r, Problem{Type: "urn:findaspace:account-obligations", Status: 409, Title: "Resolve outstanding responsibilities first", Detail: "Complete or cancel active bookings, and transfer administrator or support access before deleting your account."})
	default:
		telemetry.Logger(r.Context()).Error("trust operation failed", slog.Any("error", err))
		WriteError(w, r, 500, "The change could not be saved. Try again.")
	}
}
