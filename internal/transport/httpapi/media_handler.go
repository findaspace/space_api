package httpapi

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	"github.com/samnart1/space_api/internal/listing"
	"github.com/samnart1/space_api/internal/media"
	"github.com/samnart1/space_api/internal/platform/telemetry"
)

type mediaHandler struct {
	svc      *media.Service
	listings *listing.Service
}

type mediaResponse struct {
	ID          string `json:"id"`
	URL         string `json:"url"`
	ContentType string `json:"content_type"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	ByteSize    int64  `json:"byte_size"`
	Position    int    `json:"position"`
	Status      string `json:"status"`
	CardURL     string `json:"card_url,omitempty"`
}

func toMediaResponse(m media.Media) mediaResponse {
	return mediaResponse{
		ID: m.ID.String(), URL: m.URL, ContentType: m.ContentType,
		Width: m.Width, Height: m.Height, ByteSize: m.ByteSize,
		Position: m.Position, Status: string(m.Status), CardURL: m.CardKey,
	}
}

func toMediaList(items []media.Media) []mediaResponse {
	out := make([]mediaResponse, 0, len(items))
	for _, m := range items {
		out = append(out, toMediaResponse(m))
	}
	return out
}

func (h mediaHandler) requestUpload(w http.ResponseWriter, r *http.Request) {
	p, spaceID, ok := principalAndID(w, r)
	if !ok {
		return
	}

	var body struct {
		ContentType string `json:"content_type"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}

	up, err := h.svc.RequestUpload(r.Context(), spaceID, p.UserID, body.ContentType)
	if err != nil {
		writeMediaError(w, r, err)
		return
	}

	WriteJSON(w, r, http.StatusCreated, map[string]any{
		"media":      toMediaResponse(up.Media),
		"upload_url": up.URL,
		"expires_at": up.ExpiresAt,
		"max_bytes":  up.MaxBytes,
		"method":     http.MethodPut,
		"headers":    map[string]string{"Content-Type": up.Media.ContentType},
	})
}

func (h mediaHandler) confirm(w http.ResponseWriter, r *http.Request) {
	p, spaceID, ok := principalAndID(w, r)
	if !ok {
		return
	}
	mediaID, err := uuid.Parse(r.PathValue("mediaId"))
	if err != nil {
		WriteProblem(w, r, Problem{Status: http.StatusNotFound, Title: "not found"})
		return
	}

	m, err := h.svc.Confirm(r.Context(), spaceID, mediaID, p.UserID)
	if err != nil {
		writeMediaError(w, r, err)
		return
	}
	WriteJSON(w, r, http.StatusOK, toMediaResponse(m))
}

func (h mediaHandler) remove(w http.ResponseWriter, r *http.Request) {
	p, spaceID, ok := principalAndID(w, r)
	if !ok {
		return
	}
	mediaID, err := uuid.Parse(r.PathValue("mediaId"))
	if err != nil {
		WriteProblem(w, r, Problem{Status: http.StatusNotFound, Title: "not found"})
		return
	}

	if err := h.svc.Delete(r.Context(), spaceID, mediaID, p.UserID); err != nil {
		writeMediaError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h mediaHandler) reorder(w http.ResponseWriter, r *http.Request) {
	p, spaceID, ok := principalAndID(w, r)
	if !ok {
		return
	}

	var body struct {
		IDs []string `json:"ids"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}

	ids := make([]uuid.UUID, 0, len(body.IDs))
	for _, raw := range body.IDs {
		id, err := uuid.Parse(raw)
		if err != nil {
			WriteProblem(w, r, Problem{
				Status: http.StatusUnprocessableEntity, Title: "invalid media id",
				Errors: []FieldError{{Field: "ids", Code: "invalid", Detail: raw}},
			})
			return
		}
		ids = append(ids, id)
	}

	items, err := h.svc.Reorder(r.Context(), spaceID, p.UserID, ids)
	if err != nil {
		writeMediaError(w, r, err)
		return
	}
	WriteJSON(w, r, http.StatusOK, map[string]any{"media": toMediaList(items)})
}

func (h mediaHandler) listOwn(w http.ResponseWriter, r *http.Request) {
	p, spaceID, ok := principalAndID(w, r)
	if !ok {
		return
	}
	if err := h.listings.AssertHostOwns(r.Context(), spaceID, p.UserID); err != nil {
		writeListingError(w, r, err)
		return
	}

	items, err := h.svc.ForSpace(r.Context(), spaceID)
	if err != nil {
		writeMediaError(w, r, err)
		return
	}
	WriteJSON(w, r, http.StatusOK, map[string]any{"media": toMediaList(items)})
}

// listPublic resolves the slug through the listing service, which already
// returns 404 for anything unpublished. Composing two services is the HTTP
// layer's job; neither domain package learns about the other.
func (h mediaHandler) listPublic(w http.ResponseWriter, r *http.Request) {
	sp, err := h.listings.GetPublic(r.Context(), r.PathValue("slug"))
	if err != nil {
		writeListingError(w, r, err)
		return
	}

	items, err := h.svc.ForSpace(r.Context(), sp.ID)
	if err != nil {
		writeMediaError(w, r, err)
		return
	}
	WriteJSON(w, r, http.StatusOK, map[string]any{"media": toMediaList(items)})
}

func writeMediaError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, media.ErrNotFound), errors.Is(err, listing.ErrNotFound):
		WriteProblem(w, r, Problem{Status: http.StatusNotFound, Title: "not found"})

	case errors.Is(err, media.ErrTooMany):
		WriteProblem(w, r, Problem{Status: http.StatusConflict, Title: "photo limit reached"})

	case errors.Is(err, media.ErrNotUploaded):
		WriteProblem(w, r, Problem{
			Status: http.StatusConflict, Title: "no upload found",
			Detail: "upload the file to the presigned URL before confirming",
		})

	case errors.Is(err, media.ErrUnsupportedType):
		WriteProblem(w, r, Problem{
			Status: http.StatusUnprocessableEntity, Title: "unsupported image type",
			Errors: []FieldError{{Field: "content_type", Code: "unsupported",
				Detail: "image/jpeg or image/png"}},
		})

	case errors.Is(err, media.ErrCorruptImage):
		WriteProblem(w, r, Problem{Status: http.StatusUnprocessableEntity, Title: "image could not be read"})

	case errors.Is(err, media.ErrImageTooLarge), errors.Is(err, media.ErrSizeMismatch):
		WriteProblem(w, r, Problem{Status: http.StatusRequestEntityTooLarge, Title: "image too large"})

	default:
		telemetry.Logger(r.Context()).Error("unhandled media error", slog.Any("error", err))
		WriteProblem(w, r, Problem{Status: http.StatusInternalServerError, Title: "internal server error"})
	}
}
