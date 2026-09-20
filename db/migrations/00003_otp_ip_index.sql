-- +goose Up
CREATE INDEX otp_challenges_ip_recent_idx
	ON otp_challenges (request_ip, created_at DESC)
	WHERE request_ip <> '';

-- +goose Down
DROP INDEX IF EXISTS otp_challenges_ip_recent_idx;
