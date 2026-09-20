-- +goose Up
CREATE TYPE payment_status AS ENUM ('pending', 'processing', 'succeeded', 'failed', 'abandoned');

CREATE TABLE payments (
    id                 uuid PRIMARY KEY DEFAULT uuidv7(),
    booking_id         uuid NOT NULL REFERENCES bookings(id) ON DELETE RESTRICT,
    payer_id           uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    provider           text NOT NULL DEFAULT 'paystack',
    provider_ref       text NOT NULL,
    provider_txn_id    text NOT NULL DEFAULT '',
    access_code        text NOT NULL DEFAULT '',
    authorization_url  text NOT NULL DEFAULT '',
    channel            text NOT NULL DEFAULT '',
    status             payment_status NOT NULL DEFAULT 'pending',
    currency           text NOT NULL,
    amount_minor       bigint NOT NULL,
    provider_fee_minor bigint NOT NULL DEFAULT 0,
    failure_reason     text NOT NULL DEFAULT '',
    paid_at            timestamptz,
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT payments_amount_positive CHECK (amount_minor > 0),
    CONSTRAINT payments_provider_ref_unique UNIQUE (provider, provider_ref),
    CONSTRAINT payments_succeeded_has_timestamp CHECK (
        status <> 'succeeded' OR paid_at IS NOT NULL
    )
);

-- A booking can be paid exactly once, and can have exactly one attempt in
-- flight. Both are enforced here rather than in a service that might be
-- bypassed by a retried webhook.
CREATE UNIQUE INDEX payments_one_success_per_booking
    ON payments (booking_id) WHERE status = 'succeeded';

CREATE UNIQUE INDEX payments_one_open_per_booking
    ON payments (booking_id) WHERE status IN ('pending', 'processing');

CREATE INDEX payments_booking_idx ON payments (booking_id, created_at DESC);

CREATE TABLE webhook_events (
    id           uuid PRIMARY KEY DEFAULT uuidv7(),
    provider     text NOT NULL,
    event_key    text NOT NULL,
    event_type   text NOT NULL,
    payload      jsonb NOT NULL,
    received_at  timestamptz NOT NULL DEFAULT now(),
    processed_at timestamptz,
    error        text NOT NULL DEFAULT '',

    CONSTRAINT webhook_events_unique UNIQUE (provider, event_key)
);

CREATE INDEX webhook_events_unprocessed_idx
    ON webhook_events (received_at) WHERE processed_at IS NULL;

CREATE TRIGGER payments_set_updated_at
    BEFORE UPDATE ON payments
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- +goose Down
DROP TABLE IF EXISTS webhook_events;
DROP TABLE IF EXISTS payments;
DROP TYPE IF EXISTS payment_status;
