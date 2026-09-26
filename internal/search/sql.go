package search

import (
	"fmt"
	"strings"
)

// geoExpr must match migration 00004's index expression character for
// character. If it drifts, the GiST index stops being used and the query
// silently becomes a sequential scan over every listing.
const geoExpr = `ST_SetSRID(ST_MakePoint(s.longitude, s.latitude), 4326)::geography`

const (
	priorMean   = 3.5 // what an unrated listing is assumed to be worth
	priorWeight = 5   // how many reviews it takes to pull away from the prior
)

// build assembles the statement and its arguments for one query.
func build(q Query) (string, []any) {
	b := &builder{}

	where := []string{"s.status = 'published'"}

	if q.Mode != "" {
		where = append(where, "s.rental_mode = "+b.arg(q.Mode)+"::rental_mode")
	}
	if len(q.Types) > 0 {
		where = append(where, "s.space_type::text = ANY("+b.arg(q.Types)+")")
	}
	if q.MinPrice != nil {
		where = append(where, "s.base_price_minor >= "+b.arg(*q.MinPrice))
	}
	if q.MaxPrice != nil {
		where = append(where, "s.base_price_minor <= "+b.arg(*q.MaxPrice))
	}
	if q.Guests > 0 {
		where = append(where, "s.max_occupancy >= "+b.arg(q.Guests))
	}
	if len(q.Amenities) > 0 {
		// @> is containment: the listing must have all of them, not any.
		where = append(where, "s.amenities @> "+b.arg(q.Amenities))
	}
	if len(q.Attributes) > 0 {
		// jsonb_path_ops indexes exactly this operator, and nothing else.
		where = append(where, "s.attributes @> "+b.arg(q.Attributes))
	}
	if q.Locality != "" {
		where = append(where, "s.locality ILIKE "+b.arg("%"+q.Locality+"%"))
	}
	if q.Text != "" {
		pattern := b.arg("%" + q.Text + "%")
		where = append(where, "(s.title ILIKE "+pattern+" OR s.locality ILIKE "+pattern+")")
	}

	// Geography, not geometry: ST_DWithin on geography takes metres directly
	// and accounts for the curve of the earth, so no projection is needed.
	distanceExpr := "NULL::int"
	if q.Lat != nil && q.Lng != nil {
		point := b.argf("ST_SetSRID(ST_MakePoint(%s, %s), 4326)::geography", *q.Lng, *q.Lat)
		radius := b.arg(q.RadiusM)

		where = append(where, fmt.Sprintf("ST_DWithin(%s, %s, %s)", geoExpr, point, radius))
		distanceExpr = fmt.Sprintf("ST_Distance(%s, %s)::int", geoExpr, point)
	}

	if q.CheckIn != nil && q.CheckOut != nil {
		period := b.argf("daterange(%s, %s, '[)')", *q.CheckIn, *q.CheckOut)

		// The live-booking list must match the EXCLUDE predicate in migration
		// 00008, or search will offer dates the booking engine then refuses.
		where = append(where, `NOT EXISTS (
			SELECT 1 FROM bookings bk
			WHERE bk.space_id = s.id
			  AND bk.status IN ('held','approved','confirmed','active')
			  AND daterange(bk.starts_on, bk.ends_on, '[)') && `+period+`)`)

		where = append(where, `NOT EXISTS (
			SELECT 1 FROM availability_blocks ab
			WHERE ab.space_id = s.id
			  AND daterange(ab.starts_on, ab.ends_on, '[)') && `+period+`)`)
	}

	spec := sortSpecs[q.Sort]
	scoreExpr := scoreSQL(b, q, distanceExpr)

	inner := fmt.Sprintf(`
		SELECT
			s.id, s.slug, s.title, s.space_type::text, s.rental_mode::text,
			s.base_price_minor AS price_minor, s.currency, s.price_period::text,
			s.locality, s.latitude, s.longitude,
			s.photo_count, s.review_count,
			CASE WHEN s.review_count = 0 THEN 0
			     ELSE round(s.rating_sum::numeric / s.review_count, 2)::float8 END AS rating,
			COALESCE(cover.object_key, '') AS cover_key,
			COALESCE(walk.name, '') AS landmark_name,
			COALESCE(walk.walk_seconds, 0) AS walk_seconds,
			%s AS distance_m,
			EXTRACT(EPOCH FROM s.published_at)::bigint AS published_epoch,
			%s AS score
		FROM spaces s
		LEFT JOIN LATERAL (
			SELECT COALESCE(NULLIF(card_key, ''), object_key) AS object_key
			FROM space_media
			WHERE space_id = s.id AND status = 'ready'
			ORDER BY position, created_at
			LIMIT 1
		) cover ON true
		LEFT JOIN LATERAL (
			SELECT l.name, sl.walk_seconds
			FROM space_landmarks sl
			JOIN landmarks l ON l.id = sl.landmark_id
			WHERE sl.space_id = s.id AND l.active
			ORDER BY sl.walk_seconds
			LIMIT 1
		) walk ON true
		WHERE %s`,
		distanceExpr, scoreExpr, strings.Join(where, "\n  AND "))
	outer := "SELECT * FROM (" + inner + "\n) q"

	// The cursor compares the row value (sort key, id). Postgres evaluates that
	// as a tuple, which is exact at ties and is what makes the page boundary
	// stable when many listings share a score or a price.
	if !q.Cursor.IsZero() {
		op := ">"
		if spec.desc {
			op = "<"
		}
		outer += fmt.Sprintf("\nWHERE (q.%s, q.id) %s (%s, %s)",
			spec.expr, op, b.arg(q.Cursor.Value), b.arg(q.Cursor.ID))
	}

	direction := "ASC"
	if spec.desc {
		direction = "DESC"
	}
	// NULLS LAST keeps unrankable rows (a distance sort with no coordinates on
	// the row) from crowding the top.
	outer += fmt.Sprintf("\nORDER BY q.%s %s NULLS LAST, q.id %s\nLIMIT %s",
		spec.expr, direction, direction, b.arg(q.Limit))

	return outer, b.args
}

