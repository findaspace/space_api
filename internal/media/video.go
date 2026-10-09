package media

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"

	"github.com/samnart1/space_api/internal/jobs"
	"github.com/samnart1/space_api/internal/platform/transcode"
)

// Only containers a phone actually produces. Everything else is rejected at
// the request stage, before a hundred megabytes crosses the wire.
var videoTypes = map[string]string{
	"video/mp4":        "mp4",
	"video/quicktime":  "mov",
	"video/webm":       "webm",
	"video/x-matroska": "mkv",
	"video/3gpp":       "3gp",
}

type VideoUpload struct {
	Video     Video     `json:"video"`
	UploadURL string    `json:"upload_url"`
	ExpiresAt time.Time `json:"expires_at"`
}

// RequestVideoUpload reserves the walkthrough slot and hands back a presigned
// PUT.
//
// The single PUT is the weak point of this feature. It cannot resume, so a
// ninety-megabyte upload that fails at eighty starts again from zero, which on
// a Ghanaian mobile connection is a real possibility. The cap is set low
// enough to make that survivable, and multipart upload is the proper fix once
// there are hosts to complain about it.
func (s *Service) RequestVideoUpload(ctx context.Context, spaceID, hostID uuid.UUID, contentType string) (VideoUpload, error) {
	if err := s.spaces.AssertHostOwns(ctx, spaceID, hostID); err != nil {
		return VideoUpload{}, err
	}

	ext, ok := videoTypes[strings.ToLower(strings.TrimSpace(contentType))]
	if !ok {
		return VideoUpload{}, ErrUnsupportedType
	}

	id, err := uuid.NewV7()
	if err != nil {
		return VideoUpload{}, fmt.Errorf("generate upload id: %w", err)
	}
	key := fmt.Sprintf("spaces/%s/video/%s-raw.%s", spaceID, id, ext)

	v, err := s.store.CreateVideo(ctx, spaceID, key)
	if err != nil {
		return VideoUpload{}, err
	}

	url, expires, err := s.objects.PresignPut(ctx, key, contentType)
	if err != nil {
		return VideoUpload{}, err
	}

	return VideoUpload{
		Video:     v,
		UploadURL: url,
		ExpiresAt: expires,
	}, nil
}

// ConfirmVideo checks the object landed and hands the work to the worker.
// Verification is one HEAD; the transcode is minutes, so it belongs nowhere
// near an HTTP handler.
func (s *Service) ConfirmVideo(ctx context.Context, spaceID, hostID uuid.UUID) (Video, error) {
	if err := s.spaces.AssertHostOwns(ctx, spaceID, hostID); err != nil {
		return Video{}, err
	}

	v, err := s.store.VideoForSpace(ctx, spaceID)
	if err != nil {
		return Video{}, err
	}
	if v.Status == VideoReady {
		return s.withVideoURLs(v), nil
	}

	info, err := s.objects.Head(ctx, v.UploadKey)
	if err != nil {
		return Video{}, ErrNotUploaded
	}
	if info.Size > s.cfg.VideoMaxBytes {
		_ = s.objects.Delete(ctx, v.UploadKey)
		_ = s.store.MarkVideoFailed(ctx, v.ID, "the file is larger than the limit")
		return Video{}, ErrVideoTooLarge
	}

	if err := s.queue.InsertOpts(ctx,
		jobs.ProcessVideoArgs{VideoID: v.ID, SpaceID: spaceID},
		&river.InsertOpts{MaxAttempts: 3}); err != nil {
		return Video{}, err
	}
	return v, nil
}

