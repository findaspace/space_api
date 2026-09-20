package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/samnart1/space_api/internal/booking"
	"github.com/samnart1/space_api/internal/calendar"
	"github.com/samnart1/space_api/internal/identity"
	"github.com/samnart1/space_api/internal/listing"
	"github.com/samnart1/space_api/internal/platform/telemetry"
)

type bookingHandler struct{ svc *booking.Service }

type bookingResponse struct {
	ID          string         `json:"id"`
	Reference   string         `json:"reference"`
	SpaceID     string         `json:"space_id"`
	Mode        booking.Mode   `json:"rental_mode"`
	Status      booking.Status `json:"status"`
	StartsOn    string         `json:"starts_on"`
	EndsOn      string         `json:"ends_on"`
	Nights      int            `json:"nights,omitempty"`
	Months      int            `json:"months,omitempty"`
	Guests      int            `json:"guests"`
	Currency    string         `json:"currency"`
	TotalMinor  int64          `json:"total_minor"`
	DueNowMinor int64          `json:"due_now_minor"`
	Policy      booking.Policy `json:"cancellation_policy"`
	RefundMinor int64          `json:"refund_minor,omitempty"`
	HoldExpires *time.Time     `json:"hold_expires_at,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
}

func toBookingResponse(b booking.Booking) bookingResponse {
	return bookingResponse{
		ID: b.ID.String(), Reference: b.Reference, SpaceID: b.SpaceID.String(),
		Mode: b.Mode, Status: b.Status,
		StartsOn: b.StartsOn.Format(dateLayout), EndsOn: b.EndsOn.Format(dateLayout),
		Nights: b.Nights, Months: b.Months, Guests: b.Guests,
		Currency: b.Currency, TotalMinor: b.TotalMinor, DueNowMinor: b.DueNowMinor,
		Policy: b.Policy, RefundMinor: b.RefundMinor,
		HoldExpires: b.HoldExpiresAt, CreatedAt: b.CreatedAt,
	}
}

func (h bookingHandler) create(w http.ResponseWriter, r *http.Request) {
	p, ok := PrincipalFrom(r.Context())
	if !ok {
		unauthorized(w, r, "missing bearer token")
		return
	}

	var body struct {
		SpaceID  string `json:"space_id"`
		CheckIn  string `json:"check_in"`
		CheckOut string `json:"check_out"`
		StartsOn string `json:"starts_on"`
		Months   int    `json:"months"`
		Guests   int    `json:"guests"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}

	spaceID, err := uuid.Parse(body.SpaceID)
	if err != nil {
		WriteProblem(w, r, Problem{
			Status: http.StatusUnprocessableEntity, Title: "invalid space",
			Errors: []FieldError{{
				Field: "space_id", Code: "invalid",
				Detail: "must be a uuid",
			}},
		})
		return
	}

	req := booking.HoldRequest{
		SpaceID: spaceID, GuestID: p.UserID, Guests: body.Guests, Months: body.Months,
		// Scoped to the guest by the unique index, so two people may safely
		// pick the same key.
		IdempotencyKey: r.Header.Get("Idempotency-Key"),
	}

	if body.CheckIn != "" {
		if req.CheckIn, err = time.Parse(dateLayout, body.CheckIn); err != nil {
			badDates(w, r, "check_in must be YYYY-MM-DD")
			return
		}
		if req.CheckOut, err = time.Parse(dateLayout, body.CheckOut); err != nil {
			badDates(w, r, "check_out must be YYYY-MM-DD")
			return
		}
	}
	if body.StartsOn != "" {
		if req.StartsOn, err = time.Parse(dateLayout, body.StartsOn); err != nil {
			badDates(w, r, "starts_on must be YYYY-MM-DD")
			return
		}
	}

	b, err := h.svc.Hold(r.Context(), req)
	if err != nil {
		writeBookingError(w, r, err)
		return
	}
	WriteJSON(w, r, http.StatusCreated, toBookingResponse(b))
}

func (h bookingHandler) list(w http.ResponseWriter, r *http.Request) {
	p, ok := PrincipalFrom(r.Context())
	if !ok {
		unauthorized(w, r, "missing bearer token")
		return
	}

	limit := clampQueryInt(r, "limit", 20, 1, 100)
	offset := clampQueryInt(r, "offset", 0, 0, 100000)

	var (
		items []booking.Booking
		err   error
	)
	if r.URL.Query().Get("role") == "host" {
		items, err = h.svc.ListAsHost(r.Context(), p.UserID, limit, offset)
	} else {
		items, err = h.svc.ListAsGuest(r.Context(), p.UserID, limit, offset)
	}
	if err != nil {
		writeBookingError(w, r, err)
		return
	}

	out := make([]bookingResponse, 0, len(items))
	for _, b := range items {
		out = append(out, toBookingResponse(b))
	}
	WriteJSON(w, r, http.StatusOK, map[string]any{"bookings": out, "limit": limit, "offset": offset})
}

