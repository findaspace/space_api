-- +goose Up
CREATE TYPE cancellation_policy AS ENUM ('flexible', 'moderate', 'strict');

ALTER TABLE stay_rules
    ADD COLUMN cancellation_policy cancellation_policy NOT NULL DEFAULT 'moderate';

-- Frozen at hold time. A policy change must never alter the refund owed on a
-- booking that already exists.
ALTER TABLE bookings
    ADD COLUMN cancellation_policy cancellation_policy NOT NULL DEFAULT 'moderate';

-- +goose Down
ALTER TABLE bookings DROP COLUMN IF EXISTS cancellation_policy;
ALTER TABLE stay_rules DROP COLUMN IF EXISTS cancellation_policy;
DROP TYPE IF EXISTS cancellation_policy;
