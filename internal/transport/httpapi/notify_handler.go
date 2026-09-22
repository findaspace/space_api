package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/samnart1/space_api/internal/notify"
	"github.com/samnart1/space_api/internal/platform/telemetry"
)

type notifyHandler struct{ svc *notify.Service }

func (h notifyHandler) inbox(w http.ResponseWriter, r *http.Request) {
	p, ok := PrincipalFrom(r.Context())
	if !ok {
		unauthorized(w, r, "missing bearer token")
		return
	}

	before := uuid.Nil
	if raw := r.URL.Query().Get("before"); raw != "" {
		parsed, err := uuid.Parse(raw)
		if err != nil {
			WriteProblem(w, r, Problem{Status: http.StatusUnprocessableEntity, Title: "invalid cursor"})
			return
		}
		before = parsed
	}
	limit := clampQueryInt(r, "limit", 30, 1, 100)

	items, unread, err := h.svc.Inbox(r.Context(), p.UserID, before, limit)
	if err != nil {
		writeNotifyError(w, r, err)
		return
	}

	out := make([]map[string]any, 0, len(items))
	for _, n := range items {
		out = append(out, map[string]any{
			"id": n.ID.String(), "event": n.Event,
			"title": n.Title, "body": n.Body, "data": n.Data,
			"read": n.ReadAt != nil, "created_at": n.CreatedAt,
		})
	}

	resp := map[string]any{"notifications": out, "unread": unread}
	if int32(len(items)) == limit {
		resp["next_before"] = items[len(items)-1].ID.String()
	}
	WriteJSON(w, r, http.StatusOK, resp)
}

func (h notifyHandler) markRead(w http.ResponseWriter, r *http.Request) {
	p, id, ok := principalAndID(w, r)
	if !ok {
		return
	}
	if err := h.svc.MarkRead(r.Context(), p.UserID, id); err != nil {
		writeNotifyError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h notifyHandler) markAllRead(w http.ResponseWriter, r *http.Request) {
	p, ok := PrincipalFrom(r.Context())
	if !ok {
		unauthorized(w, r, "missing bearer token")
		return
	}
	n, err := h.svc.MarkAllRead(r.Context(), p.UserID)
	if err != nil {
		writeNotifyError(w, r, err)
		return
	}
	WriteJSON(w, r, http.StatusOK, map[string]any{"marked": n, "at": time.Now()})
}

func (h notifyHandler) setPreference(w http.ResponseWriter, r *http.Request) {
	p, ok := PrincipalFrom(r.Context())
	if !ok {
		unauthorized(w, r, "missing bearer token")
		return
	}
	var body struct {
		Event string `json:"event"`
		SMS   bool   `json:"sms"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if err := h.svc.SetPreference(r.Context(), p.UserID, body.Event, body.SMS); err != nil {
		writeNotifyError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeNotifyError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, notify.ErrNotFound):
		WriteProblem(w, r, Problem{Status: http.StatusNotFound, Title: "not found"})
	case errors.Is(err, notify.ErrUnknownEvent):
		WriteProblem(w, r, Problem{Status: http.StatusUnprocessableEntity, Title: "unknown event",
			Errors: []FieldError{{Field: "event", Code: "unknown"}}})
	default:
		telemetry.Logger(r.Context()).Error("unhandled notify error", slog.Any("error", err))
		WriteProblem(w, r, Problem{Status: http.StatusInternalServerError, Title: "internal server error"})
	}
}
