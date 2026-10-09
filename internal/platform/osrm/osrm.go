package osrm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

var (
	// ErrUnavailable means the routing service could not be reached or did not
	// answer. Callers degrade to straight-line distance rather than failing:
	// a routing outage must never stop a host publishing a listing.
	ErrUnavailable = errors.New("osrm: routing service unavailable")

	ErrTooManyPoints = errors.New("osrm: too many destinations for one table call")
)

// MaxDestinations caps one Table call. OSRM's own default ceiling is 100
// coordinates per request and the cost is quadratic in the general case, so a
// listing routed against more than this many landmarks is a sign the prefilter
// radius is too generous.
const MaxDestinations = 64

type Point struct {
	Lat float64
	Lng float64
}

// Leg is one origin-to-destination result. Unreachable pairs come back with
// Reachable false rather than a zero, because zero seconds and no route are
// very different facts.
type Leg struct {
	Seconds   int
	Metres    int
	Reachable bool
}

type Client struct {
	http    *http.Client
	baseURL string
	profile string
}

// New builds a client. profile is the OSRM profile name in the URL path.
//
// Note that a self-hosted osrm-routed serves exactly one profile: whichever
// .lua it was built with. The name in the path is cosmetic there. It matters
// on the public demo servers, which ignore the path and return driving times
// for a foot request unless you use the routed-foot host, which is why this
// client is pointed at our own instance.
func New(baseURL, profile string, timeout time.Duration) *Client {
	if profile == "" {
		profile = "foot"
	}
	return &Client{
		http:    &http.Client{Timeout: timeout},
		baseURL: strings.TrimRight(baseURL, "/"),
		profile: profile,
	}
}

// Pointers, not float64. OSRM writes null for a destination it cannot reach,
// and null decodes into a plain float64 as zero, which is indistinguishable
// from a landmark you are standing on. That ambiguity cost a whole debugging
// session: a listing at a market's exact coordinates had its only real match
// discarded as unreachable.
type tableResponse struct {
	Code      string       `json:"code"`
	Message   string       `json:"message"`
	Durations [][]*float64 `json:"durations"`
	Distances [][]*float64 `json:"distances"`
}

// TableFromOne returns the walk from one origin to every destination in a
// single request.
//
// This is the whole reason walking times are affordable: a listing is routed
// against all of its nearby landmarks once, at publish time, in one round trip.
// Routing per search result would be both slow and pointless, since the walk
// from a room to a market does not change between requests.
func (c *Client) TableFromOne(ctx context.Context, origin Point, destinations []Point) ([]Leg, error) {
	if len(destinations) == 0 {
		return nil, nil
	}
	if len(destinations) > MaxDestinations {
		return nil, ErrTooManyPoints
	}

	// OSRM takes coordinates as lng,lat, which is the opposite order to how
	// everyone says them out loud. Getting this backwards puts every listing in
	// the Atlantic and the errors look like plausible long walks.
	coords := make([]string, 0, len(destinations)+1)
	coords = append(coords, formatPoint(origin))
	for _, d := range destinations {
		coords = append(coords, formatPoint(d))
	}

	url := fmt.Sprintf("%s/table/v1/%s/%s?sources=0&annotations=duration,distance",
		c.baseURL, c.profile, strings.Join(coords, ";"))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build table request: %w", err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrUnavailable, err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("%w: reading response", ErrUnavailable)
	}
	if resp.StatusCode >= 500 {
		return nil, fmt.Errorf("%w: http %d", ErrUnavailable, resp.StatusCode)
	}

	var out tableResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("%w: unreadable response", ErrUnavailable)
	}
	if out.Code != "Ok" {
		return nil, fmt.Errorf("osrm: %s (%s)", out.Code, out.Message)
	}
	if len(out.Durations) != 1 {
		return nil, fmt.Errorf("osrm: expected one source row, got %d", len(out.Durations))
	}

	durations := out.Durations[0]
	var distances []*float64
	if len(out.Distances) == 1 {
		distances = out.Distances[0]
	}

	// Index 0 is the origin against itself; the destinations start at 1.
	legs := make([]Leg, len(destinations))
	for i := range destinations {
		pos := i + 1
		if pos >= len(durations) || durations[pos] == nil {
			continue
		}

		seconds := *durations[pos]
		if seconds < 0 {
			continue
		}

		leg := Leg{Seconds: int(seconds + 0.5), Reachable: true}
		if pos < len(distances) && distances[pos] != nil {
			leg.Metres = int(*distances[pos] + 0.5)
		}
		legs[i] = leg
	}
	return legs, nil
}

// Healthy reports whether the service answers at all. The readiness probe uses
// it so an instance with a dead router is pulled from the load balancer rather
// than silently publishing listings with no walking times.
func (c *Client) Healthy(ctx context.Context) bool {
	// Accra's Independence Square, routed against itself: the cheapest request
	// that still exercises the graph.
	_, err := c.TableFromOne(ctx,
		Point{Lat: 5.5480, Lng: -0.1920},
		[]Point{{Lat: 5.5490, Lng: -0.1930}})
	return err == nil
}

func formatPoint(p Point) string {
	return strconv.FormatFloat(p.Lng, 'f', 6, 64) + "," + strconv.FormatFloat(p.Lat, 'f', 6, 64)
}

// Minutes rounds a leg for display. Walking times are shown to the nearest
// minute with a floor of one: "0 min walk" reads as broken even when it is
// technically true of a shop across the street.
func Minutes(seconds int) int {
	m := (seconds + 30) / 60
	if m < 1 {
		return 1
	}
	return m
}
