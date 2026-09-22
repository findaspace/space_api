-- +goose Up
ALTER TABLE spaces ADD COLUMN review_count integer NOT NULL DEFAULT 0;
ALTER TABLE spaces ADD COLUMN rating_sum   integer NOT NULL DEFAULT 0;
ALTER TABLE spaces ADD COLUMN photo_count  integer NOT NULL DEFAULT 0;

-- Ranked results are read far more than they are written, so the ordering
-- columns are indexed together for the published set only.
CREATE INDEX spaces_search_rank_idx
    ON spaces (rating_sum DESC, published_at DESC)
    WHERE status = 'published';

-- +goose Down
DROP INDEX IF EXISTS spaces_search_rank_idx;
ALTER TABLE spaces DROP COLUMN IF EXISTS photo_count;
ALTER TABLE spaces DROP COLUMN IF EXISTS rating_sum;
ALTER TABLE spaces DROP COLUMN IF EXISTS review_count;
