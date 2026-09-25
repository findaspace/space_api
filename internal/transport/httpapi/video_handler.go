package httpapi

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/samnart1/space_api/internal/listing"
	"github.com/samnart1/space_api/internal/media"
	"github.com/samnart1/space_api/internal/platform/telemetry"
)

type videoHandler struct {
	svc      *media.Service
	listings *listing.Service
}

func (h videoHandler) request(w http.ResponseWriter, r *http.Request) {
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

	out, err := h.svc.RequestVideoUpload(r.Context(), spaceID, p.UserID, body.ContentType)
	if err != nil {
		writeVideoError(w, r, err)
		return
	}
	WriteJSON(w, r, http.StatusCreated, out)
}

func (h videoHandler) confirm(w http.ResponseWriter, r *http.Request) {
	p, spaceID, ok := principalAndID(w, r)
	if !ok {
		return
	}

	v, err := h.svc.ConfirmVideo(r.Context(), spaceID, p.UserID)
	if err != nil {
		writeVideoError(w, r, err)
		return
	}
	WriteJSON(w, r, http.StatusAccepted, v)
}

func (h videoHandler) own(w http.ResponseWriter, r *http.Request) {
	p, spaceID, ok := principalAndID(w, r)
	if !ok {
		return
	}
	if err := h.listings.AssertHostOwns(r.Context(), spaceID, p.UserID); err != nil {
		writeListingError(w, r, err)
		return
	}

	v, err := h.svc.Video(r.Context(), spaceID)
	if err != nil {
		writeVideoError(w, r, err)
		return
	}
	WriteJSON(w, r, http.StatusOK, v)
}

func (h videoHandler) remove(w http.ResponseWriter, r *http.Request) {
	p, spaceID, ok := principalAndID(w, r)
	if !ok {
		return
	}
	if err := h.svc.DeleteVideo(r.Context(), spaceID, p.UserID); err != nil {
		writeVideoError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// forListing is the public one. A listing with no walkthrough returns 404,
// which the client treats as "no badge" rather than as an error.
func (h videoHandler) forListing(w http.ResponseWriter, r *http.Request) {
	sp, err := h.listings.GetPublic(r.Context(), r.PathValue("slug"))
	if err != nil {
		writeListingError(w, r, err)
		return
	}

	v, err := h.svc.Video(r.Context(), sp.ID)
	if err != nil {
		writeVideoError(w, r, err)
		return
	}
	if v.Status != media.VideoReady {
		WriteProblem(w, r, Problem{Status: http.StatusNotFound, Title: "no walkthrough"})
		return
	}
	WriteJSON(w, r, http.StatusOK, v)
}

func writeVideoError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, media.ErrVideoNotFound):
		WriteProblem(w, r, Problem{Status: http.StatusNotFound, Title: "no walkthrough"})
	case errors.Is(err, media.ErrVideoExists):
		WriteProblem(w, r, Problem{Status: http.StatusConflict,
			Title:  "this listing already has a walkthrough",
			Detail: "delete the existing one before uploading another"})
	case errors.Is(err, media.ErrUnsupportedType):
		WriteProblem(w, r, Problem{Status: http.StatusUnprocessableEntity, Title: "unsupported format",
			Errors: []FieldError{{Field: "content_type", Code: "unsupported",
				Detail: "mp4, mov, webm, mkv or 3gp"}}})
	case errors.Is(err, media.ErrVideoTooLarge):
		WriteProblem(w, r, Problem{Status: http.StatusRequestEntityTooLarge,
			Title:  "video is too large",
			Detail: "record at 720p or lower and keep it under a minute"})
	case errors.Is(err, media.ErrNotUploaded):
		WriteProblem(w, r, Problem{Status: http.StatusConflict, Title: "nothing was uploaded"})
	case errors.Is(err, listing.ErrNotFound), errors.Is(err, listing.ErrNotFound):
		WriteProblem(w, r, Problem{Status: http.StatusNotFound, Title: "not found"})
	default:
		telemetry.Logger(r.Context()).Error("unhandled video error", slog.Any("error", err))
		WriteProblem(w, r, Problem{Status: http.StatusInternalServerError, Title: "internal server error"})
	}
}
