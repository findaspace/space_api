-- +goose Up
-- The card rendition's key. Empty means the photo predates renditions, and
-- search falls back to the full image rather than pointing at an object that
-- does not exist: heavier, never broken.
ALTER TABLE space_media ADD COLUMN card_key text NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE space_media DROP COLUMN IF EXISTS card_key;
