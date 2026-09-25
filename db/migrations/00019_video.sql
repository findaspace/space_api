-- +goose Up
CREATE TYPE video_status AS ENUM ('pending', 'processing', 'ready', 'failed');

-- One walkthrough per listing, enforced by the unique constraint on space_id.
-- Replacing a walkthrough means deleting this row and uploading again, which
-- keeps the model honest: there is no such thing as "the current video" among
-- several.
--
-- Separate from space_media on purpose. Photos are many per listing, small,
-- and processed in milliseconds; a video is one, large, and processed in a
-- subprocess that can fail in ways a JPEG cannot. Sharing a table would put
-- video branches inside every photo query.
CREATE TABLE space_videos (
    id               uuid PRIMARY KEY DEFAULT uuidv7(),
    space_id         uuid NOT NULL UNIQUE REFERENCES spaces(id) ON DELETE CASCADE,
    status           video_status NOT NULL DEFAULT 'pending',

    -- The raw upload, deleted once a transcode succeeds. Keeping originals
    -- would multiply storage by roughly fifteen for no benefit: the source is
    -- a phone clip, not a master.
    upload_key       text NOT NULL,
    video_key        text NOT NULL DEFAULT '',
    poster_key       text NOT NULL DEFAULT '',

    duration_seconds smallint NOT NULL DEFAULT 0,
    width            integer NOT NULL DEFAULT 0,
    height           integer NOT NULL DEFAULT 0,
    bytes            bigint NOT NULL DEFAULT 0,

    failure_reason   text NOT NULL DEFAULT '',
    attempts         smallint NOT NULL DEFAULT 0,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT space_videos_duration CHECK (duration_seconds >= 0 AND duration_seconds <= 300),
    CONSTRAINT space_videos_ready_has_keys CHECK (
        status <> 'ready' OR (video_key <> '' AND poster_key <> '')
    )
);

-- Search and the completeness score both ask "does this listing have a
-- walkthrough", which is a lookup on the ready ones only.
CREATE INDEX space_videos_ready_idx ON space_videos (space_id) WHERE status = 'ready';

-- Uploads that were requested and never confirmed, for the sweeper. A
-- hundred-megabyte object nobody will ever watch is worth more attention than
-- an abandoned photo.
CREATE INDEX space_videos_abandoned_idx ON space_videos (created_at)
    WHERE status IN ('pending', 'failed');

CREATE TRIGGER space_videos_set_updated_at
    BEFORE UPDATE ON space_videos
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- +goose Down
DROP TABLE IF EXISTS space_videos;
DROP TYPE IF EXISTS video_status;
