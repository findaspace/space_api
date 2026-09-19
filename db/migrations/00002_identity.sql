-- +goose Up
CREATE TYPE user_status AS ENUM ('active', 'suspended', 'deleted');
CREATE TYPE user_role AS ENUM ('guest', 'host', 'admin', 'support');
CREATE TYPE otp_purpose AS ENUM ('login', 'phone_change');

CREATE TABLE users (
    id                uuid PRIMARY KEY DEFAULT uuidv7(),
    phone_e164        text NOT NULL UNIQUE,
    email             citext UNIQUE,
    display_name      text NOT NULL DEFAULT '',
    status            user_status NOT NULL DEFAULT 'active',
    phone_verified_at timestamptz,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT users_phone_e164_format CHECK (phone_e164 ~ '^\+[1-9][0-9]{6,14}$')
);

CREATE TABLE user_roles (
    user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role       user_role NOT NULL,
    granted_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, role)
);

CREATE TABLE otp_challenges (
    id           uuid PRIMARY KEY DEFAULT uuidv7(),
    phone_e164   text NOT NULL,
    purpose      otp_purpose NOT NULL DEFAULT 'login',
    code_hash    bytea NOT NULL,
    attempts     smallint NOT NULL DEFAULT 0,
    max_attempts smallint NOT NULL DEFAULT 5,
    expires_at   timestamptz NOT NULL,
    consumed_at  timestamptz,
    request_ip   text NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX otp_challenges_phone_recent_idx
    ON otp_challenges (phone_e164, created_at DESC);

CREATE TABLE sessions (
    id             uuid PRIMARY KEY DEFAULT uuidv7(),
    user_id        uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    family_id      uuid NOT NULL,
    refresh_hash   bytea NOT NULL UNIQUE,
    user_agent     text NOT NULL DEFAULT '',
    request_ip     text NOT NULL DEFAULT '',
    expires_at     timestamptz NOT NULL,
    rotated_at     timestamptz,
    revoked_at     timestamptz,
    revoked_reason text,
    created_at     timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX sessions_user_active_idx ON sessions (user_id) WHERE revoked_at IS NULL;
CREATE INDEX sessions_family_idx ON sessions (family_id);

-- +goose StatementBegin
CREATE FUNCTION set_updated_at() RETURNS trigger AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER users_set_updated_at
    BEFORE UPDATE ON users
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- +goose Down
DROP TRIGGER IF EXISTS users_set_updated_at ON users;
DROP FUNCTION IF EXISTS set_updated_at();
DROP TABLE IF EXISTS sessions;
DROP TABLE IF EXISTS otp_challenges;
DROP TABLE IF EXISTS user_roles;
DROP TABLE IF EXISTS users;
DROP TYPE IF EXISTS otp_purpose;
DROP TYPE IF EXISTS user_role;
DROP TYPE IF EXISTS user_status;
