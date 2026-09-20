package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/samnart1/space_api/internal/calendar"
	"github.com/samnart1/space_api/internal/listing"
	"github.com/samnart1/space_api/internal/platform/telemetry"
)

const dateLayout = "2006-01-02"

type calendarHandler struct {
	svc      *calendar.Service
	listings *listing.Service
}

// toPricing is the seam between the two domains. Neither package imports the
// other; the handler owns the translation.
func toPricing(s listing.Space) calendar.SpacePricing {
	return calendar.SpacePricing{
		SpaceID:        s.ID,
		Mode:           string(s.Mode),
		Status:         string(s.Status),
		Currency:       s.Currency,
		BasePriceMinor: s.BasePriceMinor,
		PricePeriod:    string(s.PricePeriod),
		DepositMinor:   s.DepositMinor,
		AdvanceMonths:  int(s.AdvanceMonths),
		MaxOccupancy:   int(s.MaxOccupancy),
	}
}

func queryDate(r *http.Request, key string) (time.Time, bool) {
	raw := r.URL.Query().Get(key)
	if raw == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(dateLayout, raw)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

func queryInt(r *http.Request, key string, def int) int {
	raw := r.URL.Query().Get(key)
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return def
	}
	return n
}

func (h calendarHandler) quote(w http.ResponseWriter, r *http.Request) {
	sp, err := h.listings.GetPublic(r.Context(), r.PathValue("slug"))
	if err != nil {
		writeListingError(w, r, err)
		return
	}

	req := calendar.QuoteRequest{Guests: queryInt(r, "guests", 1), Months: queryInt(r, "months", 0)}

	if sp.Mode == listing.ModeNightly {
		checkIn, okIn := queryDate(r, "check_in")
		checkOut, okOut := queryDate(r, "check_out")
		if !okIn || !okOut {
			badDates(w, r, "check_in and check_out are required, formatted YYYY-MM-DD")
			return
		}
		req.CheckIn, req.CheckOut = checkIn, checkOut
	} else {
		startsOn, ok := queryDate(r, "starts_on")
		if !ok || req.Months < 1 {
			badDates(w, r, "starts_on (YYYY-MM-DD) and months are required")
			return
		}
		req.StartsOn = startsOn
	}

	q, err := h.svc.Quote(r.Context(), toPricing(sp), req)
	if err != nil {
		writeCalendarError(w, r, err)
		return
	}
	WriteJSON(w, r, http.StatusOK, q)
}

func (h calendarHandler) publicCalendar(w http.ResponseWriter, r *http.Request) {
	sp, err := h.listings.GetPublic(r.Context(), r.PathValue("slug"))
	if err != nil {
		writeListingError(w, r, err)
		return
	}

	from, okFrom := queryDate(r, "from")
	to, okTo := queryDate(r, "to")
	if !okFrom {
		from = time.Now()
	}
	if !okTo {
		to = from.AddDate(0, 3, 0)
	}

	nights, err := h.svc.Calendar(r.Context(), toPricing(sp), from, to)
	if err != nil {
		writeCalendarError(w, r, err)
		return
	}
	WriteJSON(w, r, http.StatusOK, map[string]any{
		"currency": sp.Currency,
		"nights":   nights,
	})
}

func (h calendarHandler) getRules(w http.ResponseWriter, r *http.Request) {
	_, spaceID, ok := h.ownedSpace(w, r)
	if !ok {
		return
	}
	rules, err := h.svc.Rules(r.Context(), spaceID)
	if err != nil {
		writeCalendarError(w, r, err)
		return
	}
	WriteJSON(w, r, http.StatusOK, rulesResponse(rules))
}

