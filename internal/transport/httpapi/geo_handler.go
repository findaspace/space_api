package httpapi

import (
	"log/slog"
	"net/http"

	"github.com/samnart1/space_api/internal/geo"
	"github.com/samnart1/space_api/internal/listing"
	"github.com/samnart1/space_api/internal/platform/osrm"
	"github.com/samnart1/space_api/internal/platform/telemetry"
)

type geoHandler struct {
	svc      *geo.Service
	listings *listing.Service
}

// forListing answers the "14 min walk to Zongo Junction" lines on the listing
// screen. Three is the ceiling: a list of every landmark within five
// kilometres is a table nobody reads.
func (h geoHandler) forListing(w http.ResponseWriter, r *http.Request) {
	sp, err := h.listings.GetPublic(r.Context(), r.PathValue("slug"))
	if err != nil {
		writeListingError(w, r, err)
		return
	}

	walks, err := h.svc.ForSpace(r.Context(), sp.ID, 3)
	if err != nil {
		telemetry.Logger(r.Context()).Error("unhandled geo error", slog.Any("error", err))
		WriteProblem(w, r, Problem{Status: http.StatusInternalServerError, Title: "internal server error"})
		return
	}

	// An unrouted listing returns an empty list rather than a 404. The walk
	// times are an enrichment, and their absence is not an error the client
	// should have to handle as one.
	out := make([]map[string]any, 0, len(walks))
	for _, walk := range walks {
		out = append(out, map[string]any{
			"name":     walk.Landmark.Name,
			"kind":     walk.Landmark.Kind,
			"locality": walk.Landmark.Locality,
			"minutes":  osrm.Minutes(walk.Seconds),
			"metres":   walk.Metres,
		})
	}
	WriteJSON(w, r, http.StatusOK, map[string]any{"walks": out})
}
