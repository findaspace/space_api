-- +goose Up
ALTER TYPE media_status ADD VALUE IF NOT EXISTS 'failed';

ALTER TABLE space_media ADD COLUMN failure_reason text NOT NULL DEFAULT '';
ALTER TABLE space_media ADD COLUMN attempts smallint NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE space_media DROP COLUMN IF EXISTS attempts;
ALTER TABLE space_media DROP COLUMN IF EXISTS failure_reason;
