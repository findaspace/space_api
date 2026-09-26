package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/samnart1/space_api/internal/listing"
	"github.com/samnart1/space_api/internal/platform/telemetry"
)

type listingHandler struct{ svc *listing.Service }

type locationBody struct {
	Latitude       float64 `json:"latitude"`
	Longitude      float64 `json:"longitude"`
	Locality       string  `json:"locality"`
	Region         string  `json:"region"`
	Country        string  `json:"country"`
	Landmark       string  `json:"landmark"`
	DigitalAddress string  `json:"digital_address"`
}

type createSpaceBody struct {
	SpaceType      listing.SpaceType   `json:"space_type"`
	RentalMode     listing.RentalMode  `json:"rental_mode"`
	Title          string              `json:"title"`
	Description    string              `json:"description"`
	BasePriceMinor int64               `json:"base_price_minor"`
	Currency       string              `json:"currency"`
	PricePeriod    listing.PricePeriod `json:"price_period"`
	DepositMinor   int64               `json:"deposit_minor"`
	AdvanceMonths  int16               `json:"advance_months"`
	MaxOccupancy   int16               `json:"max_occupancy"`
	Location       locationBody        `json:"location"`
	Amenities      []string            `json:"amenities"`
	Attributes     map[string]any      `json:"attributes"`
}

type patchSpaceBody struct {
	Title          *string        `json:"title"`
	Description    *string        `json:"description"`
	BasePriceMinor *int64         `json:"base_price_minor"`
	DepositMinor   *int64         `json:"deposit_minor"`
	AdvanceMonths  *int16         `json:"advance_months"`
	MaxOccupancy   *int16         `json:"max_occupancy"`
	Latitude       *float64       `json:"latitude"`
	Longitude      *float64       `json:"longitude"`
	Locality       *string        `json:"locality"`
	Region         *string        `json:"region"`
	Landmark       *string        `json:"landmark"`
	DigitalAddress *string        `json:"digital_address"`
	Amenities      []string       `json:"amenities"`
	Attributes     map[string]any `json:"attributes"`
}

type spaceResponse struct {
	ID             string              `json:"id"`
	Slug           string              `json:"slug"`
	HostID         string              `json:"host_id"`
	SpaceType      listing.SpaceType   `json:"space_type"`
	RentalMode     listing.RentalMode  `json:"rental_mode"`
	Status         listing.Status      `json:"status"`
	Title          string              `json:"title"`
	Description    string              `json:"description"`
	BasePriceMinor int64               `json:"base_price_minor"`
	Currency       string              `json:"currency"`
	PricePeriod    listing.PricePeriod `json:"price_period"`
	DepositMinor   int64               `json:"deposit_minor"`
	AdvanceMonths  int16               `json:"advance_months"`
	MaxOccupancy   int16               `json:"max_occupancy"`
	Location       listing.Location    `json:"location"`
	Amenities      []string            `json:"amenities"`
	Attributes     map[string]any      `json:"attributes"`
	PublishedAt    *time.Time          `json:"published_at"`
	CreatedAt      time.Time           `json:"created_at"`
	UpdatedAt      time.Time           `json:"updated_at"`
}

func toSpaceResponse(s listing.Space) spaceResponse {
	return spaceResponse{
		ID: s.ID.String(), Slug: s.Slug, HostID: s.HostID.String(),
		SpaceType: s.Type, RentalMode: s.Mode, Status: s.Status,
		Title: s.Title, Description: s.Description,
		BasePriceMinor: s.BasePriceMinor, Currency: s.Currency,
		PricePeriod: s.PricePeriod, DepositMinor: s.DepositMinor,
		AdvanceMonths: s.AdvanceMonths, MaxOccupancy: s.MaxOccupancy,
		Location: s.Location, Amenities: s.Amenities, Attributes: s.Attributes,
		PublishedAt: s.PublishedAt, CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt,
	}
}

