package httpapi

import (
	"net/http"
)

// completeness answers the host's listing screen: how finished is this, and
// what is the single best thing to add next.
//
// It is a host-only view. A visitor seeing "this listing is 40% complete" would
// read it as a warning about the place rather than a nudge to the owner.
func (h listingHandler) completeness(w http.ResponseWriter, r *http.Request) {
	p, id, ok := principalAndID(w, r)
	if !ok {
		return
	}

	score, gaps, err := h.svc.Completeness(r.Context(), id, p.UserID)
	if err != nil {
		writeListingError(w, r, err)
		return
	}

	// Gaps arrive ordered by what each is worth. Three is the ceiling on the
	// screen: a list of eleven things to do is a list nobody starts.
	out := make([]map[string]any, 0, len(gaps))
	for _, g := range gaps {
		out = append(out, map[string]any{
			"key":   g.Key,
			"label": g.Label,
			"worth": g.Worth,
		})
	}

	WriteJSON(w, r, http.StatusOK, map[string]any{
		"score": score,
		"gaps":  out,
	})
}
