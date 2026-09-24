-- +goose Up
ALTER TABLE spaces ADD COLUMN completeness smallint NOT NULL DEFAULT 0;

ALTER TABLE spaces
    ADD CONSTRAINT spaces_completeness_range CHECK (completeness BETWEEN 0 AND 100);

-- Ranking reads this alongside the existing columns, so it joins the partial
-- index rather than getting one of its own.
DROP INDEX IF EXISTS spaces_search_rank_idx;
CREATE INDEX spaces_search_rank_idx
    ON spaces (completeness DESC, rating_sum DESC, published_at DESC)
    WHERE status = 'published';

-- +goose Down
DROP INDEX IF EXISTS spaces_search_rank_idx;
CREATE INDEX spaces_search_rank_idx
    ON spaces (rating_sum DESC, published_at DESC)
    WHERE status = 'published';
ALTER TABLE spaces DROP COLUMN IF EXISTS completeness;
