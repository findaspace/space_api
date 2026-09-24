package geo

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/samnart1/space_api/internal/platform/osrm"
)

type Router interface {
	TableFromOne(ctx context.Context, origin osrm.Point, destinations []osrm.Point) ([]osrm.Leg, error)
}

type ServiceConfig struct {
	// RadiusM is how far out to look for landmarks. Five kilometres is about
	// an hour's walk, which is already past the point anyone would call
	// walkable; beyond it the number stops being useful and starts costing
	// routing time.
	RadiusM int

	// MaxLandmarks caps one listing's landmark set. Twelve is plenty: the
	// listing page shows three and search shows one.
	MaxLandmarks int

	// MaxWalkSeconds discards results past a sensible walk. OSRM will happily
	// route a two-hour hike, and "118 min walk to Makola" is noise on a card.
	MaxWalkSeconds int
}

func DefaultConfig() ServiceConfig {
	return ServiceConfig{RadiusM: 5000, MaxLandmarks: 12, MaxWalkSeconds: 2700}
}

type Service struct {
	store  *Store
	router Router
	log    *slog.Logger
	cfg    ServiceConfig
}

func NewService(store *Store, router Router, log *slog.Logger, cfg ServiceConfig) *Service {
	if cfg.RadiusM <= 0 {
		cfg = DefaultConfig()
	}
	return &Service{store: store, router: router, log: log, cfg: cfg}
}

// Compute routes one listing against its nearby landmarks and stores the
// result. It runs in the worker, once, when a listing is published.
//
// A routing outage returns nil, not an error: the listing keeps its straight
// line distance in search and the backfill job picks it up later. Failing here
// would mean an OSRM restart blocked hosts from publishing.
func (s *Service) Compute(ctx context.Context, spaceID uuid.UUID) error {
	point, err := s.store.SpacePoint(ctx, spaceID)
	if err != nil {
		return err
	}
	return s.computePoint(ctx, point)
}

func (s *Service) computePoint(ctx context.Context, point SpacePoint) error {
	landmarks, err := s.store.Nearby(ctx, point.Lat, point.Lng, s.cfg.RadiusM, s.cfg.MaxLandmarks)
	if err != nil {
		return err
	}
	if len(landmarks) == 0 {
		// Nothing seeded near this listing. Storing an empty set is correct and
		// it stops the backfill from retrying forever, but it is also a signal
		// that the landmark table needs more rows for this area.
		s.log.InfoContext(ctx, "no landmarks near listing",
			slog.String("space_id", point.ID.String()))
		return nil
	}

	destinations := make([]osrm.Point, 0, len(landmarks))
	for _, l := range landmarks {
		destinations = append(destinations, osrm.Point{Lat: l.Lat, Lng: l.Lng})
	}

	legs, err := s.router.TableFromOne(ctx, osrm.Point{Lat: point.Lat, Lng: point.Lng}, destinations)
	if errors.Is(err, osrm.ErrUnavailable) {
		s.log.WarnContext(ctx, "routing unavailable, leaving walks uncomputed",
			slog.String("space_id", point.ID.String()))
		return nil
	}
	if err != nil {
		return err
	}

	walks := make([]Walk, 0, len(legs))
	for i, leg := range legs {
		if !leg.Reachable || leg.Seconds > s.cfg.MaxWalkSeconds {
			continue
		}
		walks = append(walks, Walk{Landmark: landmarks[i], Seconds: leg.Seconds, Metres: leg.Metres})
	}

	return s.store.Replace(ctx, point.ID, walks)
}

// Backfill routes listings that have no walking times yet. It satisfies the
// worker's job interface and reports how many it handled.
func (s *Service) Backfill(ctx context.Context, limit int32) (int, error) {
	points, err := s.store.Unrouted(ctx, limit)
	if err != nil {
		return 0, err
	}

	done := 0
	for _, p := range points {
		if err := s.computePoint(ctx, p); err != nil {
			s.log.ErrorContext(ctx, "could not route listing",
				slog.String("space_id", p.ID.String()), slog.Any("error", err))
			continue
		}
		done++
	}
	return done, nil
}

// Recompute rolls out corrected landmark coordinates across listings routed
// before a cutoff. Run it by hand after fixing a gate's position, not on a
// schedule: re-routing the whole table every hour is wasted work for data that
// does not change.
func (s *Service) Recompute(ctx context.Context, before time.Time, limit int32) (int, error) {
	points, err := s.store.Stale(ctx, before, limit)
	if err != nil {
		return 0, err
	}

	done := 0
	for _, p := range points {
		if err := s.computePoint(ctx, p); err != nil {
			s.log.ErrorContext(ctx, "could not re-route listing",
				slog.String("space_id", p.ID.String()), slog.Any("error", err))
			continue
		}
		done++
	}
	return done, nil
}

// NearestLabel is what a card shows: "6 min walk to Madina Market". Empty when
// the listing has no computed walks, and the caller falls back to the locality
// alone rather than inventing a number.
func (s *Service) NearestLabel(ctx context.Context, spaceID uuid.UUID) (string, int, error) {
	walks, err := s.store.ForSpace(ctx, spaceID, 1)
	if err != nil || len(walks) == 0 {
		return "", 0, err
	}
	minutes := osrm.Minutes(walks[0].Seconds)
	return walks[0].Landmark.Name, minutes, nil
}

func (s *Service) ForSpace(ctx context.Context, spaceID uuid.UUID, limit int) ([]Walk, error) {
	if limit <= 0 {
		limit = 3
	}
	return s.store.ForSpace(ctx, spaceID, limit)
}