func (h calendarHandler) setRules(w http.ResponseWriter, r *http.Request) {
	_, spaceID, ok := h.ownedSpace(w, r)
	if !ok {
		return
	}

	var body struct {
		MinNights          int   `json:"min_nights"`
		MaxNights          int   `json:"max_nights"`
		WeeklyDiscountBps  int   `json:"weekly_discount_bps"`
		MonthlyDiscountBps int   `json:"monthly_discount_bps"`
		CleaningFeeMinor   int64 `json:"cleaning_fee_minor"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}

	rules, err := h.svc.SetRules(r.Context(), spaceID, calendar.StayRules{
		MinNights:          body.MinNights,
		MaxNights:          body.MaxNights,
		WeeklyDiscountBps:  body.WeeklyDiscountBps,
		MonthlyDiscountBps: body.MonthlyDiscountBps,
		CleaningFeeMinor:   body.CleaningFeeMinor,
	})
	if err != nil {
		writeCalendarError(w, r, err)
		return
	}
	WriteJSON(w, r, http.StatusOK, rulesResponse(rules))
}

func rulesResponse(r calendar.StayRules) map[string]any {
	return map[string]any{
		"min_nights":           r.MinNights,
		"max_nights":           r.MaxNights,
		"weekly_discount_bps":  r.WeeklyDiscountBps,
		"monthly_discount_bps": r.MonthlyDiscountBps,
		"cleaning_fee_minor":   r.CleaningFeeMinor,
	}
}

func planResponse(p calendar.RatePlan) map[string]any {
	out := map[string]any{
		"id":          p.ID.String(),
		"name":        p.Name,
		"starts_on":   p.StartsOn.Format(dateLayout),
		"ends_on":     p.EndsOn.Format(dateLayout),
		"price_minor": p.PriceMinor,
	}
	if p.MinNights != nil {
		out["min_nights"] = *p.MinNights
	}
	return out
}

func (h calendarHandler) listPlans(w http.ResponseWriter, r *http.Request) {
	_, spaceID, ok := h.ownedSpace(w, r)
	if !ok {
		return
	}
	plans, err := h.svc.ListPlans(r.Context(), spaceID)
	if err != nil {
		writeCalendarError(w, r, err)
		return
	}
	out := make([]map[string]any, 0, len(plans))
	for _, p := range plans {
		out = append(out, planResponse(p))
	}
	WriteJSON(w, r, http.StatusOK, map[string]any{"rate_plans": out})
}

func (h calendarHandler) createPlan(w http.ResponseWriter, r *http.Request) {
	_, spaceID, ok := h.ownedSpace(w, r)
	if !ok {
		return
	}

	var body struct {
		Name       string `json:"name"`
		StartsOn   string `json:"starts_on"`
		EndsOn     string `json:"ends_on"`
		PriceMinor int64  `json:"price_minor"`
		MinNights  *int   `json:"min_nights"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}

	startsOn, err1 := time.Parse(dateLayout, body.StartsOn)
	endsOn, err2 := time.Parse(dateLayout, body.EndsOn)
	if err1 != nil || err2 != nil {
		badDates(w, r, "starts_on and ends_on must be YYYY-MM-DD")
		return
	}

	plan, err := h.svc.CreatePlan(r.Context(), calendar.RatePlan{
		SpaceID: spaceID, Name: body.Name,
		StartsOn: startsOn, EndsOn: endsOn,
		PriceMinor: body.PriceMinor, MinNights: body.MinNights,
	})
	if err != nil {
		writeCalendarError(w, r, err)
		return
	}
	WriteJSON(w, r, http.StatusCreated, planResponse(plan))
}

