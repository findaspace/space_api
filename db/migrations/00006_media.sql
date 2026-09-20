-- +goose Up
CREATE TYPE media_status AS ENUM ('pending', 'ready');

CREATE TABLE space_media (
    id           uuid PRIMARY KEY DEFAULT uuidv7(),
    space_id     uuid NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    object_key   text NOT NULL UNIQUE,
    content_type text NOT NULL,
    byte_size    bigint NOT NULL DEFAULT 0,
    width        integer NOT NULL DEFAULT 0,
    height       integer NOT NULL DEFAULT 0,
    position     integer NOT NULL DEFAULT 0,
    status       media_status NOT NULL DEFAULT 'pending',
    created_at   timestamptz NOT NULL DEFAULT now(),
    ready_at     timestamptz,

    CONSTRAINT space_media_content_type CHECK (content_type IN ('image/jpeg', 'image/png')),
    CONSTRAINT space_media_size_range CHECK (byte_size >= 0 AND byte_size <= 10485760),
    CONSTRAINT space_media_position_nonneg CHECK (position >= 0),
    CONSTRAINT space_media_ready_has_dimensions CHECK (
        status <> 'ready' OR (width > 0 AND height > 0 AND ready_at IS NOT NULL)
    )
);

CREATE INDEX space_media_order_idx ON space_media (space_id, position, created_at);
CREATE INDEX space_media_pending_idx ON space_media (created_at) WHERE status = 'pending';

-- +goose Down
DROP TABLE IF EXISTS space_media;
DROP TYPE IF EXISTS media_status;
