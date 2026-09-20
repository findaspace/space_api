-- +goose Up
CREATE TYPE ledger_account AS ENUM (
    'psp_clearing',      -- asset: captured, not yet settled to our bank
    'bank',              -- asset: settled cash
    'host_payable',      -- liability: owed to hosts
    'platform_revenue',  -- income: our fee
    'refunds_payable'    -- liability: refunds approved, not yet sent
);

CREATE TYPE entry_direction AS ENUM ('debit', 'credit');

CREATE TYPE ledger_kind AS ENUM ('charge', 'refund', 'payout', 'settlement', 'reversal');

CREATE TABLE ledger_transactions (
    id           uuid PRIMARY KEY DEFAULT uuidv7(),
    kind         ledger_kind NOT NULL,
    booking_id   uuid REFERENCES bookings(id) ON DELETE RESTRICT,
    currency     text NOT NULL,
    memo         text NOT NULL DEFAULT '',
    external_ref text,
    created_at   timestamptz NOT NULL DEFAULT now()
);

-- One posting per external event. A replayed webhook must not double the books.
CREATE UNIQUE INDEX ledger_transactions_external_idx
    ON ledger_transactions (kind, external_ref)
    WHERE external_ref IS NOT NULL;

CREATE INDEX ledger_transactions_booking_idx ON ledger_transactions (booking_id);

CREATE TABLE ledger_entries (
    id             uuid PRIMARY KEY DEFAULT uuidv7(),
    transaction_id uuid NOT NULL REFERENCES ledger_transactions(id) ON DELETE RESTRICT,
    account        ledger_account NOT NULL,
    direction      entry_direction NOT NULL,
    amount_minor   bigint NOT NULL,
    party_id       uuid REFERENCES users(id) ON DELETE RESTRICT,
    created_at     timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT ledger_entries_amount_positive CHECK (amount_minor > 0)
);

-- Per-host balances are a sum over this index, never a stored column.
CREATE INDEX ledger_entries_party_idx ON ledger_entries (party_id, account)
    WHERE party_id IS NOT NULL;
CREATE INDEX ledger_entries_transaction_idx ON ledger_entries (transaction_id);

-- +goose StatementBegin
CREATE FUNCTION assert_ledger_balanced() RETURNS trigger AS $$
DECLARE
    imbalance bigint;
BEGIN
    SELECT COALESCE(SUM(
        CASE WHEN direction = 'debit' THEN amount_minor ELSE -amount_minor END
    ), 0)
    INTO imbalance
    FROM ledger_entries
    WHERE transaction_id = NEW.transaction_id;

    IF imbalance <> 0 THEN
        RAISE EXCEPTION 'ledger transaction % is out of balance by %',
            NEW.transaction_id, imbalance
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NULL;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- DEFERRABLE INITIALLY DEFERRED so the check runs once at COMMIT, after every
-- entry in the posting exists. A non-deferred trigger would reject the first
-- row of every transaction, since one entry can never balance on its own.
CREATE CONSTRAINT TRIGGER ledger_entries_balanced
    AFTER INSERT ON ledger_entries
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION assert_ledger_balanced();

-- +goose StatementBegin
CREATE FUNCTION forbid_ledger_mutation() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'ledger entries are append-only; post a reversing entry instead'
        USING ERRCODE = 'restrict_violation';
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER ledger_entries_immutable
    BEFORE UPDATE OR DELETE ON ledger_entries
    FOR EACH ROW EXECUTE FUNCTION forbid_ledger_mutation();

-- +goose Down
DROP TRIGGER IF EXISTS ledger_entries_immutable ON ledger_entries;
DROP TRIGGER IF EXISTS ledger_entries_balanced ON ledger_entries;
DROP FUNCTION IF EXISTS forbid_ledger_mutation();
DROP FUNCTION IF EXISTS assert_ledger_balanced();
DROP TABLE IF EXISTS ledger_entries;
DROP TABLE IF EXISTS ledger_transactions;
DROP TYPE IF EXISTS ledger_kind;
DROP TYPE IF EXISTS entry_direction;
DROP TYPE IF EXISTS ledger_account;
