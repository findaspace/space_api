package httpapi

import (
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/samnart1/space_api/internal/payment"
	"github.com/samnart1/space_api/internal/platform/telemetry"
)

type paymentHandler struct{ svc *payment.Service }

func (h paymentHandler) intent(w http.ResponseWriter, r *http.Request) {
	p, bookingID, ok := principalAndID(w, r)
	if !ok {
		return
	}

	out, err := h.svc.Intent(r.Context(), bookingID, p.UserID)
	if err != nil {
		writePaymentError(w, r, err)
		return
	}

	WriteJSON(w, r, http.StatusCreated, map[string]any{
		"payment_id":        out.ID.String(),
		"reference":         out.ProviderRef,
		"access_code":       out.AccessCode,
		"authorization_url": out.AuthorizationURL,
		"amount_minor":      out.AmountMinor,
		"currency":          out.Currency,
		"status":            out.Status,
	})
}

// webhook reads the body itself. decodeJSON cannot be used here: the signature
// covers the exact bytes Paystack sent, and any parse-then-reserialize loses them.
func (h paymentHandler) webhook(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		WriteProblem(w, r, Problem{Status: http.StatusBadRequest, Title: "unreadable body"})
		return
	}

	err = h.svc.HandleWebhook(r.Context(), raw, r.Header.Get("x-paystack-signature"))

	switch {
	case err == nil:
		w.WriteHeader(http.StatusOK)

	case errors.Is(err, payment.ErrBadSignature):
		telemetry.Logger(r.Context()).Warn("rejected webhook with bad signature")
		WriteProblem(w, r, Problem{Status: http.StatusUnauthorized, Title: "invalid signature"})

	case errors.Is(err, payment.ErrNotFound):
		// An event for a payment we never created. Retrying will not help, so
		// return 200 and stop Paystack from redelivering forever.
		telemetry.Logger(r.Context()).Warn("webhook for unknown payment")
		w.WriteHeader(http.StatusOK)

	default:
		// Anything else is ours to fix. A non-200 makes Paystack retry, which
		// is exactly what we want for a transient failure.
		telemetry.Logger(r.Context()).Error("webhook processing failed", slog.Any("error", err))
		WriteProblem(w, r, Problem{Status: http.StatusInternalServerError, Title: "processing failed"})
	}
}

func writePaymentError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, payment.ErrNotFound):
		WriteProblem(w, r, Problem{Status: http.StatusNotFound, Title: "not found"})
	case errors.Is(err, payment.ErrAlreadyPaid):
		WriteProblem(w, r, Problem{Status: http.StatusConflict, Title: "already paid"})
	case errors.Is(err, payment.ErrNotPayable):
		WriteProblem(w, r, Problem{
			Status: http.StatusConflict, Title: "not awaiting payment",
			Detail: "a term booking must be approved by the host first",
		})
	default:
		telemetry.Logger(r.Context()).Error("unhandled payment error", slog.Any("error", err))
		WriteProblem(w, r, Problem{Status: http.StatusInternalServerError, Title: "internal server error"})
	}
}
