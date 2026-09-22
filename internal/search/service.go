package search

import (
	"context"
)

type URLSigner interface {
	PublicURL(key string) string
}

type Service struct {
	store  *Store
	assets URLSigner
}

func NewService(store *Store, assets URLSigner) *Service {
	return &Service{store: store, assets: assets}
}

func (s *Service) Search(ctx context.Context, q Query) (Page, error) {
	if err := q.Normalize(); err != nil {
		return Page{}, err
	}

	// One extra row tells us whether another page exists, without a second
	// count query over the same filters.
	q.Limit++
	results, err := s.store.Search(ctx, q)
	if err != nil {
		return Page{}, err
	}
	q.Limit--

	page := Page{Results: results}
	if int32(len(results)) > q.Limit {
		page.Results = results[:q.Limit]
		last := page.Results[len(page.Results)-1]
		page.NextCursor = Cursor{Value: last.Score, ID: last.ID}.Encode()
	}

	for i := range page.Results {
		if page.Results[i].CoverKey != "" {
			page.Results[i].CoverURL = s.assets.PublicURL(page.Results[i].CoverKey)
		}
	}
	return page, nil
}

func (s *Service) RefreshStats(ctx context.Context) (int64, error) {
	return s.store.RefreshStats(ctx)
}
