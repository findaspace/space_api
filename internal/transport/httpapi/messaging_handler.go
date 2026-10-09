package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/samnart1/space_api/internal/identity"
	"github.com/samnart1/space_api/internal/listing"
	"github.com/samnart1/space_api/internal/messaging"
	"github.com/samnart1/space_api/internal/platform/telemetry"
)

type messagingHandler struct {
	svc      *messaging.Service
	listings *listing.Service
	users    *identity.Service
}

type messageResponse struct {
	ID        string    `json:"id"`
	SenderID  string    `json:"sender_id"`
	Body      string    `json:"body"`
	Redacted  bool      `json:"redacted"`
	CreatedAt time.Time `json:"created_at"`
}

func counterpart(t messaging.Thread, viewer uuid.UUID) uuid.UUID {
	if viewer == t.HostID {
		return t.GuestID
	}
	return t.HostID
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
	ctx := r.Context()

	threads, err := h.svc.Inbox(ctx, p.UserID, clampQueryInt(r, "limit", 30, 1, 100))
	if err != nil {
		writeMessagingError(w, r, err)
		return
	}

	spaceIDs := make([]uuid.UUID, 0, len(threads))
	threadIDs := make([]uuid.UUID, 0, len(threads))
	peopleIDs := make([]uuid.UUID, 0, len(threads))
	for _, t := range threads {
		spaceIDs = append(spaceIDs, t.SpaceID)
		threadIDs = append(threadIDs, t.ID)
		peopleIDs = append(peopleIDs, counterpart(t, p.UserID))
	}

	// Three queries for the whole inbox, whatever its length. A failed lookup
	// leaves those fields out rather than failing the request.
	log := telemetry.Logger(ctx)
	spaces, err := h.listings.Summaries(ctx, spaceIDs)
	if err != nil {
		log.Warn("inbox listings unavailable", slog.Any("error", err))
	}
	people, err := h.users.Summaries(ctx, peopleIDs)
	if err != nil {
		log.Warn("inbox names unavailable", slog.Any("error", err))
	}
	last, err := h.svc.LastMessages(ctx, threadIDs)
	if err != nil {
		log.Warn("inbox previews unavailable", slog.Any("error", err))
	}

	out := make([]map[string]any, 0, len(threads))
	for _, t := range threads {
		role, _ := t.RoleOf(p.UserID)
		item := map[string]any{
			"id":              t.ID.String(),
			"space_id":        t.SpaceID.String(),
			"role":            role,
			"unread":          t.Unread(p.UserID),
			"last_message_at": t.LastMessageAt,
		}
		if body, ok := last[t.ID]; ok {
			item["last_message"] = body
		}
		// Title and slug only. The summary also carries the exact location,
		// which a conversation before booking must never reveal.
		if s, ok := spaces[t.SpaceID]; ok {
			item["space"] = map[string]any{"slug": s.Slug, "title": s.Title}
		}
		if u, ok := people[counterpart(t, p.UserID)]; ok {
			item["with"] = map[string]any{"display_name": u.DisplayName}
		}
		out = append(out, item)
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

	// _, msgs, err := h.svc.Messages(r.Context(), threadID, p.UserID, before, limit)
	thread, msgs, err := h.svc.Messages(r.Context(), threadID, p.UserID, before, limit)
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
	resp := map[string]any{"messages": out, "thread": h.header(r.Context(), thread, p.UserID)}
	if int32(len(msgs)) == limit {
		resp["next_before"] = msgs[len(msgs)-1].ID.String()
	}
	WriteJSON(w, r, http.StatusOK, resp)
}

// header describes a conversation for its page: which listing, and who is on
// the other side. Lookups that fail leave fields out.
func (h messagingHandler) header(ctx context.Context, t messaging.Thread, viewer uuid.UUID) map[string]any {
	role, _ := t.RoleOf(viewer)
	out := map[string]any{"id": t.ID.String(), "role": role}
	if spaces, err := h.listings.Summaries(ctx, []uuid.UUID{t.SpaceID}); err == nil {
		if s, ok := spaces[t.SpaceID]; ok {
			out["space"] = map[string]any{"slug": s.Slug, "title": s.Title}
		}
	}
	other := counterpart(t, viewer)
	if people, err := h.users.Summaries(ctx, []uuid.UUID{other}); err == nil {
		if u, ok := people[other]; ok {
			out["with"] = map[string]any{"display_name": u.DisplayName}
		}
	}
	return out
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
	case errors.Is(err, messaging.ErrBlocked):
		WriteProblem(w, r, Problem{Type: "urn:findaspace:messaging-unavailable", Status: 403, Title: "Messaging is unavailable for this conversation"})
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
