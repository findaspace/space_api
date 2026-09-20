package media

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

type Status string

const (
	StatusPending Status = "pending"
	StatusReady   Status = "ready"
)

type Media struct {
	ID          uuid.UUID
	SpaceID     uuid.UUID
	ObjectKey   string
	ContentType string
	ByteSize    int64
	Width       int
	Height      int
	Position    int
	Status      Status
	URL         string
	CreatedAt   time.Time
}

var (
	ErrNotFound     = errors.New("media: not found")
	ErrNotUploaded  = errors.New("media: object was never uploaded")
	ErrTooMany      = errors.New("media: photo limit reached")
	ErrSizeMismatch = errors.New("media: uploaded size exceeds the limit")
)

// ObjectKey namespaces by space so a bulk delete is a prefix operation and an
// orphaned object is traceable to its listing.
func ObjectKey(spaceID, mediaID uuid.UUID, contentType string) string {
	ext := ".jpg"
	if contentType == ContentPNG {
		ext = ".png"
	}
	return "spaces/" + spaceID.String() + "/" + mediaID.String() + ext
}
