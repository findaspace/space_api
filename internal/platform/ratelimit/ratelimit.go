package ratelimit

import (
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// Tier is one class of traffic. Auth endpoints get a tight tier, reads a loose
// one, because the cost of abuse differs by orders of magnitude between them.
type Tier struct {
	Name   string
	PerMin int
	Burst  int
}

func (t Tier) limit() rate.Limit { return rate.Limit(float64(t.PerMin) / 60.0) }

type entry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// Limiter is a per-instance token bucket keyed by caller. It is deliberately
// not distributed: shared state would add a round trip to every request, and
// for abuse prevention an approximate limit per replica is enough.
type Limiter struct {
	mu       sync.Mutex
	entries  map[string]*entry
	tier     Tier
	capacity int
	idleTTL  time.Duration
	now      func() time.Time

	stop chan struct{}
	once sync.Once
}

type Option func(*Limiter)

func WithCapacity(n int) Option           { return func(l *Limiter) { l.capacity = n } }
func WithIdleTTL(d time.Duration) Option  { return func(l *Limiter) { l.idleTTL = d } }
func withClock(f func() time.Time) Option { return func(l *Limiter) { l.now = f } }

func New(tier Tier, opts ...Option) *Limiter {
	l := &Limiter{
		entries:  make(map[string]*entry),
		tier:     tier,
		capacity: 50_000,
		idleTTL:  10 * time.Minute,
		now:      time.Now,
		stop:     make(chan struct{}),
	}
	for _, opt := range opts {
		opt(l)
	}
	go l.sweepLoop()
	return l
}

func (l *Limiter) Close() {
	l.once.Do(func() { close(l.stop) })
}

// Allow reports whether the caller may proceed, and if not, how long until the
// next token. The delay comes from the bucket itself rather than a guess, so
// Retry-After is accurate.
func (l *Limiter) Allow(key string) (bool, time.Duration) {
	now := l.now()

	l.mu.Lock()
	defer l.mu.Unlock()

	e, ok := l.entries[key]
	if !ok {
		if len(l.entries) >= l.capacity {
			l.makeRoomLocked(now)
		}
		e = &entry{limiter: rate.NewLimiter(l.tier.limit(), l.tier.Burst)}
		l.entries[key] = e
	}
	e.lastSeen = now

	reservation := e.limiter.ReserveN(now, 1)
	if !reservation.OK() {
		return false, time.Minute
	}
	if delay := reservation.DelayFrom(now); delay > 0 {
		// Cancel, or the token stays spoken for and the caller is punished
		// twice for one rejected request.
		reservation.CancelAt(now)
		return false, delay
	}
	return true, 0
}

func (l *Limiter) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.entries)
}

// makeRoomLocked drops idle entries, then arbitrary ones if that was not
// enough. Arbitrary eviction can reset an active attacker's bucket, which is
// the price of a bounded map keyed by something the attacker controls.
func (l *Limiter) makeRoomLocked(now time.Time) {
	l.evictIdleLocked(now)
	if len(l.entries) < l.capacity {
		return
	}

	// Go randomizes map iteration, so taking the first N is effectively a
	// random sample without the cost of scanning for the true oldest.
	target := l.capacity / 10
	for key := range l.entries {
		delete(l.entries, key)
		target--
		if target <= 0 {
			return
		}
	}
}

func (l *Limiter) evictIdleLocked(now time.Time) {
	cutoff := now.Add(-l.idleTTL)
	for key, e := range l.entries {
		if e.lastSeen.Before(cutoff) {
			delete(l.entries, key)
		}
	}
}

func (l *Limiter) sweepLoop() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-l.stop:
			return
		case <-ticker.C:
			now := l.now()
			l.mu.Lock()
			l.evictIdleLocked(now)
			l.mu.Unlock()
		}
	}
}
