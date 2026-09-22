-- +goose Up
CREATE TYPE notification_channel AS ENUM ('in_app', 'sms', 'push');
CREATE TYPE delivery_status AS ENUM ('pending', 'sending', 'sent', 'failed', 'skipped');

CREATE TABLE notifications (
    id         uuid PRIMARY KEY DEFAULT uuidv7(),
    user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    event      text NOT NULL,
    event_key  text NOT NULL,
    title      text NOT NULL,
    body       text NOT NULL,
    sms        text NOT NULL DEFAULT '',
    data       jsonb NOT NULL DEFAULT '{}'::jsonb,
    read_at    timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),

    -- Replays and retries produce the same key and therefore one row.
    CONSTRAINT notifications_once_per_event UNIQUE (user_id, event_key)
);

CREATE INDEX notifications_inbox_idx  ON notifications (user_id, id DESC);
CREATE INDEX notifications_unread_idx ON notifications (user_id) WHERE read_at IS NULL;

CREATE TABLE notification_deliveries (
    id              uuid PRIMARY KEY DEFAULT uuidv7(),
    notification_id uuid NOT NULL REFERENCES notifications(id) ON DELETE CASCADE,
    channel         notification_channel NOT NULL,
    status          delivery_status NOT NULL DEFAULT 'pending',
    provider_ref    text NOT NULL DEFAULT '',
    error           text NOT NULL DEFAULT '',
    attempts        smallint NOT NULL DEFAULT 0,
    sent_at         timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT notification_deliveries_one_per_channel UNIQUE (notification_id, channel)
);

-- Deliveries stuck in 'sending' are exactly the ambiguous ones. This is the
-- index you query when someone says "I never got the SMS".
CREATE INDEX notification_deliveries_sending_idx
    ON notification_deliveries (updated_at) WHERE status = 'sending';

CREATE TABLE notification_preferences (
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    event   text NOT NULL,
    sms     boolean NOT NULL,
    push    boolean NOT NULL DEFAULT true,
    PRIMARY KEY (user_id, event)
);

CREATE TRIGGER notification_deliveries_set_updated_at
    BEFORE UPDATE ON notification_deliveries
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- +goose Down
DROP TABLE IF EXISTS notification_preferences;
DROP TABLE IF EXISTS notification_deliveries;
DROP TABLE IF EXISTS notifications;
DROP TYPE IF EXISTS delivery_status;
DROP TYPE IF EXISTS notification_channel;
