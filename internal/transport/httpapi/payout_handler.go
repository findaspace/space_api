package httpapi

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/samnart1/space_api/internal/payout"
	"github.com/samnart1/space_api/internal/platform/telemetry"
)

type payoutHandler struct{ svc *payout.Service }

func (h payoutHandler) saveAccount(w http.ResponseWriter, r *http.Request) {
	p, ok := PrincipalFrom(r.Context())
	if !ok {
		unauthorized(w, r, "missing bearer token")
		return
	}

	var body struct {
		Channel       string `json:"channel"`
		BankCode      string `json:"bank_code"`
		AccountNumber string `json:"account_number"`
		AccountName   string `json:"account_name"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}

	channel := payout.Channel(body.Channel)
	if channel == "" {
		channel = payout.ChannelMobileMoney
	}

	account, err := h.svc.SaveAccount(r.Context(), p.UserID, payout.AccountInput{
		Channel: channel, BankCode: body.BankCode,
		AccountNumber: body.AccountNumber, AccountName: body.AccountName,
	})
	if err != nil {
		writePayoutError(w, r, err)
		return
	}

	WriteJSON(w, r, http.StatusOK, map[string]any{
		"id":             account.ID.String(),
		"channel":        account.Channel,
		"bank_code":      account.BankCode,
		"account_number": account.AccountNumber,
		"account_name":   account.AccountName,
		"verified":       account.VerifiedAt != nil,
	})
}

func (h payoutHandler) balance(w http.ResponseWriter, r *http.Request) {
	p, ok := PrincipalFrom(r.Context())
	if !ok {
		unauthorized(w, r, "missing bearer token")
		return
	}
	b, err := h.svc.Balance(r.Context(), p.UserID)
	if err != nil {
		writePayoutError(w, r, err)
		return
	}
	WriteJSON(w, r, http.StatusOK, b)
}

func (h payoutHandler) request(w http.ResponseWriter, r *http.Request) {
	p, ok := PrincipalFrom(r.Context())
	if !ok {
		unauthorized(w, r, "missing bearer token")
		return
	}

	var body struct {
		AmountMinor int64 `json:"amount_minor"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}

	out, err := h.svc.Request(r.Context(), p.UserID, body.AmountMinor)
	if err != nil {
		writePayoutError(w, r, err)
		return
	}
	WriteJSON(w, r, http.StatusAccepted, toPayoutResponse(out))
}

func (h payoutHandler) list(w http.ResponseWriter, r *http.Request) {
	p, ok := PrincipalFrom(r.Context())
	if !ok {
		unauthorized(w, r, "missing bearer token")
		return
	}

	items, err := h.svc.List(r.Context(), p.UserID,
		clampQueryInt(r, "limit", 20, 1, 100), clampQueryInt(r, "offset", 0, 0, 100000))
	if err != nil {
		writePayoutError(w, r, err)
		return
	}

	out := make([]map[string]any, 0, len(items))
	for _, p := range items {
		out = append(out, toPayoutResponse(p))
	}
	WriteJSON(w, r, http.StatusOK, map[string]any{"payouts": out})
}

func toPayoutResponse(p payout.Payout) map[string]any {
	return map[string]any{
		"id":             p.ID.String(),
		"reference":      p.Reference,
		"status":         p.Status,
		"currency":       p.Currency,
		"amount_minor":   p.AmountMinor,
		"failure_reason": p.FailureReason,
		"settled_at":     p.SettledAt,
		"created_at":     p.CreatedAt,
	}
}

func writePayoutError(w http.ResponseWriter, r *http.Request, err error) {
	field := func(f, code, detail string) Problem {
		return Problem{Status: http.StatusUnprocessableEntity, Title: "invalid payout",
			Errors: []FieldError{{Field: f, Code: code, Detail: detail}}}
	}

	switch {
	case errors.Is(err, payout.ErrNoAccount):
		WriteProblem(w, r, Problem{
			Status: http.StatusConflict, Title: "no payout account",
			Detail: "add a mobile money account before withdrawing",
		})
	case errors.Is(err, payout.ErrInProgress):
		WriteProblem(w, r, Problem{
			Status: http.StatusConflict, Title: "payout already in progress",
		})
	case errors.Is(err, payout.ErrInsufficient):
		WriteProblem(w, r, field("amount_minor", "insufficient", "more than your withdrawable balance"))
	case errors.Is(err, payout.ErrBelowMinimum):
		WriteProblem(w, r, field("amount_minor", "min", "the minimum payout is GHS 1"))
	case errors.Is(err, payout.ErrAboveMaximum):
		WriteProblem(w, r, field("amount_minor", "max", "the maximum payout is GHS 100,000"))
	case errors.Is(err, payout.ErrUnknownNetwork):
		WriteProblem(w, r, field("bank_code", "unknown", "use MTN, VOD or ATL"))
	case errors.Is(err, payout.ErrNotFound):
		WriteProblem(w, r, Problem{Status: http.StatusNotFound, Title: "not found"})
	default:
		telemetry.Logger(r.Context()).Error("unhandled payout error", slog.Any("error", err))
		WriteProblem(w, r, Problem{Status: http.StatusInternalServerError, Title: "internal server error"})
	}
}
