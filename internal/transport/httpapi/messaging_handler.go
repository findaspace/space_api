package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/samnart1/space_api/internal/listing"
	"github.com/samnart1/space_api/internal/messaging"
	"github.com/samnart1/space_api/internal/platform/telemetry"
)

type messagingHandler struct{ svc *messaging.Service }

type messageResponse struct {
	ID        string    `json:"id"`
	SenderID  string    `json:"sender_id"`
	Body      string    `json:"body"`
	Redacted  bool      `json:"redacted"`
	CreatedAt time.Time `json:"created_at"`
}

func toMessageResponse(m messaging.Message) messageResponse {
	return messageResponse{
		ID: m.ID.String(), SenderID: m.SenderID.String(),
		Body: m.Body, Redacted: m.Redacted, CreatedAt: m.CreatedAt,
	}
}

func (h messagingHandler) send(w http.ResponseWriter, r *http.Request) {
	p, spaceID, ok := principalAndID(w, r)
	if !ok {
		return
	}
	var body struct {
		Body string `json:"body"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}

	t, m, err := h.svc.Send(r.Context(), spaceID, p.UserID, body.Body)
	if err != nil {
		writeMessagingError(w, r, err)
		return
	}
	WriteJSON(w, r, http.StatusCreated, map[string]any{
		"thread_id": t.ID.String(),
		"message":   toMessageResponse(m),
	})
}

func (h messagingHandler) reply(w http.ResponseWriter, r *http.Request) {
	p, threadID, ok := principalAndID(w, r)
	if !ok {
		return
	}
	var body struct {
		Body string `json:"body"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}

	m, err := h.svc.Reply(r.Context(), threadID, p.UserID, body.Body)
	if err != nil {
		writeMessagingError(w, r, err)
		return
	}
	WriteJSON(w, r, http.StatusCreated, toMessageResponse(m))
}

func (h messagingHandler) inbox(w http.ResponseWriter, r *http.Request) {
	p, ok := PrincipalFrom(r.Context())
	if !ok {
		unauthorized(w, r, "missing bearer token")
		return
	}

	threads, err := h.svc.Inbox(r.Context(), p.UserID, clampQueryInt(r, "limit", 30, 1, 100))
	if err != nil {
		writeMessagingError(w, r, err)
		return
	}

	out := make([]map[string]any, 0, len(threads))
	for _, t := range threads {
		role, _ := t.RoleOf(p.UserID)
		out = append(out, map[string]any{
			"id":              t.ID.String(),
			"space_id":        t.SpaceID.String(),
			"role":            role,
			"unread":          t.Unread(p.UserID),
			"last_message_at": t.LastMessageAt,
		})
	}
	WriteJSON(w, r, http.StatusOK, map[string]any{"threads": out})
}

func (h messagingHandler) messages(w http.ResponseWriter, r *http.Request) {
	p, threadID, ok := principalAndID(w, r)
	if !ok {
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

	_, msgs, err := h.svc.Messages(r.Context(), threadID, p.UserID, before, limit)
	if err != nil {
		writeMessagingError(w, r, err)
		return
	}

	out := make([]messageResponse, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, toMessageResponse(m))
	}

	// The cursor is the oldest id in this page. A short page means the start
	// of the thread was reached.
	resp := map[string]any{"messages": out}
	if int32(len(msgs)) == limit {
		resp["next_before"] = msgs[len(msgs)-1].ID.String()
	}
	WriteJSON(w, r, http.StatusOK, resp)
}

func (h messagingHandler) markRead(w http.ResponseWriter, r *http.Request) {
	p, threadID, ok := principalAndID(w, r)
	if !ok {
		return
	}
	if err := h.svc.MarkRead(r.Context(), threadID, p.UserID); err != nil {
		writeMessagingError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeMessagingError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, messaging.ErrNotFound), errors.Is(err, listing.ErrNotFound):
		WriteProblem(w, r, Problem{Status: http.StatusNotFound, Title: "not found"})
	case errors.Is(err, messaging.ErrOwnListing):
		WriteProblem(w, r, Problem{Status: http.StatusUnprocessableEntity, Title: "cannot message yourself"})
	case errors.Is(err, messaging.ErrEmptyMessage):
		WriteProblem(w, r, Problem{Status: http.StatusUnprocessableEntity, Title: "message is empty",
			Errors: []FieldError{{Field: "body", Code: "required"}}})
	case errors.Is(err, messaging.ErrTooLong):
		WriteProblem(w, r, Problem{Status: http.StatusUnprocessableEntity, Title: "message is too long",
			Errors: []FieldError{{Field: "body", Code: "max_length", Detail: "4000 characters"}}})
	default:
		telemetry.Logger(r.Context()).Error("unhandled messaging error", slog.Any("error", err))
		WriteProblem(w, r, Problem{Status: http.StatusInternalServerError, Title: "internal server error"})
	}
}
