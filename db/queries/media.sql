-- name: CreateMedia :one
INSERT INTO space_media (id, space_id, object_key, content_type, position)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: CountMedia :one
SELECT count(*) FROM space_media 
WHERE space_id = @space_id AND status <> 'failed';



-- name: MarkMediaReady :one
UPDATE space_media
SET status = 'ready', byte_size = @byte_size, width = @width, height = @height,
    object_key = @object_key, content_type = @content_type, ready_at = now()
WHERE id = @id AND space_id = @space_id
RETURNING *;

-- name: GetMedia :one
SELECT * FROM space_media WHERE id = @id AND space_id = @space_id;

-- name: ListMedia :many
SELECT * FROM space_media
WHERE space_id = @space_id AND status = 'ready'
ORDER BY position, created_at;

-- name: CountReadyMedia :one
SELECT count(*) FROM space_media WHERE space_id = @space_id AND status = 'ready';

-- name: NextMediaPosition :one
SELECT COALESCE(max(position) + 1, 0)::int FROM space_media WHERE space_id = @space_id;

-- name: DeleteMedia :one
DELETE FROM space_media WHERE id = @id AND space_id = @space_id
RETURNING object_key;

-- name: SetMediaPosition :exec
UPDATE space_media SET position = @position WHERE id = @id AND space_id = @space_id;


-- name: MarkMediaFailed :exec
UPDATE space_media
SET status = 'failed', failure_reason = @failure_reason, attempts = attempts + 1
WHERE id = @id AND space_id = @space_id;

-- name: ListAbandonedMedia :many
SELECT * FROM space_media
WHERE (status = 'pending' AND created_at < @cutoff)
	OR (status = 'failed' AND created_at < @cutoff)
ORDER BY created_at
LIMIT @row_limit;
