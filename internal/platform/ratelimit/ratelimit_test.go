package ratelimit

import (
	"strconv"
	"sync"
	"testing"
	"time"
)

// fakeClock drives both the limiter and the buckets inside it, so these tests
// exercise refill without sleeping.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func testLimiter(t *testing.T, tier Tier, opts ...Option) (*Limiter, *fakeClock) {
	t.Helper()
	clock := &fakeClock{t: time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)}
	l := New(tier, append(opts, withClock(clock.now))...)
	t.Cleanup(l.Close)
	return l, clock
}

func TestBurstThenBlock(t *testing.T) {
	l, _ := testLimiter(t, Tier{PerMin: 60, Burst: 5})

	for i := 0; i < 5; i++ {
		if ok, _ := l.Allow("ip-1"); !ok {
			t.Fatalf("request %d rejected inside the burst", i+1)
		}
	}
	ok, retry := l.Allow("ip-1")
	if ok {
		t.Fatal("sixth request allowed; the burst is not enforced")
	}
	if retry <= 0 || retry > time.Minute {
		t.Errorf("retry-after = %v, want a positive, sane duration", retry)
	}
}

func TestRefills(t *testing.T) {
	l, clock := testLimiter(t, Tier{PerMin: 60, Burst: 2})

	l.Allow("ip-1")
	l.Allow("ip-1")
	if ok, _ := l.Allow("ip-1"); ok {
		t.Fatal("allowed past the burst")
	}

	// 60 per minute is one per second.
	clock.advance(time.Second)
	if ok, _ := l.Allow("ip-1"); !ok {
		t.Fatal("bucket did not refill after a second")
	}
}

// A rejected request must not consume the token it was denied, or one blocked
// caller pays twice for the same attempt.
func TestRejectionDoesNotConsumeAToken(t *testing.T) {
	l, clock := testLimiter(t, Tier{PerMin: 60, Burst: 1})

	l.Allow("ip-1")
	for i := 0; i < 5; i++ {
		l.Allow("ip-1") // all rejected
	}

	clock.advance(time.Second)
	if ok, _ := l.Allow("ip-1"); !ok {
		t.Fatal("one second later still blocked; rejections consumed tokens")
	}
}

func TestKeysAreIndependent(t *testing.T) {
	l, _ := testLimiter(t, Tier{PerMin: 60, Burst: 1})

	if ok, _ := l.Allow("ip-1"); !ok {
		t.Fatal("first caller blocked")
	}
	if ok, _ := l.Allow("ip-2"); !ok {
		t.Fatal("a second caller was blocked by the first one's usage")
	}
}

// The map is keyed by something an attacker controls, so it must stay bounded.
func TestCapacityIsEnforced(t *testing.T) {
	const capacity = 100
	l, _ := testLimiter(t, Tier{PerMin: 60, Burst: 1}, WithCapacity(capacity))

	for i := 0; i < capacity*3; i++ {
		l.Allow("ip-" + strconv.Itoa(i))
	}
	if got := l.Len(); got > capacity {
		t.Errorf("held %d entries with a capacity of %d", got, capacity)
	}
}

func TestIdleEntriesAreDropped(t *testing.T) {
	l, clock := testLimiter(t, Tier{PerMin: 60, Burst: 1},
		WithCapacity(10), WithIdleTTL(time.Minute))

	for i := 0; i < 10; i++ {
		l.Allow("old-" + strconv.Itoa(i))
	}
	clock.advance(2 * time.Minute)

	// Inserting past capacity triggers the idle sweep before any arbitrary
	// eviction, so all ten stale entries go and the new one fits.
	l.Allow("fresh")
	if got := l.Len(); got != 1 {
		t.Errorf("held %d entries, want 1 after the idle sweep", got)
	}
}

func TestConcurrentAccessIsSafe(t *testing.T) {
	l, _ := testLimiter(t, Tier{PerMin: 6000, Burst: 100})

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				l.Allow("ip-" + strconv.Itoa(i%10))
			}
		}(i)
	}
	wg.Wait()
}