func (h listingHandler) spaceTypes(w http.ResponseWriter, r *http.Request) {
	WriteJSON(w, r, http.StatusOK, map[string]any{
		"types":     listing.Schemas(),
		"amenities": listing.Amenities(),
	})
}

func (h listingHandler) create(w http.ResponseWriter, r *http.Request) {
	p, ok := PrincipalFrom(r.Context())
	if !ok {
		unauthorized(w, r, "missing bearer token")
		return
	}

	var body createSpaceBody
	if !decodeJSON(w, r, &body) {
		return
	}

	sp, err := h.svc.Create(r.Context(), listing.CreateInput{
		HostID:         p.UserID,
		Type:           body.SpaceType,
		Mode:           body.RentalMode,
		Title:          body.Title,
		Description:    body.Description,
		BasePriceMinor: body.BasePriceMinor,
		Currency:       body.Currency,
		PricePeriod:    body.PricePeriod,
		DepositMinor:   body.DepositMinor,
		AdvanceMonths:  body.AdvanceMonths,
		MaxOccupancy:   body.MaxOccupancy,
		Location: listing.Location{
			Latitude:       body.Location.Latitude,
			Longitude:      body.Location.Longitude,
			Locality:       body.Location.Locality,
			Region:         body.Location.Region,
			Country:        body.Location.Country,
			Landmark:       body.Location.Landmark,
			DigitalAddress: body.Location.DigitalAddress,
		},
		Amenities:  body.Amenities,
		Attributes: body.Attributes,
	})
	if err != nil {
		writeListingError(w, r, err)
		return
	}
	WriteJSON(w, r, http.StatusCreated, toSpaceResponse(sp))
}

func (h listingHandler) list(w http.ResponseWriter, r *http.Request) {
	p, ok := PrincipalFrom(r.Context())
	if !ok {
		unauthorized(w, r, "missing bearer token")
		return
	}

	limit := clampQueryInt(r, "limit", 20, 1, 100)
	offset := clampQueryInt(r, "offset", 0, 0, 100000)

	spaces, err := h.svc.ListForHost(r.Context(), p.UserID, limit, offset)
	if err != nil {
		writeListingError(w, r, err)
		return
	}

	out := make([]spaceResponse, 0, len(spaces))
	for _, s := range spaces {
		out = append(out, toSpaceResponse(s))
	}
	WriteJSON(w, r, http.StatusOK, map[string]any{"spaces": out, "limit": limit, "offset": offset})
}

func (h listingHandler) getOwn(w http.ResponseWriter, r *http.Request) {
	p, id, ok := principalAndID(w, r)
	if !ok {
		return
	}
	sp, err := h.svc.GetForHost(r.Context(), id, p.UserID)
	if err != nil {
		writeListingError(w, r, err)
		return
	}
	WriteJSON(w, r, http.StatusOK, toSpaceResponse(sp))
}

func (h listingHandler) getPublic(w http.ResponseWriter, r *http.Request) {
	sp, err := h.svc.GetPublic(r.Context(), r.PathValue("slug"))
	if err != nil {
		writeListingError(w, r, err)
		return
	}
	// The public shape, never toSpaceResponse: that is the host's view and
	// carries the exact address and the host's id.
	WriteJSON(w, r, http.StatusOK, toPublicSpaceResponse(sp))
}

func (h listingHandler) patch(w http.ResponseWriter, r *http.Request) {
	p, id, ok := principalAndID(w, r)
	if !ok {
		return
	}

	var body patchSpaceBody
	if !decodeJSON(w, r, &body) {
		return
	}

	sp, err := h.svc.Update(r.Context(), id, p.UserID, listing.Patch{
		Title: body.Title, Description: body.Description,
		BasePriceMinor: body.BasePriceMinor, DepositMinor: body.DepositMinor,
		AdvanceMonths: body.AdvanceMonths, MaxOccupancy: body.MaxOccupancy,
		Latitude: body.Latitude, Longitude: body.Longitude,
		Locality: body.Locality, Region: body.Region,
		Landmark: body.Landmark, DigitalAddress: body.DigitalAddress,
		Amenities: body.Amenities, Attributes: body.Attributes,
	})
	if err != nil {
		writeListingError(w, r, err)
		return
	}
	WriteJSON(w, r, http.StatusOK, toSpaceResponse(sp))
}