func (h bookingHandler) get(w http.ResponseWriter, r *http.Request) {
	p, id, ok := principalAndID(w, r)
	if !ok {
		return
	}
	b, err := h.svc.Get(r.Context(), id, p.UserID)
	if err != nil {
		writeBookingError(w, r, err)
		return
	}
	WriteJSON(w, r, http.StatusOK, toBookingResponse(b))
}

func (h bookingHandler) refundQuote(w http.ResponseWriter, r *http.Request) {
	p, id, ok := principalAndID(w, r)
	if !ok {
		return
	}
	out, err := h.svc.CancellationQuote(r.Context(), id, p.UserID, p.Has(identity.RoleAdmin))
	if err != nil {
		writeBookingError(w, r, err)
		return
	}
	WriteJSON(w, r, http.StatusOK, out)
}

func (h bookingHandler) cancel(w http.ResponseWriter, r *http.Request) {
	p, id, ok := principalAndID(w, r)
	if !ok {
		return
	}

	var body struct {
		Reason string `json:"reason"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}

	res, err := h.svc.Cancel(r.Context(), id, p.UserID, p.Has(identity.RoleAdmin), body.Reason)
	if err != nil {
		writeBookingError(w, r, err)
		return
	}
	WriteJSON(w, r, http.StatusOK, map[string]any{
		"booking": toBookingResponse(res.Booking),
		"refund":  res.Refund,
	})
}

func (h bookingHandler) approve(w http.ResponseWriter, r *http.Request) {
	p, id, ok := principalAndID(w, r)
	if !ok {
		return
	}
	b, err := h.svc.Approve(r.Context(), id, p.UserID)
	if err != nil {
		writeBookingError(w, r, err)
		return
	}
	WriteJSON(w, r, http.StatusOK, toBookingResponse(b))
}

func (h bookingHandler) decline(w http.ResponseWriter, r *http.Request) {
	p, id, ok := principalAndID(w, r)
	if !ok {
		return
	}

	var body struct {
		Reason string `json:"reason"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}

	b, err := h.svc.Decline(r.Context(), id, p.UserID, body.Reason)
	if err != nil {
		writeBookingError(w, r, err)
		return
	}
	WriteJSON(w, r, http.StatusOK, toBookingResponse(b))
}

func writeBookingError(w http.ResponseWriter, r *http.Request, err error) {
	// Pricing rejections arrive through the booking path unchanged, so the
	// guest gets the same field-level message they saw on the quote screen.
	if errors.Is(err, calendar.ErrUnavailable) || errors.Is(err, calendar.ErrTooShort) ||
		errors.Is(err, calendar.ErrTooLong) || errors.Is(err, calendar.ErrPastDate) ||
		errors.Is(err, calendar.ErrHorizon) || errors.Is(err, calendar.ErrTooManyGuests) ||
		errors.Is(err, calendar.ErrInvalidRange) || errors.Is(err, calendar.ErrModeMismatch) ||
		errors.Is(err, calendar.ErrNotBookable) || errors.Is(err, calendar.ErrAdvanceMonths) {
		writeCalendarError(w, r, err)
		return
	}

	switch {
	case errors.Is(err, booking.ErrNotFound), errors.Is(err, listing.ErrNotFound):
		WriteProblem(w, r, Problem{Status: http.StatusNotFound, Title: "not found"})

	case errors.Is(err, booking.ErrDatesTaken):
		WriteProblem(w, r, Problem{
			Status: http.StatusConflict, Title: "dates no longer available",
			Detail: "somebody booked those dates first",
		})

	case errors.Is(err, booking.ErrOwnListing):
		WriteProblem(w, r, Problem{
			Status: http.StatusUnprocessableEntity, Title: "cannot book your own listing",
		})

	case errors.Is(err, booking.ErrBadTransition):
		WriteProblem(w, r, Problem{Status: http.StatusConflict, Title: "invalid status change"})

	case errors.Is(err, booking.ErrNotPermitted):
		WriteProblem(w, r, Problem{Status: http.StatusForbidden, Title: "not allowed"})

	case errors.Is(err, booking.ErrHoldExpired):
		WriteProblem(w, r, Problem{
			Status: http.StatusConflict, Title: "hold expired",
			Detail: "the booking was not confirmed in time",
		})

	case errors.Is(err, booking.ErrConflict):
		WriteProblem(w, r, Problem{
			Status: http.StatusConflict, Title: "booking changed",
			Detail: "reload and try again",
		})

	default:
		telemetry.Logger(r.Context()).Error("unhandled booking error", slog.Any("error", err))
		WriteProblem(w, r, Problem{Status: http.StatusInternalServerError, Title: "internal server error"})
	}
}
