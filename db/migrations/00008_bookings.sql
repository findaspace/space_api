-- +goose Up
CREATE TYPE booking_status AS ENUM (
    'held', 'approved', 'confirmed', 'active',
    'completed', 'cancelled', 'declined', 'expired'
);

CREATE TYPE actor_kind AS ENUM ('guest', 'host', 'system', 'admin');

CREATE TABLE bookings (
    id        uuid PRIMARY KEY DEFAULT uuidv7(),
    reference text NOT NULL UNIQUE,

    space_id uuid NOT NULL REFERENCES spaces(id) ON DELETE RESTRICT,
    host_id  uuid NOT NULL REFERENCES users(id)  ON DELETE RESTRICT,
    guest_id uuid NOT NULL REFERENCES users(id)  ON DELETE RESTRICT,

    rental_mode rental_mode NOT NULL,
    status      booking_status NOT NULL DEFAULT 'held',

    starts_on date NOT NULL,
    ends_on   date NOT NULL,
    nights    integer NOT NULL DEFAULT 0,
    months    smallint NOT NULL DEFAULT 0,
    guests    smallint NOT NULL DEFAULT 1,

    currency       text NOT NULL,
    subtotal_minor bigint NOT NULL,
    discount_minor bigint NOT NULL DEFAULT 0,
    fees_minor     bigint NOT NULL DEFAULT 0,
    total_minor    bigint NOT NULL,
    due_now_minor  bigint NOT NULL,
    quote          jsonb NOT NULL,

    idempotency_key text,
    hold_expires_at timestamptz,
    approved_at     timestamptz,
    confirmed_at    timestamptz,
    cancelled_at    timestamptz,
    cancelled_by    actor_kind,
    cancel_reason   text NOT NULL DEFAULT '',
    refund_minor    bigint NOT NULL DEFAULT 0,

    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT bookings_period CHECK (ends_on > starts_on),
    CONSTRAINT bookings_guest_not_host CHECK (guest_id <> host_id),
    CONSTRAINT bookings_guests_positive CHECK (guests >= 1),
    CONSTRAINT bookings_amounts CHECK (
        subtotal_minor >= 0 AND discount_minor >= 0 AND fees_minor >= 0
        AND total_minor >= 0 AND due_now_minor >= 0 AND refund_minor >= 0
    ),
    CONSTRAINT bookings_refund_within_total CHECK (refund_minor <= due_now_minor),
    CONSTRAINT bookings_quote_object CHECK (jsonb_typeof(quote) = 'object'),

    CONSTRAINT bookings_hold_has_expiry CHECK (
        status <> 'held' OR hold_expires_at IS NOT NULL
    ),
    CONSTRAINT bookings_confirmed_has_timestamp CHECK (
        status NOT IN ('confirmed', 'active', 'completed') OR confirmed_at IS NOT NULL
    ),
    CONSTRAINT bookings_cancelled_has_actor CHECK (
        status NOT IN ('cancelled', 'declined')
        OR (cancelled_at IS NOT NULL AND cancelled_by IS NOT NULL)
    ),
    CONSTRAINT bookings_nightly_shape CHECK (
        rental_mode <> 'nightly' OR (nights > 0 AND months = 0)
    ),
    CONSTRAINT bookings_term_shape CHECK (
        rental_mode <> 'term' OR (months > 0 AND nights = 0)
    ),

    -- The phase in one constraint. Only live bookings hold dates; cancelled,
    -- declined and expired rows keep their history without blocking anyone.
    CONSTRAINT bookings_no_overlap EXCLUDE USING gist (
        space_id WITH =,
        (daterange(starts_on, ends_on, '[)')) WITH &&
    ) WHERE (status IN ('held', 'approved', 'confirmed', 'active'))
);

-- Two taps on Book must produce one booking. The index is the check; there is
-- no safe way to look first and insert second.
CREATE UNIQUE INDEX bookings_idempotency_idx
    ON bookings (guest_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL;

CREATE INDEX bookings_guest_idx ON bookings (guest_id, created_at DESC);
CREATE INDEX bookings_host_idx  ON bookings (host_id, created_at DESC);
CREATE INDEX bookings_space_idx ON bookings (space_id, starts_on);
CREATE INDEX bookings_expiring_idx ON bookings (hold_expires_at) WHERE status = 'held';

CREATE TRIGGER bookings_set_updated_at
    BEFORE UPDATE ON bookings
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- +goose Down
DROP TABLE IF EXISTS bookings;
DROP TYPE IF EXISTS actor_kind;
DROP TYPE IF EXISTS booking_status;