// ProcessVideo is the worker half: download, probe, transcode, poster, upload,
// and throw the original away.
//
// It returns nil on a bad file rather than an error. A clip that cannot be
// decoded will not decode on the twenty-fifth attempt either, and River would
// spend a worker slot proving that. The row carries the reason so the host can
// be told.
func (s *Service) ProcessVideo(ctx context.Context, videoID uuid.UUID) error {
	v, err := s.store.VideoByID(ctx, videoID)
	if err != nil {
		return err
	}
	if v.Status == VideoReady {
		return nil
	}

	claimed, err := s.store.ClaimVideo(ctx, videoID)
	if err != nil {
		return err
	}
	if !claimed {
		return nil // another worker has it, or it is already done
	}

	fail := func(reason string) error {
		s.log.WarnContext(ctx, "walkthrough rejected",
			slog.String("video_id", videoID.String()), slog.String("reason", reason))
		_ = s.objects.Delete(ctx, v.UploadKey)
		return s.store.MarkVideoFailed(ctx, videoID, reason)
	}

	dir, err := os.MkdirTemp(s.cfg.WorkDir, "space-video-")
	if err != nil {
		return fmt.Errorf("make work directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	// ffmpeg needs to seek, so the source goes to disk rather than through a
	// pipe. This is why the worker needs scratch space proportional to the
	// upload cap times its concurrency.
	rawPath := filepath.Join(dir, "raw")
	if err := s.download(ctx, v.UploadKey, rawPath); err != nil {
		return err
	}

	info, err := s.video.Probe(ctx, rawPath)
	if err != nil {
		return fail("this file does not contain a video we can read")
	}
	if info.Seconds < 2 {
		return fail("the walkthrough is too short to be useful")
	}

	outPath := filepath.Join(dir, "out.mp4")
	result, err := s.video.Video(ctx, rawPath, outPath, info)
	if err != nil {
		return fail("we could not convert this video")
	}

	posterPath := filepath.Join(dir, "poster.jpg")
	if err := s.video.Poster(ctx, outPath, posterPath, info); err != nil {
		return fail("we could not read a frame from this video")
	}

	videoKey := strings.TrimSuffix(v.UploadKey, filepath.Ext(v.UploadKey))
	videoKey = strings.TrimSuffix(videoKey, "-raw") + ".mp4"
	posterKey := strings.TrimSuffix(videoKey, ".mp4") + "-poster.jpg"

	videoBytes, err := os.ReadFile(outPath)
	if err != nil {
		return fmt.Errorf("read transcoded video: %w", err)
	}
	posterBytes, err := os.ReadFile(posterPath)
	if err != nil {
		return fmt.Errorf("read poster: %w", err)
	}

	const immutable = "public, max-age=31536000, immutable"
	if err := s.objects.Put(ctx, videoKey, "video/mp4", videoBytes, immutable); err != nil {
		return err
	}
	if err := s.objects.Put(ctx, posterKey, "image/jpeg", posterBytes, immutable); err != nil {
		return err
	}

	if err := s.store.MarkVideoReady(ctx, videoID, videoKey, posterKey,
		result.Seconds, result.Width, result.Height, int64(len(videoBytes))); err != nil {
		return err
	}

	// The original is fifteen times the size of what replaced it and nothing
	// reads it again. Keeping it would be the single largest line on the
	// storage bill.
	if err := s.objects.Delete(ctx, v.UploadKey); err != nil {
		s.log.WarnContext(ctx, "could not delete the raw upload",
			slog.String("key", v.UploadKey), slog.Any("error", err))
	}
	return nil
}

func (s *Service) download(ctx context.Context, key, path string) error {
	body, err := s.objects.Get(ctx, key)
	if err != nil {
		return ErrNotUploaded
	}
	defer func() { _ = body.Close() }()

	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	defer func() { _ = f.Close() }()

	// Bounded even though the HEAD already checked: the object could have been
	// replaced between the check and here, and an unbounded copy fills the
	// worker's disk.
	n, err := io.Copy(f, io.LimitReader(body, s.cfg.VideoMaxBytes+1))
	if err != nil {
		return fmt.Errorf("download video: %w", err)
	}
	if n > s.cfg.VideoMaxBytes {
		return ErrVideoTooLarge
	}
	return f.Close()
}

func (s *Service) Video(ctx context.Context, spaceID uuid.UUID) (Video, error) {
	v, err := s.store.VideoForSpace(ctx, spaceID)
	if err != nil {
		return Video{}, err
	}
	return s.withVideoURLs(v), nil
}

func (s *Service) DeleteVideo(ctx context.Context, spaceID, hostID uuid.UUID) error {
	if err := s.spaces.AssertHostOwns(ctx, spaceID, hostID); err != nil {
		return err
	}

	v, err := s.store.DeleteVideo(ctx, spaceID)
	if err != nil {
		return err
	}

	// Best effort on the objects. The row is gone, so the listing is correct
	// either way; an orphaned object costs pennies and the sweeper is the
	// wrong place to be strict.
	for _, key := range []string{v.UploadKey, v.VideoKey, v.PosterKey} {
		if key != "" {
			_ = s.objects.Delete(ctx, key)
		}
	}
	return nil
}

func (s *Service) HasVideo(ctx context.Context, spaceID uuid.UUID) (bool, error) {
	return s.store.HasVideo(ctx, spaceID)
}

// SweepVideos removes uploads that were never confirmed and transcodes that
// failed. Ninety minutes is generous: a large upload on a slow connection is
// legitimately still in flight long after a photo would be.
func (s *Service) SweepVideos(ctx context.Context, olderThan time.Duration) (int, error) {
	stale, err := s.store.AbandonedVideos(ctx, time.Now().Add(-olderThan), 100)
	if err != nil {
		return 0, err
	}

	removed := 0
	for _, v := range stale {
		if _, err := s.store.DeleteVideo(ctx, v.SpaceID); err != nil {
			continue
		}
		for _, key := range []string{v.UploadKey, v.VideoKey, v.PosterKey} {
			if key != "" {
				_ = s.objects.Delete(ctx, key)
			}
		}
		removed++
	}
	return removed, nil
}

func (s *Service) withVideoURLs(v Video) Video {
	if v.VideoKey != "" {
		v.VideoURL = s.objects.PublicURL(v.VideoKey)
	}
	if v.PosterKey != "" {
		v.PosterURL = s.objects.PublicURL(v.PosterKey)
	}
	return v
}

var _ = transcode.Info{}
