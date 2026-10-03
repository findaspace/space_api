-- name: CreateSpace :one
INSERT INTO spaces (
    host_id, slug, space_type, rental_mode, title, description,
    base_price_minor, currency, price_period, deposit_minor, advance_months,
    max_occupancy, latitude, longitude, locality, region, country,
    landmark, digital_address, amenities, attributes
) VALUES (
    $1, $2, $3, $4, $5, $6,
    $7, $8, $9, $10, $11,
    $12, $13, $14, $15, $16, $17,
    $18, $19, $20, $21
)
RETURNING *;

-- name: GetSpaceForHost :one
SELECT * FROM spaces
WHERE id = @id AND host_id = @host_id;

-- name: GetSpaceByID :one
SELECT * FROM spaces WHERE id = $1;

-- name: GetPublishedSpaceBySlug :one
SELECT * FROM spaces
WHERE slug = @slug AND status = 'published';

-- name: ListSpacesForHost :many
SELECT * FROM spaces
WHERE host_id = @host_id
ORDER BY created_at DESC
LIMIT @page_limit OFFSET @page_offset;


-- name: SpaceSummaries :many
SELECT id, slug, title, latitude, longitude, locality, landmark, digital_address
FROM spaces WHERE id = ANY(@ids::uuid[]);

-- name: UpdateSpace :one
UPDATE spaces SET
    status = 'draft',
    rental_mode      = COALESCE(sqlc.narg('rental_mode'), rental_mode),
    price_period     = COALESCE(sqlc.narg('price_period'), price_period),
    title            = COALESCE(sqlc.narg('title'), title),
    description      = COALESCE(sqlc.narg('description'), description),
    base_price_minor = COALESCE(sqlc.narg('base_price_minor'), base_price_minor),
    deposit_minor    = COALESCE(sqlc.narg('deposit_minor'), deposit_minor),
    advance_months   = COALESCE(sqlc.narg('advance_months'), advance_months),
    max_occupancy    = COALESCE(sqlc.narg('max_occupancy'), max_occupancy),
    latitude         = COALESCE(sqlc.narg('latitude'), latitude),
    longitude        = COALESCE(sqlc.narg('longitude'), longitude),
    locality         = COALESCE(sqlc.narg('locality'), locality),
    region           = COALESCE(sqlc.narg('region'), region),
    landmark         = COALESCE(sqlc.narg('landmark'), landmark),
    digital_address  = COALESCE(sqlc.narg('digital_address'), digital_address),
    amenities        = COALESCE(@amenities::text[], amenities),
    attributes       = COALESCE(@attributes::jsonb, attributes)
WHERE id = @id
  AND host_id = @host_id
  AND status IN ('draft', 'in_review')
RETURNING *;

-- name: SetSpaceStatus :one
UPDATE spaces SET
    status = @next_status,
    published_at = CASE
        WHEN @next_status::space_status = 'published' THEN COALESCE(published_at, now())
        ELSE published_at
    END
WHERE id = @id AND status = @expected_status
RETURNING *;