func (h calendarHandler) deletePlan(w http.ResponseWriter, r *http.Request) {
	_, spaceID, ok := h.ownedSpace(w, r)
	if !ok {
		return
	}
	planID, err := uuid.Parse(r.PathValue("planId"))
	if err != nil {
		WriteProblem(w, r, Problem{Status: http.StatusNotFound, Title: "not found"})
		return
	}
	if err := h.svc.DeletePlan(r.Context(), planID, spaceID); err != nil {
		writeCalendarError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h calendarHandler) listBlocks(w http.ResponseWriter, r *http.Request) {
	_, spaceID, ok := h.ownedSpace(w, r)
	if !ok {
		return
	}

	from, okFrom := queryDate(r, "from")
	to, okTo := queryDate(r, "to")
	if !okFrom {
		from = time.Now()
	}
	if !okTo {
		to = from.AddDate(1, 0, 0)
	}

	blocks, err := h.svc.ListBlocks(r.Context(), spaceID, from, to)
	if err != nil {
		writeCalendarError(w, r, err)
		return
	}

	out := make([]map[string]any, 0, len(blocks))
	for _, b := range blocks {
		out = append(out, map[string]any{
			"id":        b.ID.String(),
			"starts_on": b.StartsOn.Format(dateLayout),
			"ends_on":   b.EndsOn.Format(dateLayout),
			"reason":    b.Reason,
		})
	}
	WriteJSON(w, r, http.StatusOK, map[string]any{"blocks": out})
}

func (h calendarHandler) createBlock(w http.ResponseWriter, r *http.Request) {
	_, spaceID, ok := h.ownedSpace(w, r)
	if !ok {
		return
	}

	var body struct {
		StartsOn string `json:"starts_on"`
		EndsOn   string `json:"ends_on"`
		Reason   string `json:"reason"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}

	startsOn, err1 := time.Parse(dateLayout, body.StartsOn)
	endsOn, err2 := time.Parse(dateLayout, body.EndsOn)
	if err1 != nil || err2 != nil {
		badDates(w, r, "starts_on and ends_on must be YYYY-MM-DD")
		return
	}

	block, err := h.svc.CreateBlock(r.Context(), calendar.Block{
		SpaceID: spaceID, StartsOn: startsOn, EndsOn: endsOn, Reason: body.Reason,
	})
	if err != nil {
		writeCalendarError(w, r, err)
		return
	}
	WriteJSON(w, r, http.StatusCreated, map[string]any{
		"id":        block.ID.String(),
		"starts_on": block.StartsOn.Format(dateLayout),
		"ends_on":   block.EndsOn.Format(dateLayout),
		"reason":    block.Reason,
	})
}

func (h calendarHandler) deleteBlock(w http.ResponseWriter, r *http.Request) {
	_, spaceID, ok := h.ownedSpace(w, r)
	if !ok {
		return
	}
	blockID, err := uuid.Parse(r.PathValue("blockId"))
	if err != nil {
		WriteProblem(w, r, Problem{Status: http.StatusNotFound, Title: "not found"})
		return
	}
	if err := h.svc.DeleteBlock(r.Context(), blockID, spaceID); err != nil {
		writeCalendarError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ownedSpace resolves the path id and proves the caller owns it before any
// calendar write touches the database.
func (h calendarHandler) ownedSpace(w http.ResponseWriter, r *http.Request) (Principal, uuid.UUID, bool) {
	p, spaceID, ok := principalAndID(w, r)
	if !ok {
		return Principal{}, uuid.Nil, false
	}
	if err := h.listings.AssertHostOwns(r.Context(), spaceID, p.UserID); err != nil {
		writeListingError(w, r, err)
		return Principal{}, uuid.Nil, false
	}
	return p, spaceID, true
}

func badDates(w http.ResponseWriter, r *http.Request, detail string) {
	WriteProblem(w, r, Problem{
		Status: http.StatusUnprocessableEntity,
		Title:  "invalid dates",
		Detail: detail,
	})
}

func writeCalendarError(w http.ResponseWriter, r *http.Request, err error) {
	field := func(f, code, detail string) Problem {
		return Problem{
			Status: http.StatusUnprocessableEntity, Title: "invalid request",
			Errors: []FieldError{{Field: f, Code: code, Detail: detail}},
		}
	}

	switch {
	case errors.Is(err, calendar.ErrNotFound):
		WriteProblem(w, r, Problem{Status: http.StatusNotFound, Title: "not found"})

	case errors.Is(err, calendar.ErrOverlap):
		WriteProblem(w, r, Problem{
			Status: http.StatusConflict, Title: "overlapping rate plan",
			Detail: "another rate plan already covers part of that period",
		})

	case errors.Is(err, calendar.ErrUnavailable):
		WriteProblem(w, r, Problem{
			Status: http.StatusConflict, Title: "dates unavailable",
			Detail: "one or more of those nights are not available",
		})

	case errors.Is(err, calendar.ErrInvalidRange):
		WriteProblem(w, r, field("ends_on", "invalid", "end date must be after start date"))
	case errors.Is(err, calendar.ErrPastDate):
		WriteProblem(w, r, field("check_in", "past", "the date is in the past"))
	case errors.Is(err, calendar.ErrHorizon):
		WriteProblem(w, r, field("check_in", "too_far", "that date is too far ahead"))
	case errors.Is(err, calendar.ErrTooShort):
		WriteProblem(w, r, field("check_out", "min_nights", "the stay is shorter than the host allows"))
	case errors.Is(err, calendar.ErrTooLong):
		WriteProblem(w, r, field("check_out", "max_nights", "the stay is longer than the host allows"))
	case errors.Is(err, calendar.ErrTooManyGuests):
		WriteProblem(w, r, field("guests", "max_occupancy", "too many guests for this space"))
	case errors.Is(err, calendar.ErrAdvanceMonths):
		WriteProblem(w, r, field("months", "advance", "shorter than the required rent advance"))
	case errors.Is(err, calendar.ErrInvalidPrice):
		WriteProblem(w, r, field("price_minor", "invalid", "price must be positive"))

	case errors.Is(err, calendar.ErrModeMismatch):
		WriteProblem(w, r, Problem{
			Status: http.StatusUnprocessableEntity, Title: "wrong quote type",
			Detail: "this listing is not rented that way",
		})

	case errors.Is(err, calendar.ErrNotBookable):
		WriteProblem(w, r, Problem{Status: http.StatusConflict, Title: "listing is not accepting bookings"})

	default:
		telemetry.Logger(r.Context()).Error("unhandled calendar error", slog.Any("error", err))
		WriteProblem(w, r, Problem{Status: http.StatusInternalServerError, Title: "internal server error"})
	}
}
