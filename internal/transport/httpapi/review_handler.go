package httpapi

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/samnart1/space_api/internal/listing"
	"github.com/samnart1/space_api/internal/platform/telemetry"
	"github.com/samnart1/space_api/internal/review"
)

type reviewHandler struct {
	svc      *review.Service
	listings *listing.Service
}

func (h reviewHandler) write(w http.ResponseWriter, r *http.Request) {
	p, bookingID, ok := principalAndID(w, r)
	if !ok {
		return
	}
	var body struct {
		Rating int    `json:"rating"`
		Body   string `json:"body"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}

	rv, err := h.svc.Write(r.Context(), bookingID, p.UserID, body.Rating, body.Body)
	if err != nil {
		writeReviewError(w, r, err)
		return
	}
	WriteJSON(w, r, http.StatusCreated, map[string]any{
		"id":       rv.ID.String(),
		"rating":   rv.Rating,
		"revealed": rv.RevealedAt != nil,
	})
}

func (h reviewHandler) forListing(w http.ResponseWriter, r *http.Request) {
	sp, err := h.listings.GetPublic(r.Context(), r.PathValue("slug"))
	if err != nil {
		writeListingError(w, r, err)
		return
	}

	items, sum, err := h.svc.ForSpace(r.Context(), sp.ID, clampQueryInt(r, "limit", 20, 1, 100))
	if err != nil {
		writeReviewError(w, r, err)
		return
	}

	out := make([]map[string]any, 0, len(items))
	for _, rv := range items {
		out = append(out, map[string]any{
			"rating": rv.Rating, "body": rv.Body, "published_at": rv.RevealedAt,
		})
	}
	WriteJSON(w, r, http.StatusOK, map[string]any{"summary": sum, "reviews": out})
}

func writeReviewError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, review.ErrNotFound):
		WriteProblem(w, r, Problem{Status: http.StatusNotFound, Title: "not found"})
	case errors.Is(err, review.ErrNotEligible):
		WriteProblem(w, r, Problem{Status: http.StatusConflict, Title: "stay cannot be reviewed yet"})
	case errors.Is(err, review.ErrWindowClosed):
		WriteProblem(w, r, Problem{Status: http.StatusConflict, Title: "review window has closed"})
	case errors.Is(err, review.ErrAlreadyWritten):
		WriteProblem(w, r, Problem{Status: http.StatusConflict, Title: "already reviewed"})
	case errors.Is(err, review.ErrBadRating):
		WriteProblem(w, r, Problem{Status: http.StatusUnprocessableEntity, Title: "invalid rating",
			Errors: []FieldError{{Field: "rating", Code: "range", Detail: "1 to 5"}}})
	default:
		telemetry.Logger(r.Context()).Error("unhandled review error", slog.Any("error", err))
		WriteProblem(w, r, Problem{Status: http.StatusInternalServerError, Title: "internal server error"})
	}
}
