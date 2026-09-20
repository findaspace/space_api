package media

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"

	"github.com/samnart1/space_api/internal/platform/objectstore"
)

// SpaceOwnership is declared here so this package never imports listing.
type SpaceOwnership interface {
	AssertHostOwns(ctx context.Context, spaceID, hostID uuid.UUID) error
}

type ObjectStore interface {
	PresignPut(ctx context.Context, key, contentType string) (string, time.Time, error)
	Head(ctx context.Context, key string) (objectstore.ObjectInfo, error)
	Get(ctx context.Context, key string) (io.ReadCloser, error)
	Put(ctx context.Context, key, contentType string, body []byte, cacheControl string) error
	Delete(ctx context.Context, key string) error
	PublicURL(key string) string
}

type ServiceConfig struct {
	MaxBytes    int64
	MaxPerSpace int
}

type Service struct {
	store   *Store
	objects ObjectStore
	spaces  SpaceOwnership
	cfg     ServiceConfig
}

func NewService(store *Store, objects ObjectStore, spaces SpaceOwnership, cfg ServiceConfig) *Service {
	return &Service{store: store, objects: objects, spaces: spaces, cfg: cfg}
}

type Upload struct {
	Media     Media
	URL       string
	ExpiresAt time.Time
	MaxBytes  int64
}

func (s *Service) RequestUpload(ctx context.Context, spaceID, hostID uuid.UUID, contentType string) (Upload, error) {
	if contentType != ContentJPEG && contentType != ContentPNG {
		return Upload{}, ErrUnsupportedType
	}
	if err := s.spaces.AssertHostOwns(ctx, spaceID, hostID); err != nil {
		return Upload{}, err
	}

	count, err := s.store.Count(ctx, spaceID)
	if err != nil {
		return Upload{}, err
	}
	if count >= s.cfg.MaxPerSpace {
		return Upload{}, ErrTooMany
	}

	position, err := s.store.NextPosition(ctx, spaceID)
	if err != nil {
		return Upload{}, err
	}

	// Generated in Go rather than by the database, because the object key
	// embeds the id and the key has to exist before the row does.
	id, err := uuid.NewV7()
	if err != nil {
		return Upload{}, fmt.Errorf("generate media id: %w", err)
	}
	key := ObjectKey(spaceID, id, contentType)

	m, err := s.store.Create(ctx, id, spaceID, key, contentType, position)
	if err != nil {
		return Upload{}, err
	}

	url, expires, err := s.objects.PresignPut(ctx, key, contentType)
	if err != nil {
		return Upload{}, err
	}
	return Upload{Media: m, URL: url, ExpiresAt: expires, MaxBytes: s.cfg.MaxBytes}, nil
}

// Confirm verifies the upload landed, then rewrites the image. The rewrite is
// what removes EXIF, so it happens before the row is ever marked ready.
func (s *Service) Confirm(ctx context.Context, spaceID, mediaID, hostID uuid.UUID) (Media, error) {
	if err := s.spaces.AssertHostOwns(ctx, spaceID, hostID); err != nil {
		return Media{}, err
	}

	m, err := s.store.Get(ctx, mediaID, spaceID)
	if err != nil {
		return Media{}, err
	}
	if m.Status == StatusReady {
		m.URL = s.objects.PublicURL(m.ObjectKey)
		return m, nil // idempotent: a retried confirm is not an error
	}

	info, err := s.objects.Head(ctx, m.ObjectKey)
	if err != nil {
		return Media{}, ErrNotUploaded
	}
	if info.Size > s.cfg.MaxBytes {
		_ = s.objects.Delete(ctx, m.ObjectKey)
		return Media{}, ErrSizeMismatch
	}

	body, err := s.objects.Get(ctx, m.ObjectKey)
	if err != nil {
		return Media{}, ErrNotUploaded
	}
	defer body.Close()

	norm, err := Normalize(body, m.ContentType, s.cfg.MaxBytes)
	if err != nil {
		_ = s.objects.Delete(ctx, m.ObjectKey)
		return Media{}, err
	}

	// Immutable key, so a year-long cache lifetime is safe: edits create a new
	// media row with a new key rather than overwriting this one.
	if err := s.objects.Put(ctx, m.ObjectKey, norm.ContentType, norm.Data, "public, max-age=31536000, immutable"); err != nil {
		return Media{}, err
	}

	out, err := s.store.MarkReady(ctx, mediaID, spaceID, m.ObjectKey, norm.ContentType,
		int64(len(norm.Data)), norm.Width, norm.Height)
	if err != nil {
		return Media{}, err
	}
	out.URL = s.objects.PublicURL(out.ObjectKey)
	return out, nil
}

func (s *Service) ForSpace(ctx context.Context, spaceID uuid.UUID) ([]Media, error) {
	items, err := s.store.ListReady(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	for i := range items {
		items[i].URL = s.objects.PublicURL(items[i].ObjectKey)
	}
	return items, nil
}

func (s *Service) Delete(ctx context.Context, spaceID, mediaID, hostID uuid.UUID) error {
	if err := s.spaces.AssertHostOwns(ctx, spaceID, hostID); err != nil {
		return err
	}

	key, err := s.store.Delete(ctx, mediaID, spaceID)
	if err != nil {
		return err
	}
	// The row is authoritative. A failed object delete leaves a harmless orphan
	// for the sweeper; a failed row delete would leave a photo nobody can remove.
	_ = s.objects.Delete(ctx, key)
	return nil
}

func (s *Service) Reorder(ctx context.Context, spaceID, hostID uuid.UUID, ids []uuid.UUID) ([]Media, error) {
	if err := s.spaces.AssertHostOwns(ctx, spaceID, hostID); err != nil {
		return nil, err
	}

	existing, err := s.store.ListReady(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	if len(ids) != len(existing) {
		return nil, ErrNotFound
	}

	// Every id must belong to this space and appear exactly once, or a caller
	// could reposition another host's photo by including its id.
	owned := make(map[uuid.UUID]bool, len(existing))
	for _, m := range existing {
		owned[m.ID] = true
	}
	for _, id := range ids {
		if !owned[id] {
			return nil, ErrNotFound
		}
		delete(owned, id)
	}

	if err := s.store.Reorder(ctx, spaceID, ids); err != nil {
		return nil, err
	}
	return s.ForSpace(ctx, spaceID)
}
