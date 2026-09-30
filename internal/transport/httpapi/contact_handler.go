package httpapi

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/samnart1/space_api/internal/contact"
	"github.com/samnart1/space_api/internal/platform/telemetry"
)

type contactHandler struct{ svc *contact.Service }

func (h contactHandler) reveal(w http.ResponseWriter, r *http.Request) {
	p, spaceID, ok := principalAndID(w, r)
	if !ok {
		return
	}
	c, err := h.svc.Reveal(r.Context(), p.UserID, spaceID)
	switch {
	case errors.Is(err, contact.ErrNotFound):
		WriteProblem(w, r, Problem{Status: http.StatusNotFound, Title: "not found"})
	case errors.Is(err, contact.ErrTooMany):
		WriteProblem(w, r, Problem{Status: http.StatusTooManyRequests, Title: "too many contact reveals",
			Detail: "try again tomorrow, or chat with the owner on Findaspace"})
	case err != nil:
		telemetry.Logger(r.Context()).Error("contact reveal failed", slog.Any("error", err))
		WriteProblem(w, r, Problem{Status: http.StatusInternalServerError, Title: "internal server error"})
	default:
		WriteJSON(w, r, http.StatusOK, map[string]any{"display_name": c.DisplayName, "phone": c.Phone})
	}
}
