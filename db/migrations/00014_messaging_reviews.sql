-- +goose Up
CREATE TABLE threads (
    id              uuid PRIMARY KEY DEFAULT uuidv7(),
    space_id        uuid NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    guest_id        uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    host_id         uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    last_message_at timestamptz NOT NULL DEFAULT now(),
    guest_read_at   timestamptz,
    host_read_at    timestamptz,
    flagged         boolean NOT NULL DEFAULT false,
    created_at      timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT threads_participants_differ CHECK (guest_id <> host_id),
    CONSTRAINT threads_one_per_guest UNIQUE (space_id, guest_id)
);

CREATE INDEX threads_guest_inbox_idx ON threads (guest_id, last_message_at DESC);
CREATE INDEX threads_host_inbox_idx  ON threads (host_id, last_message_at DESC);

CREATE TABLE messages (
    id         uuid PRIMARY KEY DEFAULT uuidv7(),
    thread_id  uuid NOT NULL REFERENCES threads(id) ON DELETE CASCADE,
    sender_id  uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    body       text NOT NULL,
    redacted   boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT messages_body_length CHECK (length(body) BETWEEN 1 AND 4000)
);

-- Keyset pagination walks this index backwards from a cursor id.
CREATE INDEX messages_thread_keyset_idx ON messages (thread_id, id DESC);

CREATE TYPE review_role AS ENUM ('guest', 'host');

CREATE TABLE reviews (
    id          uuid PRIMARY KEY DEFAULT uuidv7(),
    booking_id  uuid NOT NULL REFERENCES bookings(id) ON DELETE CASCADE,
    space_id    uuid NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    author_id   uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    subject_id  uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    author_role review_role NOT NULL,
    rating      smallint NOT NULL,
    body        text NOT NULL DEFAULT '',
    revealed_at timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT reviews_rating_range CHECK (rating BETWEEN 1 AND 5),
    CONSTRAINT reviews_body_length  CHECK (length(body) <= 2000),
    CONSTRAINT reviews_not_self     CHECK (author_id <> subject_id),
    CONSTRAINT reviews_one_per_side UNIQUE (booking_id, author_role)
);

CREATE INDEX reviews_space_public_idx
    ON reviews (space_id, revealed_at DESC)
    WHERE revealed_at IS NOT NULL AND author_role = 'guest';

CREATE INDEX reviews_unrevealed_idx
    ON reviews (created_at) WHERE revealed_at IS NULL;

-- +goose Down
DROP TABLE IF EXISTS reviews;
DROP TYPE IF EXISTS review_role;
DROP TABLE IF EXISTS messages;
DROP TABLE IF EXISTS threads;