func (h listingHandler) submit(w http.ResponseWriter, r *http.Request) {
	h.hostAction(w, r, h.svc.Submit)
}
func (h listingHandler) withdraw(w http.ResponseWriter, r *http.Request) {
	h.hostAction(w, r, h.svc.Withdraw)
}
func (h listingHandler) archive(w http.ResponseWriter, r *http.Request) {
	h.hostAction(w, r, h.svc.Archive)
}

func (h listingHandler) hostAction(w http.ResponseWriter, r *http.Request,
	fn func(ctx context.Context, id, hostID uuid.UUID) (listing.Space, error)) {

	p, id, ok := principalAndID(w, r)
	if !ok {
		return
	}
	sp, err := fn(r.Context(), id, p.UserID)
	if err != nil {
		writeListingError(w, r, err)
		return
	}
	WriteJSON(w, r, http.StatusOK, toSpaceResponse(sp))
}

func (h listingHandler) publish(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		WriteProblem(w, r, Problem{Status: http.StatusNotFound, Title: "not found"})
		return
	}
	sp, err := h.svc.Publish(r.Context(), id)
	if err != nil {
		writeListingError(w, r, err)
		return
	}
	WriteJSON(w, r, http.StatusOK, toSpaceResponse(sp))
}

func principalAndID(w http.ResponseWriter, r *http.Request) (Principal, uuid.UUID, bool) {
	p, ok := PrincipalFrom(r.Context())
	if !ok {
		unauthorized(w, r, "missing bearer token")
		return Principal{}, uuid.Nil, false
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		WriteProblem(w, r, Problem{Status: http.StatusNotFound, Title: "not found"})
		return Principal{}, uuid.Nil, false
	}
	return p, id, true
}

func clampQueryInt(r *http.Request, key string, def, lo, hi int32) int32 {
	raw := r.URL.Query().Get(key)
	if raw == "" {
		return def
	}
	n, err := strconv.ParseInt(raw, 10, 32)
	if err != nil {
		return def
	}
	return min32(max32(int32(n), lo), hi)
}

func min32(a, b int32) int32 {
	if a < b {
		return a
	}
	return b
}
func max32(a, b int32) int32 {
	if a > b {
		return a
	}
	return b
}

func writeListingError(w http.ResponseWriter, r *http.Request, err error) {
	var ve *listing.ValidationError
	if errors.As(err, &ve) {
		fields := make([]FieldError, 0, len(ve.Issues))
		for _, i := range ve.Issues {
			fields = append(fields, FieldError{Field: i.Field, Code: i.Code, Detail: i.Detail})
		}
		WriteProblem(w, r, Problem{
			Status: http.StatusUnprocessableEntity,
			Title:  "invalid listing",
			Errors: fields,
		})
		return
	}

	switch {
	case errors.Is(err, listing.ErrNotFound):
		WriteProblem(w, r, Problem{Status: http.StatusNotFound, Title: "not found"})
	case errors.Is(err, listing.ErrBadTransition):
		WriteProblem(w, r, Problem{Status: http.StatusConflict, Title: "invalid status change"})
	case errors.Is(err, listing.ErrConflict):
		WriteProblem(w, r, Problem{
			Status: http.StatusConflict, Title: "listing changed",
			Detail: "reload and try again",
		})
	default:
		telemetry.Logger(r.Context()).Error("unhandled listing error", slog.Any("error", err))
		WriteProblem(w, r, Problem{Status: http.StatusInternalServerError, Title: "internal server error"})
	}
}
