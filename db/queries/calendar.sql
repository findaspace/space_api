-- name: GetStayRules :one
SELECT * FROM stay_rules WHERE space_id = $1;

-- name: UpsertStayRules :one
INSERT INTO stay_rules (
    space_id, min_nights, max_nights,
    weekly_discount_bps, monthly_discount_bps, cleaning_fee_minor, cancellation_policy
) VALUES ($1, $2, $3, $4, $5, $6, sqlc.arg('cancellation_policy')::cancellation_policy)
ON CONFLICT (space_id) DO UPDATE SET
    min_nights           = EXCLUDED.min_nights,
    max_nights           = EXCLUDED.max_nights,
    weekly_discount_bps  = EXCLUDED.weekly_discount_bps,
    monthly_discount_bps = EXCLUDED.monthly_discount_bps,
    cleaning_fee_minor   = EXCLUDED.cleaning_fee_minor,
    cancellation_policy  = EXCLUDED.cancellation_policy
RETURNING *;

-- name: CreateRatePlan :one
INSERT INTO rate_plans (space_id, name, starts_on, ends_on, price_minor, min_nights)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: ListRatePlansInWindow :many
SELECT * FROM rate_plans
WHERE space_id = @space_id AND ends_on > @from_date AND starts_on < @to_date
ORDER BY starts_on;

-- name: ListRatePlans :many
SELECT * FROM rate_plans WHERE space_id = $1 ORDER BY starts_on;

-- name: DeleteRatePlan :execrows
DELETE FROM rate_plans WHERE id = @id AND space_id = @space_id;

-- name: CreateBlock :one
INSERT INTO availability_blocks (space_id, starts_on, ends_on, reason)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: ListBlocksInWindow :many
SELECT * FROM availability_blocks
WHERE space_id = @space_id AND ends_on > @from_date AND starts_on < @to_date
ORDER BY starts_on;

-- name: DeleteBlock :execrows
DELETE FROM availability_blocks WHERE id = @id AND space_id = @space_id;
