package fed

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// fakeFetch is a scripted rate source for the cache tests: it counts calls and
// answers with the currently configured rate or error.
type fakeFetch struct {
	mu    sync.Mutex
	rate  Rate
	err   error
	calls int
}

func (f *fakeFetch) fetchFn(context.Context) (Rate, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return Rate{}, f.err
	}
	return f.rate, nil
}

func (f *fakeFetch) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *fakeFetch) succeed(rate Rate) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rate, f.err = rate, nil
}

func (f *fakeFetch) fail(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

// localTime builds an instant on the system clock, anchored to the host's
// timezone so the calendar day is the same whatever TZ the suite runs under.
func localTime(year int, month time.Month, day, hour, minute int) time.Time {
	return time.Date(year, month, day, hour, minute, 0, 0, time.Local)
}

// testRate is a fixed reference rate for the cache tests.
func testRate() Rate {
	delta := 25.0
	return Rate{
		Date:             time.Date(2026, 9, 21, 0, 0, 0, 0, time.Local),
		TargetLow:        3.75,
		TargetHigh:       4.00,
		Effective:        3.88,
		EffectiveDeltaBp: &delta,
	}
}

func TestCacheFetchesOncePerCalendarDay(t *testing.T) {
	clock := localTime(2026, time.September, 21, 23, 0)
	source := &fakeFetch{rate: testRate()}
	cache := NewDailyCache(source.fetchFn)
	cache.Now = func() time.Time { return clock }

	for i := 0; i < 3; i++ {
		rate, err := cache.Get(context.Background(), false)
		if err != nil {
			t.Fatalf("Get() #%d error = %v", i, err)
		}
		if rate.Effective != 3.88 {
			t.Fatalf("Get() #%d Effective = %v, want 3.88", i, rate.Effective)
		}
	}
	if got := source.callCount(); got != 1 {
		t.Fatalf("fetch calls = %d, want 1: the same calendar day must be served from the cache", got)
	}

	// Still the same civil day, just before midnight.
	clock = localTime(2026, time.September, 21, 23, 59)
	if _, err := cache.Get(context.Background(), false); err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got := source.callCount(); got != 1 {
		t.Errorf("fetch calls = %d, want 1 before the next midnight", got)
	}

	// The next calendar day has started.
	clock = localTime(2026, time.September, 22, 0, 1)
	if _, err := cache.Get(context.Background(), false); err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got := source.callCount(); got != 2 {
		t.Errorf("fetch calls = %d, want 2 after the calendar day turned over", got)
	}
}

func TestCacheForceRefetches(t *testing.T) {
	clock := localTime(2026, time.September, 21, 10, 0)
	source := &fakeFetch{rate: testRate()}
	cache := NewDailyCache(source.fetchFn)
	cache.Now = func() time.Time { return clock }

	if _, err := cache.Get(context.Background(), false); err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if _, err := cache.Get(context.Background(), false); err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got := source.callCount(); got != 1 {
		t.Fatalf("fetch calls = %d, want 1 for two cached reads", got)
	}

	if _, err := cache.Get(context.Background(), true); err != nil {
		t.Fatalf("Get(force) error = %v", err)
	}
	if _, err := cache.Get(context.Background(), true); err != nil {
		t.Fatalf("Get(force) error = %v", err)
	}
	if got := source.callCount(); got != 3 {
		t.Errorf("fetch calls = %d, want 3: force must bypass the day boundary every time", got)
	}
}

func TestCacheBacksOffOnFailureAndResetsOnSuccess(t *testing.T) {
	boom := errors.New("fred unreachable")
	clock := localTime(2026, time.September, 21, 10, 0)
	source := &fakeFetch{rate: testRate()}
	cache := NewDailyCache(source.fetchFn)
	cache.Now = func() time.Time { return clock }

	source.fail(boom)
	if _, err := cache.Get(context.Background(), false); !errors.Is(err, boom) {
		t.Fatalf("Get() error = %v, want %v", err, boom)
	}
	if got := source.callCount(); got != 1 {
		t.Fatalf("fetch calls = %d, want 1", got)
	}

	// Inside the retry window the failure is reported again without hitting the
	// network: a failing source must not be re-fetched on every dashboard tick.
	clock = localTime(2026, time.September, 21, 10, 3)
	if _, err := cache.Get(context.Background(), false); !errors.Is(err, boom) {
		t.Fatalf("Get() inside the retry window error = %v, want %v", err, boom)
	}
	if got := source.callCount(); got != 1 {
		t.Errorf("fetch calls = %d, want 1 inside the retry window", got)
	}

	// The first failure schedules a 5-minute retry.
	clock = localTime(2026, time.September, 21, 10, 6)
	source.succeed(testRate())
	rate, err := cache.Get(context.Background(), false)
	if err != nil {
		t.Fatalf("Get() after the retry window error = %v", err)
	}
	if rate.Effective != 3.88 {
		t.Errorf("Get() Effective = %v, want 3.88", rate.Effective)
	}
	if got := source.callCount(); got != 2 {
		t.Errorf("fetch calls = %d, want 2 after the retry window", got)
	}

	// A success resets the ladder and restarts the calendar-day schedule.
	if _, err := cache.Get(context.Background(), false); err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got := source.callCount(); got != 2 {
		t.Errorf("fetch calls = %d, want the recovered cache to serve a hit", got)
	}
}

func TestCacheForceRetriesImmediatelyAfterFailure(t *testing.T) {
	boom := errors.New("fred unreachable")
	clock := localTime(2026, time.September, 21, 10, 0)
	source := &fakeFetch{rate: testRate()}
	cache := NewDailyCache(source.fetchFn)
	cache.Now = func() time.Time { return clock }

	source.fail(boom)
	if _, err := cache.Get(context.Background(), false); !errors.Is(err, boom) {
		t.Fatalf("Get() error = %v, want %v", err, boom)
	}

	// The dashboard's `r` key must not be swallowed by the retry window.
	source.succeed(testRate())
	if _, err := cache.Get(context.Background(), true); err != nil {
		t.Fatalf("Get(force) error = %v, want a fresh attempt", err)
	}
	if got := source.callCount(); got != 2 {
		t.Errorf("fetch calls = %d, want 2: force bypasses the retry window", got)
	}
}

func TestCacheTreatsNoDataAsAFailure(t *testing.T) {
	clock := localTime(2026, time.September, 21, 10, 0)
	source := &fakeFetch{err: ErrNoData}
	cache := NewDailyCache(source.fetchFn)
	cache.Now = func() time.Time { return clock }

	if _, err := cache.Get(context.Background(), false); !errors.Is(err, ErrNoData) {
		t.Fatalf("Get() error = %v, want ErrNoData", err)
	}

	// Inside the retry window the same failure is reported without a fetch.
	clock = localTime(2026, time.September, 21, 10, 3)
	if _, err := cache.Get(context.Background(), false); !errors.Is(err, ErrNoData) {
		t.Fatalf("Get() error = %v, want ErrNoData", err)
	}
	if got := source.callCount(); got != 1 {
		t.Errorf("fetch calls = %d, want 1 inside the retry window", got)
	}

	// An empty window is retried on the failure schedule, not cached as a rate.
	clock = localTime(2026, time.September, 21, 10, 6)
	if _, err := cache.Get(context.Background(), false); !errors.Is(err, ErrNoData) {
		t.Fatalf("Get() error = %v, want a second attempt to report ErrNoData", err)
	}
	if got := source.callCount(); got != 2 {
		t.Errorf("fetch calls = %d, want 2", got)
	}
}

func TestCacheKeepsTheLastGoodRateOnFailure(t *testing.T) {
	boom := errors.New("fred unreachable")
	clock := localTime(2026, time.September, 21, 10, 0)
	source := &fakeFetch{rate: testRate()}
	cache := NewDailyCache(source.fetchFn)
	cache.Now = func() time.Time { return clock }

	if _, err := cache.Get(context.Background(), false); err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	source.fail(boom)
	clock = localTime(2026, time.September, 22, 10, 0)
	if _, err := cache.Get(context.Background(), false); !errors.Is(err, boom) {
		t.Fatalf("Get() error = %v, want the failure reported", err)
	}

	// The failure is not returned as data, but the last good rate must survive
	// for the dashboard's stale rendering.
	cache.mu.Lock()
	kept := cache.rate
	cache.mu.Unlock()
	if kept.Effective != 3.88 {
		t.Errorf("cached rate after a failure = %v, want the last good 3.88", kept.Effective)
	}
}

func TestCacheSurvivesConcurrentReaders(t *testing.T) {
	clock := localTime(2026, time.September, 21, 10, 0)
	source := &fakeFetch{rate: testRate()}
	cache := NewDailyCache(source.fetchFn)
	cache.Now = func() time.Time { return clock }

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 5; j++ {
				rate, err := cache.Get(context.Background(), false)
				if err != nil {
					t.Errorf("Get() error = %v", err)
					return
				}
				// Whatever the interleaving, a caller must never observe a
				// half-built rate.
				if rate.Effective != 3.88 || rate.TargetLow != 3.75 || rate.TargetHigh != 4.00 {
					t.Errorf("Get() returned %+v, want the full rate", rate)
					return
				}
			}
		}()
	}
	wg.Wait()
}

