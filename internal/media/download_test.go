package media

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"
)

type downloadObjects struct {
	ObjectStore
	data string
}

func (o downloadObjects) Get(context.Context, string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader(o.data)), nil
}
func TestVideoDownloadRejectsObjectChangedAfterHead(t *testing.T) {
	for _, tc := range []struct {
		data      string
		oversized bool
	}{{"1234", false}, {"12345", true}} {
		s := Service{objects: downloadObjects{data: tc.data}, cfg: ServiceConfig{VideoMaxBytes: 4}}
		err := s.download(context.Background(), "key", filepath.Join(t.TempDir(), "video"))
		if tc.oversized && !errors.Is(err, ErrVideoTooLarge) {
			t.Fatalf("oversized object accepted: %v", err)
		}
		if !tc.oversized && err != nil {
			t.Fatal(err)
		}
	}
}
