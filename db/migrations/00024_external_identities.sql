-- +goose Up
ALTER TABLE users ALTER COLUMN phone_e164 DROP NOT NULL;
CREATE TABLE external_identities (
 provider text NOT NULL CHECK (provider IN ('google','microsoft','apple')),
 issuer text NOT NULL,
 subject text NOT NULL,
 user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 email text,
 created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(provider,issuer,subject),
 UNIQUE(user_id,provider)
);
CREATE TABLE external_login_exchanges (
 token_hash bytea PRIMARY KEY,
 expires_at timestamptz NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX external_login_exchanges_expiry_idx ON external_login_exchanges(expires_at);
-- +goose StatementBegin
CREATE FUNCTION require_host_contact() RETURNS trigger AS $$
BEGIN
 IF NEW.status IN ('in_review','published') AND NOT EXISTS (
  SELECT 1 FROM users WHERE id=NEW.host_id AND status='active'
  AND phone_e164 IS NOT NULL AND phone_verified_at IS NOT NULL
 ) THEN RAISE EXCEPTION 'A verified host phone is required' USING ERRCODE='23514', CONSTRAINT='spaces_host_phone_verified';
 END IF;
 RETURN NEW;
END $$ LANGUAGE plpgsql;
-- +goose StatementEnd
CREATE TRIGGER spaces_require_host_contact BEFORE INSERT OR UPDATE OF status ON spaces
 FOR EACH ROW EXECUTE FUNCTION require_host_contact();
-- +goose Down
DROP TRIGGER spaces_require_host_contact ON spaces;
DROP FUNCTION require_host_contact();
-- Refuse rollback if external-only accounts still exist; never delete users.
ALTER TABLE users ALTER COLUMN phone_e164 SET NOT NULL;
DROP TABLE external_login_exchanges;
DROP TABLE external_identities;