// scoreSQL builds the ranking expression. Every component is an integer in a
// fixed band, so the total is comparable across queries and exact as a cursor.
func scoreSQL(b *builder, q Query, distanceExpr string) string {
	parts := []string{}

	// Proximity, 0 to 4000, decaying linearly across the search radius. Only
	// applies when the searcher told us where they are.
	if q.Lat != nil && q.Lng != nil {
		radius := b.arg(float64(q.RadiusM))
		parts = append(parts, fmt.Sprintf(
			"GREATEST(0, 4000 - ((%s)::float8 / %s * 4000))::bigint", distanceExpr, radius))
	}

	// Quality, 0 to 3000, as a Bayesian average. A lone five-star review sits
	// near the prior rather than at the top; fifty good reviews earn the top.
	parts = append(parts, fmt.Sprintf(
		"((s.rating_sum + %g * %d)::float8 / (s.review_count + %d) * 600)::bigint",
		priorMean, priorWeight, priorWeight))

	// Freshness, 0 to 1000, decaying over 90 days, so a new listing gets a
	// chance to earn its first booking.
	parts = append(parts,
		"GREATEST(0, 1000 - (EXTRACT(EPOCH FROM (now() - s.published_at)) / 86400 / 90 * 1000))::bigint")

	// Completeness, 250 per photo to a ceiling of 1000. Listings with photos
	// convert; this pays hosts for doing the work.
	parts = append(parts, "LEAST(1000, s.photo_count * 250)::bigint")

	return "(" + strings.Join(parts, "\n\t\t\t\t+ ") + ")"
}

// RefreshStatsSQL recomputes the denormalized search columns. Bounded staleness
// by design: the worker runs it on a schedule instead of every write path
// remembering to maintain a counter.
const RefreshStatsSQL = `
UPDATE spaces s SET
	review_count = stats.n,
	rating_sum   = stats.total,
	photo_count  = stats.photos
FROM (
	SELECT
		sp.id,
		COALESCE(r.n, 0)      AS n,
		COALESCE(r.total, 0)  AS total,
		COALESCE(m.n, 0)      AS photos
	FROM spaces sp
	LEFT JOIN LATERAL (
		SELECT count(*) AS n, COALESCE(sum(rating), 0) AS total
		FROM reviews
		WHERE space_id = sp.id AND author_role = 'guest' AND revealed_at IS NOT NULL
	) r ON true
	LEFT JOIN LATERAL (
		SELECT count(*) AS n FROM space_media
		WHERE space_id = sp.id AND status = 'ready'
	) m ON true
) stats
WHERE stats.id = s.id
  AND (s.review_count IS DISTINCT FROM stats.n
    OR s.rating_sum   IS DISTINCT FROM stats.total
    OR s.photo_count  IS DISTINCT FROM stats.photos)`
