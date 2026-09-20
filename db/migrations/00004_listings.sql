-- +goose Up
CREATE TYPE rental_mode AS ENUM ('nightly', 'term');
CREATE TYPE price_period AS ENUM ('night', 'week', 'month', 'year');
CREATE TYPE space_status AS ENUM ('draft', 'in_review', 'published', 'suspended', 'archived');
CREATE TYPE space_type AS ENUM (
    'room', 'self_contained', 'chamber_and_hall', 'apartment', 'house',
    'boys_quarters', 'hostel_bed', 'shop', 'office', 'warehouse',
    'event_space', 'land', 'parking'
);

CREATE TABLE spaces (
    id               uuid PRIMARY KEY DEFAULT uuidv7(),
    host_id          uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    slug             text NOT NULL UNIQUE,
    space_type       space_type NOT NULL,
    rental_mode      rental_mode NOT NULL,
    status           space_status NOT NULL DEFAULT 'draft',

    title            text NOT NULL,
    description      text NOT NULL DEFAULT '',

    base_price_minor bigint NOT NULL,
    currency         char(3) NOT NULL DEFAULT 'GHS',
    price_period     price_period NOT NULL,
    deposit_minor    bigint NOT NULL DEFAULT 0,
    advance_months   smallint NOT NULL DEFAULT 0,
    max_occupancy    smallint NOT NULL DEFAULT 1,

    latitude         double precision NOT NULL,
    longitude        double precision NOT NULL,
    locality         text NOT NULL,
    region           text NOT NULL DEFAULT '',
    country          char(2) NOT NULL DEFAULT 'GH',
    landmark         text NOT NULL DEFAULT '',
    digital_address  text NOT NULL DEFAULT '',

    amenities        text[] NOT NULL DEFAULT '{}',
    attributes       jsonb NOT NULL DEFAULT '{}'::jsonb,

    published_at     timestamptz,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT spaces_lat_range        CHECK (latitude BETWEEN -90 AND 90),
    CONSTRAINT spaces_lng_range        CHECK (longitude BETWEEN -180 AND 180),
    CONSTRAINT spaces_price_positive   CHECK (base_price_minor > 0),
    CONSTRAINT spaces_deposit_nonneg   CHECK (deposit_minor >= 0),
    CONSTRAINT spaces_advance_range    CHECK (advance_months BETWEEN 0 AND 24),
    CONSTRAINT spaces_occupancy_range  CHECK (max_occupancy BETWEEN 1 AND 500),
    CONSTRAINT spaces_currency_upper   CHECK (currency = upper(currency)),
    CONSTRAINT spaces_title_not_blank  CHECK (length(btrim(title)) >= 8),
    CONSTRAINT spaces_attributes_object CHECK (jsonb_typeof(attributes) = 'object'),

    CONSTRAINT spaces_digital_address_shape CHECK (
        digital_address = '' OR digital_address ~ '^[A-Z][A-Z0-9]-[0-9]{3,5}-[0-9]{3,5}$'
    ),

    CONSTRAINT spaces_period_matches_mode CHECK (
        (rental_mode = 'nightly' AND price_period IN ('night', 'week')) OR
        (rental_mode = 'term'    AND price_period IN ('month', 'year'))
    ),

    CONSTRAINT spaces_mode_allowed_for_type CHECK (
        CASE
            WHEN space_type IN ('shop', 'office', 'warehouse', 'land')
                THEN rental_mode = 'term'
            WHEN space_type = 'event_space'
                THEN rental_mode = 'nightly'
            ELSE true
        END
    ),

    CONSTRAINT spaces_published_has_timestamp CHECK (
        status <> 'published' OR published_at IS NOT NULL
    ),

    CONSTRAINT spaces_advance_only_for_term CHECK (
        rental_mode = 'term' OR advance_months = 0
    )
);

CREATE INDEX spaces_host_idx ON spaces (host_id, created_at DESC);

CREATE INDEX spaces_live_idx ON spaces (published_at DESC)
    WHERE status = 'published';

CREATE INDEX spaces_live_type_idx ON spaces (space_type, rental_mode, base_price_minor)
    WHERE status = 'published';

CREATE INDEX spaces_amenities_idx  ON spaces USING gin (amenities);
CREATE INDEX spaces_attributes_idx ON spaces USING gin (attributes jsonb_path_ops);
CREATE INDEX spaces_title_trgm_idx ON spaces USING gin (title gin_trgm_ops);
CREATE INDEX spaces_locality_trgm_idx ON spaces USING gin (locality gin_trgm_ops);

-- Every radius query MUST repeat this expression exactly or the planner will
-- fall back to a sequential scan.
CREATE INDEX spaces_geo_idx ON spaces
    USING gist ((ST_SetSRID(ST_MakePoint(longitude, latitude), 4326)::geography));

CREATE TRIGGER spaces_set_updated_at
    BEFORE UPDATE ON spaces
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- +goose Down
DROP TABLE IF EXISTS spaces;
DROP TYPE IF EXISTS space_type;
DROP TYPE IF EXISTS space_status;
DROP TYPE IF EXISTS price_period;
DROP TYPE IF EXISTS rental_mode;
