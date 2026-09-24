package osrm

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func stub(t *testing.T, body string, status int) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return New(srv.URL, "foot", 5*time.Second)
}

func TestTableFromOne(t *testing.T) {
	c := stub(t, `{"code":"Ok",
		"durations":[[0,372.4,918.6]],
		"distances":[[0,517.2,1284.9]]}`, 200)

	legs, err := c.TableFromOne(context.Background(),
		Point{Lat: 5.6836, Lng: -0.1667},
		[]Point{{Lat: 5.6850, Lng: -0.1670}, {Lat: 5.6900, Lng: -0.1700}})
	if err != nil {
		t.Fatal(err)
	}
	if len(legs) != 2 {
		t.Fatalf("legs = %d, want 2", len(legs))
	}
	if legs[0].Seconds != 372 || legs[0].Metres != 517 || !legs[0].Reachable {
		t.Errorf("first leg = %+v", legs[0])
	}
	if legs[1].Seconds != 919 {
		t.Errorf("second leg seconds = %d, want 919 after rounding", legs[1].Seconds)
	}
}

// OSRM takes lng,lat. Reversed, every Ghanaian listing lands in the Atlantic
// and the resulting walk times still look plausible, which is why this is
// asserted rather than assumed.
func TestCoordinateOrderIsLngLat(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Path
		w.Write([]byte(`{"code":"Ok","durations":[[0,60]],"distances":[[0,80]]}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "foot", 5*time.Second)
	_, err := c.TableFromOne(context.Background(),
		Point{Lat: 5.683600, Lng: -0.166700},
		[]Point{{Lat: 6.674500, Lng: -1.571600}})
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(got, "-0.166700,5.683600") {
		t.Errorf("origin not in lng,lat order: %s", got)
	}
	if !strings.Contains(got, "-1.571600,6.674500") {
		t.Errorf("destination not in lng,lat order: %s", got)
	}
	if !strings.Contains(got, "/foot/") {
		t.Errorf("profile missing from path: %s", got)
	}
}

// A listing at a landmark's exact coordinates routes in zero seconds, which is
// a real answer and not a null. Conflating the two discarded the single most
// relevant match on every listing posted at a market or a campus gate.
func TestZeroSecondsIsReachable(t *testing.T) {
	c := stub(t, `{"code":"Ok","durations":[[0,0]],"distances":[[0,0]]}`, 200)

	legs, err := c.TableFromOne(context.Background(),
		Point{Lat: 5.6836, Lng: -0.1667},
		[]Point{{Lat: 5.6836, Lng: -0.1667}})
	if err != nil {
		t.Fatal(err)
	}
	if !legs[0].Reachable {
		t.Error("a zero-second walk is reachable; only null is not")
	}
	if legs[0].Seconds != 0 {
		t.Errorf("seconds = %d, want 0", legs[0].Seconds)
	}
}

func TestUnreachableDestination(t *testing.T) {
	c := stub(t, `{"code":"Ok","durations":[[0,null,120]],"distances":[[0,null,150]]}`, 200)

	legs, err := c.TableFromOne(context.Background(),
		Point{Lat: 5.68, Lng: -0.16},
		[]Point{{Lat: 5.69, Lng: -0.17}, {Lat: 5.70, Lng: -0.18}})
	if err != nil {
		t.Fatal(err)
	}
	if legs[0].Reachable {
		t.Error("a null duration must not read as reachable in zero seconds")
	}
	if !legs[1].Reachable || legs[1].Seconds != 120 {
		t.Errorf("second leg = %+v", legs[1])
	}
}

// A routing outage degrades: the caller stores no walking times and the listing
// still publishes.
func TestServerErrorIsUnavailable(t *testing.T) {
	c := stub(t, `{}`, 503)

	_, err := c.TableFromOne(context.Background(),
		Point{Lat: 5.68, Lng: -0.16}, []Point{{Lat: 5.69, Lng: -0.17}})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
}

func TestUnreachableHostIsUnavailable(t *testing.T) {
	c := New("http://127.0.0.1:1", "foot", 200*time.Millisecond)

	_, err := c.TableFromOne(context.Background(),
		Point{Lat: 5.68, Lng: -0.16}, []Point{{Lat: 5.69, Lng: -0.17}})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
}

// An OSRM error arrives as HTTP 200 with a code field, so the status alone is
// not enough to tell success from failure.
func TestOSRMErrorCode(t *testing.T) {
	c := stub(t, `{"code":"NoSegment","message":"Could not find a matching segment for coordinate 0"}`, 200)

	_, err := c.TableFromOne(context.Background(),
		Point{Lat: 5.68, Lng: -0.16}, []Point{{Lat: 5.69, Lng: -0.17}})
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "NoSegment") {
		t.Errorf("err = %v, want the OSRM code", err)
	}
}

func TestTooManyDestinations(t *testing.T) {
	c := stub(t, `{"code":"Ok","durations":[[0]]}`, 200)

	points := make([]Point, MaxDestinations+1)
	_, err := c.TableFromOne(context.Background(), Point{Lat: 5.68, Lng: -0.16}, points)
	if !errors.Is(err, ErrTooManyPoints) {
		t.Fatalf("err = %v, want ErrTooManyPoints", err)
	}
}

func TestNoDestinationsIsNotACall(t *testing.T) {
	c := New("http://127.0.0.1:1", "foot", time.Millisecond)

	legs, err := c.TableFromOne(context.Background(), Point{Lat: 5.68, Lng: -0.16}, nil)
	if err != nil || legs != nil {
		t.Errorf("legs = %v, err = %v; an empty list must not hit the network", legs, err)
	}
}

func TestMinutes(t *testing.T) {
	cases := map[int]int{
		0:    1,
		5:    1,
		29:   1,
		31:   1,
		90:   2,
		372:  6,
		919:  15,
		3600: 60,
	}
	for seconds, want := range cases {
		if got := Minutes(seconds); got != want {
			t.Errorf("Minutes(%d) = %d, want %d", seconds, got, want)
		}
	}
}
