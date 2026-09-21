-- +goose Up
CREATE TYPE payout_channel AS ENUM ('mobile_money', 'bank');
CREATE TYPE payout_status AS ENUM ('pending', 'processing', 'paid', 'failed', 'reversed');

CREATE TABLE payout_accounts (
    id             uuid PRIMARY KEY DEFAULT uuidv7(),
    user_id        uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider       text NOT NULL DEFAULT 'paystack',
    recipient_code text NOT NULL DEFAULT '',
    channel        payout_channel NOT NULL,
    bank_code      text NOT NULL,
    account_number text NOT NULL,
    account_name   text NOT NULL,
    currency       text NOT NULL DEFAULT 'GHS',
    verified_at    timestamptz,
    is_default     boolean NOT NULL DEFAULT true,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT payout_accounts_name_not_blank CHECK (length(btrim(account_name)) >= 2),
    CONSTRAINT payout_accounts_number_not_blank CHECK (length(btrim(account_number)) >= 6),
    CONSTRAINT payout_accounts_unique UNIQUE (user_id, channel, bank_code, account_number)
);

CREATE UNIQUE INDEX payout_accounts_one_default
    ON payout_accounts (user_id) WHERE is_default;

CREATE TABLE payouts (
    id             uuid PRIMARY KEY DEFAULT uuidv7(),
    host_id        uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    account_id     uuid NOT NULL REFERENCES payout_accounts(id) ON DELETE RESTRICT,
    reference      text NOT NULL UNIQUE,
    transfer_code  text NOT NULL DEFAULT '',
    status         payout_status NOT NULL DEFAULT 'pending',
    currency       text NOT NULL,
    amount_minor   bigint NOT NULL,
    failure_reason text NOT NULL DEFAULT '',
    settled_at     timestamptz,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT payouts_amount_positive CHECK (amount_minor > 0),
    CONSTRAINT payouts_paid_has_timestamp CHECK (status <> 'paid' OR settled_at IS NOT NULL)
);

-- Two withdrawal requests racing on one balance is the classic double-spend.
-- The database settles it; no service-level check can.
CREATE UNIQUE INDEX payouts_one_open_per_host
    ON payouts (host_id) WHERE status IN ('pending', 'processing');

CREATE INDEX payouts_host_idx ON payouts (host_id, created_at DESC);

CREATE TRIGGER payout_accounts_set_updated_at
    BEFORE UPDATE ON payout_accounts
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TRIGGER payouts_set_updated_at
    BEFORE UPDATE ON payouts
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- +goose Down
DROP TABLE IF EXISTS payouts;
DROP TABLE IF EXISTS payout_accounts;
DROP TYPE IF EXISTS payout_status;
DROP TYPE IF EXISTS payout_channel;
