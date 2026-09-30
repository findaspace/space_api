-- +goose Up
-- Every time a signed-in renter reveals an owner's number. It is what lets a
-- daily cap stop anyone scraping the directory of hosts, and it means a
-- reported scam can be traced to the verified phone that first saw the number.
CREATE TABLE contact_reveals (
    id          uuid PRIMARY KEY DEFAULT uuidv7(),
    viewer_id   uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    space_id    uuid NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    host_id     uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at  timestamptz NOT NULL DEFAULT now()
);

-- The cap counts one viewer's reveals over the last day.
CREATE INDEX contact_reveals_viewer_recent ON contact_reveals (viewer_id, created_at DESC);

-- +goose Down
DROP TABLE IF EXISTS contact_reveals;
