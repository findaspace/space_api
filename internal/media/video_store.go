package media

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type VideoStatus string

const (
	VideoPending    VideoStatus = "pending"
	VideoProcessing VideoStatus = "processing"
	VideoReady      VideoStatus = "ready"
	VideoFailed     VideoStatus = "failed"
)

type Video struct {
	ID        uuid.UUID   `json:"id"`
	SpaceID   uuid.UUID   `json:"space_id"`
	Status    VideoStatus `json:"status"`
	UploadKey string      `json:"-"`
	VideoKey  string      `json:"-"`
	PosterKey string      `json:"-"`
	VideoURL  string      `json:"video_url,omitempty"`
	PosterURL string      `json:"poster_url,omitempty"`
	Seconds   int         `json:"duration_seconds"`
	Width     int         `json:"width"`
	Height    int         `json:"height"`
	Bytes     int64       `json:"bytes"`
	Reason    string      `json:"failure_reason,omitempty"`
	CreatedAt time.Time   `json:"created_at"`
}

var (
	ErrVideoExists   = errors.New("media: this listing already has a walkthrough")
	ErrVideoNotFound = errors.New("media: no walkthrough")
	ErrVideoTooLarge = errors.New("media: video is too large")
)

const videoColumns = `id, space_id, status::text, upload_key, video_key, poster_key,
	duration_seconds, width, height, bytes, failure_reason, created_at`

func scanVideo(row pgx.Row) (Video, error) {
	var v Video
	err := row.Scan(&v.ID, &v.SpaceID, &v.Status, &v.UploadKey, &v.VideoKey, &v.PosterKey,
		&v.Seconds, &v.Width, &v.Height, &v.Bytes, &v.Reason, &v.CreatedAt)
	return v, err
}

// CreateVideo reserves the single walkthrough slot for a listing. The unique
// constraint on space_id is what makes "one walkthrough" true rather than
// merely intended, so a double tap on the upload button gets a clear conflict
// instead of a second orphaned object in the bucket.
func (s *Store) CreateVideo(ctx context.Context, spaceID uuid.UUID, uploadKey string) (Video, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return Video{}, fmt.Errorf("generate video id: %w", err)
	}

	v, err := scanVideo(s.pool.QueryRow(ctx, `
		INSERT INTO space_videos (id, space_id, upload_key)
		VALUES ($1, $2, $3)
		RETURNING `+videoColumns, id, spaceID, uploadKey))
	if err != nil {
		if isUniqueViolation(err, "space_videos_space_id_key") {
			return Video{}, ErrVideoExists
		}
		return Video{}, fmt.Errorf("create video: %w", err)
	}
	return v, nil
}

func (s *Store) VideoForSpace(ctx context.Context, spaceID uuid.UUID) (Video, error) {
	v, err := scanVideo(s.pool.QueryRow(ctx,
		`SELECT `+videoColumns+` FROM space_videos WHERE space_id = $1`, spaceID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Video{}, ErrVideoNotFound
	}
	if err != nil {
		return Video{}, fmt.Errorf("get video: %w", err)
	}
	return v, nil
}

func (s *Store) VideoByID(ctx context.Context, id uuid.UUID) (Video, error) {
	v, err := scanVideo(s.pool.QueryRow(ctx,
		`SELECT `+videoColumns+` FROM space_videos WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Video{}, ErrVideoNotFound
	}
	if err != nil {
		return Video{}, fmt.Errorf("get video: %w", err)
	}
	return v, nil
}

// ClaimVideo moves a row into processing and counts the attempt. It succeeds
// only from pending or failed, so two workers racing the same job cannot both
// spend five minutes of CPU on the same file.
func (s *Store) ClaimVideo(ctx context.Context, id uuid.UUID) (bool, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE space_videos
		SET status = 'processing', attempts = attempts + 1
		WHERE id = $1 AND status IN ('pending', 'failed')`, id)
	if err != nil {
		return false, fmt.Errorf("claim video: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

func (s *Store) MarkVideoReady(ctx context.Context, id uuid.UUID, videoKey, posterKey string,
	seconds, width, height int, bytes int64) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE space_videos
		SET status = 'ready', video_key = $2, poster_key = $3,
		    duration_seconds = $4, width = $5, height = $6, bytes = $7,
		    failure_reason = ''
		WHERE id = $1`, id, videoKey, posterKey, seconds, width, height, bytes)
	if err != nil {
		return fmt.Errorf("mark video ready: %w", err)
	}
	return nil
}

func (s *Store) MarkVideoFailed(ctx context.Context, id uuid.UUID, reason string) error {
	if len(reason) > 500 {
		reason = reason[:500]
	}
	_, err := s.pool.Exec(ctx,
		`UPDATE space_videos SET status = 'failed', failure_reason = $2 WHERE id = $1`,
		id, reason)
	if err != nil {
		return fmt.Errorf("mark video failed: %w", err)
	}
	return nil
}

func (s *Store) DeleteVideo(ctx context.Context, spaceID uuid.UUID) (Video, error) {
	v, err := scanVideo(s.pool.QueryRow(ctx,
		`DELETE FROM space_videos WHERE space_id = $1 RETURNING `+videoColumns, spaceID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Video{}, ErrVideoNotFound
	}
	if err != nil {
		return Video{}, fmt.Errorf("delete video: %w", err)
	}
	return v, nil
}

// HasVideo feeds the completeness score. Only ready counts: a walkthrough
// still transcoding is not something a renter can watch.
func (s *Store) HasVideo(ctx context.Context, spaceID uuid.UUID) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM space_videos WHERE space_id = $1 AND status = 'ready')`,
		spaceID).Scan(&ok)
	if err != nil {
		return false, fmt.Errorf("check video: %w", err)
	}
	return ok, nil
}

// AbandonedVideos finds uploads that were requested and never completed. These
// matter more than stale photos: a hundred megabytes nobody will watch is real
// money sitting in the bucket.
func (s *Store) AbandonedVideos(ctx context.Context, cutoff time.Time, limit int32) ([]Video, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+videoColumns+` FROM space_videos
		WHERE status IN ('pending', 'failed') AND created_at < $1
		ORDER BY created_at LIMIT $2`, cutoff, limit)
	if err != nil {
		return nil, fmt.Errorf("list abandoned videos: %w", err)
	}
	defer rows.Close()

	out := []Video{}
	for rows.Next() {
		v, err := scanVideo(rows)
		if err != nil {
			return nil, fmt.Errorf("scan video: %w", err)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
