package jobs

import "github.com/google/uuid"

// Kinds are stable strings stored in the database. Renaming one orphans every
// queued job of that kind, so treat them like column names.

type ProcessMediaArgs struct {
	MediaID uuid.UUID `json:"media_id"`
	SpaceID uuid.UUID `json:"space_id"`
}

func (ProcessMediaArgs) Kind() string { return "process_media" }

type DeliverNotificationArgs struct {
	NotificationID uuid.UUID `json:"notification_id"`
}

func (DeliverNotificationArgs) Kind() string { return "deliver_notification" }

type ExpireHoldsArgs struct{}

func (ExpireHoldsArgs) Kind() string { return "expire_holds" }

type ReconcilePayoutsArgs struct{}

func (ReconcilePayoutsArgs) Kind() string { return "reconcile_payouts" }

type SweepUploadsArgs struct{}

func (SweepUploadsArgs) Kind() string { return "sweep_uploads" }

type TrialBalanceArgs struct{}

func (TrialBalanceArgs) Kind() string { return "trial_balance" }

type AdvanceBookingsArgs struct{}

func (AdvanceBookingsArgs) Kind() string { return "advance_bookings" }

type RevealReviewsArgs struct{}

func (RevealReviewsArgs) Kind() string { return "reveal_reviews" }

type RefreshSearchStatsArgs struct{}

func (RefreshSearchStatsArgs) Kind() string { return "refresh_search_stats" }

type RouteListingArgs struct {
	SpaceID uuid.UUID `json:"space_id"`
}

func (RouteListingArgs) Kind() string { return "route_listing" }

type BackfillRoutesArgs struct{}

func (BackfillRoutesArgs) Kind() string { return "backfill_routes" }

type ProcessVideoArgs struct {
	VideoID uuid.UUID `json:"video_id"`
	SpaceID uuid.UUID `json:"space_id"`
}

func (ProcessVideoArgs) Kind() string { return "process_video" }

type SweepVideosArgs struct{}

func (SweepVideosArgs) Kind() string { return "sweep_videos" }
