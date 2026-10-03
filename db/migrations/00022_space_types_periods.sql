-- +goose NO TRANSACTION
-- +goose Up
-- Enum additions must commit before a following migration uses their values.
ALTER TYPE space_type ADD VALUE IF NOT EXISTS 'studio';
ALTER TYPE space_type ADD VALUE IF NOT EXISTS 'football_pitch';
ALTER TYPE space_type ADD VALUE IF NOT EXISTS 'sports_court';
ALTER TYPE space_type ADD VALUE IF NOT EXISTS 'sports_facility';
ALTER TYPE rental_mode ADD VALUE IF NOT EXISTS 'flexible';
ALTER TYPE price_period ADD VALUE IF NOT EXISTS 'hour';
ALTER TYPE price_period ADD VALUE IF NOT EXISTS 'day';
ALTER TYPE price_period ADD VALUE IF NOT EXISTS 'semester';
ALTER TYPE price_period ADD VALUE IF NOT EXISTS 'academic_year';

-- +goose Down
-- PostgreSQL enum values cannot be safely removed from a live database.
-- Roll back application code only after removing/reclassifying new inventory.
