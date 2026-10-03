package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	// "time"

	"github.com/samnart1/space_api/internal/platform/telemetry"
	"github.com/samnart1/space_api/internal/search"
)

type searchHandler struct{ svc *search.Service }

func queryFloat(r *http.Request, key string) *float64 {
	raw := r.URL.Query().Get(key)
	if raw == "" {
		return nil
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return nil
	}
	return &v
}

func queryMoney(r *http.Request, key string) *int64 {
	raw := r.URL.Query().Get(key)
	if raw == "" {
		return nil
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || v < 0 {
		return nil
	}
	return &v
}

func queryList(r *http.Request, key string) []string {
	raw := r.URL.Query().Get(key)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func (h searchHandler) search(w http.ResponseWriter, r *http.Request) {
	params := r.URL.Query()

	q := search.Query{
		Text:      params.Get("q"),
		Types:     queryList(r, "type"),
		Mode:      params.Get("mode"),
		Period:    params.Get("price_period"),
		Locality:  params.Get("locality"),
		MinPrice:  queryMoney(r, "min_price"),
		MaxPrice:  queryMoney(r, "max_price"),
		Guests:    int(clampQueryInt(r, "guests", 0, 0, 500)),
		Amenities: queryList(r, "amenities"),
		Lat:       queryFloat(r, "lat"),
		Lng:       queryFloat(r, "lng"),
		RadiusM:   int(clampQueryInt(r, "radius_m", 0, 0, search.MaxRadiusM)),
		Sort:      search.Sort(params.Get("sort")),
		Limit:     clampQueryInt(r, "limit", search.DefaultLimit, 1, search.MaxLimit),
	}

	// Attribute filters arrive as attr.bedrooms=2. Values are parsed as numbers
	// or booleans where possible so @> matches the jsonb the listing stored:
	// the document holds 2, and "2" would never match it.
	attrs := map[string]any{}
	for key, values := range params {
		name, ok := strings.CutPrefix(key, "attr.")
		if !ok || len(values) == 0 || name == "" {
			continue
		}
		attrs[name] = coerceAttr(values[0])
	}
	if len(attrs) > 0 {
		q.Attributes = attrs
	}

	if in, ok := queryDate(r, "check_in"); ok {
		q.CheckIn = &in
	}
	if out, ok := queryDate(r, "check_out"); ok {
		q.CheckOut = &out
	}

	cursor, err := search.ParseCursor(params.Get("cursor"))
	if err != nil {
		WriteProblem(w, r, Problem{Status: http.StatusUnprocessableEntity, Title: "invalid cursor"})
		return
	}
	q.Cursor = cursor

	page, err := h.svc.Search(r.Context(), q)
	if err != nil {
		writeSearchError(w, r, err)
		return
	}
	WriteJSON(w, r, http.StatusOK, page)
}

func coerceAttr(raw string) any {
	if raw == "true" || raw == "false" {
		return raw == "true"
	}
	if n, err := strconv.ParseInt(raw, 10, 64); err == nil {
		return n
	}
	if f, err := strconv.ParseFloat(raw, 64); err == nil {
		return f
	}
	return raw
}

func writeSearchError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, search.ErrBadFilter):
		WriteProblem(w, r, Problem{Status: http.StatusUnprocessableEntity, Title: "invalid rental mode or price period"})
	case errors.Is(err, search.ErrBadSort):
		WriteProblem(w, r, Problem{Status: http.StatusUnprocessableEntity, Title: "unknown sort",
			Errors: []FieldError{{Field: "sort", Code: "unknown",
				Detail: "relevance, price_asc, price_desc, distance or newest"}}})
	case errors.Is(err, search.ErrBadRange):
		WriteProblem(w, r, Problem{Status: http.StatusUnprocessableEntity, Title: "invalid dates",
			Errors: []FieldError{{Field: "check_out", Code: "invalid",
				Detail: "must be after check_in"}}})
	case errors.Is(err, search.ErrBadCursor):
		WriteProblem(w, r, Problem{Status: http.StatusUnprocessableEntity, Title: "invalid cursor"})
	default:
		telemetry.Logger(r.Context()).Error("unhandled search error", slog.Any("error", err))
		WriteProblem(w, r, Problem{Status: http.StatusInternalServerError, Title: "internal server error"})
	}
}

// var _ = time.Time{}
