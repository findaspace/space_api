-- +goose Up
CREATE TABLE stay_rules (
    space_id             uuid PRIMARY KEY REFERENCES spaces(id) ON DELETE CASCADE,
    min_nights           smallint NOT NULL DEFAULT 1,
    max_nights           smallint,
    weekly_discount_bps  smallint NOT NULL DEFAULT 0,
    monthly_discount_bps smallint NOT NULL DEFAULT 0,
    cleaning_fee_minor   bigint NOT NULL DEFAULT 0,
    updated_at           timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT stay_rules_min_nights CHECK (min_nights >= 1),
    CONSTRAINT stay_rules_max_nights CHECK (max_nights IS NULL OR max_nights >= min_nights),
    CONSTRAINT stay_rules_weekly_bps CHECK (weekly_discount_bps BETWEEN 0 AND 9000),
    CONSTRAINT stay_rules_monthly_bps CHECK (monthly_discount_bps BETWEEN 0 AND 9000),
    CONSTRAINT stay_rules_cleaning_fee CHECK (cleaning_fee_minor >= 0)
);

CREATE TABLE rate_plans (
    id          uuid PRIMARY KEY DEFAULT uuidv7(),
    space_id    uuid NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    name        text NOT NULL DEFAULT '',
    starts_on   date NOT NULL,
    ends_on     date NOT NULL,
    price_minor bigint NOT NULL,
    min_nights  smallint,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT rate_plans_period CHECK (ends_on > starts_on),
    CONSTRAINT rate_plans_price CHECK (price_minor > 0),
    CONSTRAINT rate_plans_min_nights CHECK (min_nights IS NULL OR min_nights >= 1),

    -- Two price windows covering one night is ambiguous, so it cannot exist.
    -- btree_gist (migration 00001) is what allows mixing = on a uuid with &&
    -- on a range in one index.
    CONSTRAINT rate_plans_no_overlap EXCLUDE USING gist (
        space_id WITH =,
        (daterange(starts_on, ends_on, '[)')) WITH &&
    )
);

CREATE INDEX rate_plans_space_period_idx ON rate_plans (space_id, starts_on, ends_on);

CREATE TABLE availability_blocks (
    id         uuid PRIMARY KEY DEFAULT uuidv7(),
    space_id   uuid NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    starts_on  date NOT NULL,
    ends_on    date NOT NULL,
    reason     text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT availability_blocks_period CHECK (ends_on > starts_on)
);

CREATE INDEX availability_blocks_space_idx
    ON availability_blocks USING gist (
        space_id,
        (daterange(starts_on, ends_on, '[)'))
    );

CREATE TRIGGER stay_rules_set_updated_at
    BEFORE UPDATE ON stay_rules
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TRIGGER rate_plans_set_updated_at
    BEFORE UPDATE ON rate_plans
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- +goose Down
DROP TABLE IF EXISTS availability_blocks;
DROP TABLE IF EXISTS rate_plans;
DROP TABLE IF EXISTS stay_rules;