func TestFailureBackoffDoublesToACeiling(t *testing.T) {
	cases := []struct {
		failures int
		want     time.Duration
	}{
		{0, failureRetryInterval},
		{1, 5 * time.Minute},
		{2, 10 * time.Minute},
		{3, 20 * time.Minute},
		{4, 30 * time.Minute},
		{9, 30 * time.Minute},
		{100, 30 * time.Minute},
	}
	for _, tt := range cases {
		if got := failureBackoff(tt.failures); got != tt.want {
			t.Errorf("failureBackoff(%d) = %v, want %v", tt.failures, got, tt.want)
		}
	}
}

func TestCacheBacksOffToTheCeilingUnderAnOutage(t *testing.T) {
	boom := errors.New("fred unreachable")
	clock := localTime(2026, time.September, 21, 0, 0)
	source := &fakeFetch{err: boom}
	cache := NewDailyCache(source.fetchFn)
	cache.Now = func() time.Time { return clock }

	wantIntervals := []time.Duration{0, 5 * time.Minute, 10 * time.Minute, 20 * time.Minute, 30 * time.Minute, 30 * time.Minute}
	for i, gap := range wantIntervals {
		clock = clock.Add(gap)
		if _, err := cache.Get(context.Background(), false); !errors.Is(err, boom) {
			t.Fatalf("attempt %d: Get() error = %v, want %v", i, err, boom)
		}
	}
	if got := source.callCount(); got != len(wantIntervals) {
		t.Fatalf("fetch calls = %d, want %d", got, len(wantIntervals))
	}

	// Every attempt inside the 30-minute ceiling window must be served from the
	// failure state instead of the network.
	for i := 0; i < 5; i++ {
		clock = clock.Add(time.Minute)
		if _, err := cache.Get(context.Background(), false); !errors.Is(err, boom) {
			t.Fatalf("quiet attempt %d: Get() error = %v, want %v", i, err, boom)
		}
	}
	if got := source.callCount(); got != len(wantIntervals) {
		t.Errorf("fetch calls = %d, want no requests inside the backoff window", got)
	}
}

func TestNewDailyCacheAppliesDefaults(t *testing.T) {
	cache := NewDailyCache(func(context.Context) (Rate, error) { return Rate{}, nil })
	if cache.Now == nil {
		t.Fatal("NewDailyCache().Now is nil, want a clock default")
	}
	if cache.fetch == nil {
		t.Fatal("NewDailyCache().fetch is nil")
	}
	fixed := time.Date(2026, time.September, 21, 10, 0, 0, 0, time.Local)
	cache.Now = func() time.Time { return fixed }
	if !cache.now().Equal(fixed) {
		t.Error("now() disagrees with the injected clock")
	}
}
