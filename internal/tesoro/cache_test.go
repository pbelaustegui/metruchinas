package tesoro

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// newYork loads the publication timezone from the embedded database, so these
// tests do not depend on the host having a tzdata package installed.
func newYork(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(publicationTimezone)
	if err != nil {
		t.Fatalf("LoadLocation(%q) error = %v", publicationTimezone, err)
	}
	return loc
}

// localDay builds a civil date in the system's timezone, the way the client
// anchors Curve.Date.
func localDay(year int, month time.Month, day int) time.Time {
	return time.Date(year, month, day, 0, 0, 0, 0, time.Local)
}

// nyTime builds an instant on the publication clock.
func nyTime(loc *time.Location, year int, month time.Month, day, hour, minute int) time.Time {
	return time.Date(year, month, day, hour, minute, 0, 0, loc)
}

func TestNextCheckAtFollowsThePublicationSchedule(t *testing.T) {
	ny := newYork(t)

	cases := []struct {
		name string
		now  time.Time
		row  time.Time
		loc  *time.Location
		want time.Time
	}{
		{
			name: "before publication, holding the previous business day",
			now:  nyTime(ny, 2026, time.September, 21, 9, 0),
			row:  localDay(2026, time.September, 18),
			loc:  ny,
			want: nyTime(ny, 2026, time.September, 21, 16, 0),
		},
		{
			name: "a minute before publication",
			now:  nyTime(ny, 2026, time.September, 21, 15, 59),
			row:  localDay(2026, time.September, 18),
			loc:  ny,
			want: nyTime(ny, 2026, time.September, 21, 16, 0),
		},
		{
			name: "exactly at publication the release may not be in yet",
			now:  nyTime(ny, 2026, time.September, 21, 16, 0),
			row:  localDay(2026, time.September, 18),
			loc:  ny,
			want: nyTime(ny, 2026, time.September, 21, 16, 15),
		},
		{
			name: "after publication, holding today's row",
			now:  nyTime(ny, 2026, time.September, 21, 17, 30),
			row:  localDay(2026, time.September, 21),
			loc:  ny,
			want: nyTime(ny, 2026, time.September, 22, 16, 0),
		},
		{
			name: "after publication, holding an older row (late publish)",
			now:  nyTime(ny, 2026, time.September, 21, 17, 30),
			row:  localDay(2026, time.September, 18),
			loc:  ny,
			want: nyTime(ny, 2026, time.September, 21, 17, 45),
		},
		{
			name: "late publish retry stops at the next publication midnight",
			now:  nyTime(ny, 2026, time.September, 21, 23, 55),
			row:  localDay(2026, time.September, 18),
			loc:  ny,
			want: nyTime(ny, 2026, time.September, 22, 0, 0),
		},
		{
			name: "after midnight the schedule restarts for the new day",
			now:  nyTime(ny, 2026, time.September, 22, 0, 5),
			row:  localDay(2026, time.September, 18),
			loc:  ny,
			want: nyTime(ny, 2026, time.September, 22, 16, 0),
		},
		{
			// Treasury publishes on business days only, so a weekend has no
			// release to wait for and no row to expect. There is nothing to
			// retry either: a Saturday waits for Monday.
			name: "Saturday morning waits for Monday's release",
			now:  nyTime(ny, 2026, time.September, 19, 12, 0),
			row:  localDay(2026, time.September, 18),
			loc:  ny,
			want: nyTime(ny, 2026, time.September, 21, 16, 0),
		},
		{
			name: "Saturday evening has no release to retry",
			now:  nyTime(ny, 2026, time.September, 19, 17, 0),
			row:  localDay(2026, time.September, 18),
			loc:  ny,
			want: nyTime(ny, 2026, time.September, 21, 16, 0),
		},
		{
			name: "Sunday after midnight does not start a retry loop",
			now:  nyTime(ny, 2026, time.September, 20, 0, 5),
			row:  localDay(2026, time.September, 18),
			loc:  ny,
			want: nyTime(ny, 2026, time.September, 21, 16, 0),
		},
		{
			name: "Sunday evening waits for Monday's release",
			now:  nyTime(ny, 2026, time.September, 20, 20, 0),
			row:  localDay(2026, time.September, 18),
			loc:  ny,
			want: nyTime(ny, 2026, time.September, 21, 16, 0),
		},
		{
			// Friday's release is expected, so a late one is retried — but only
			// as far as the next publication-zone midnight, after which the
			// weekend rule takes over.
			name: "Friday after the release with only Thursday's row still retries",
			now:  nyTime(ny, 2026, time.September, 18, 17, 0),
			row:  localDay(2026, time.September, 17),
			loc:  ny,
			want: nyTime(ny, 2026, time.September, 18, 17, 15),
		},
		{
			name: "after Friday's midnight the weekend rule takes over",
			now:  nyTime(ny, 2026, time.September, 19, 0, 5),
			row:  localDay(2026, time.September, 17),
			loc:  ny,
			want: nyTime(ny, 2026, time.September, 21, 16, 0),
		},
		{
			name: "Friday evening after Friday's row schedules Monday",
			now:  nyTime(ny, 2026, time.September, 18, 17, 0),
			row:  localDay(2026, time.September, 18),
			loc:  ny,
			want: nyTime(ny, 2026, time.September, 21, 16, 0),
		},
		{
			name: "no row held yet, before publication",
			now:  nyTime(ny, 2026, time.September, 21, 9, 0),
			loc:  ny,
			want: nyTime(ny, 2026, time.September, 21, 16, 0),
		},
		{
			name: "no row held yet, after publication",
			now:  nyTime(ny, 2026, time.September, 21, 17, 0),
			loc:  ny,
			want: nyTime(ny, 2026, time.September, 21, 17, 15),
		},
		{
			// A row dated ahead of today is still the newest one held, so the
			// schedule does not look for today's row again; the next release
			// strictly after the clock is still this afternoon's.
			name: "a future-dated row waits for the next release",
			now:  nyTime(ny, 2026, time.September, 21, 9, 0),
			row:  localDay(2026, time.September, 22),
			loc:  ny,
			want: nyTime(ny, 2026, time.September, 21, 16, 0),
		},
		{
			name: "the row date zone never shifts its civil date",
			now:  nyTime(ny, 2026, time.September, 21, 17, 0),
			row:  time.Date(2026, time.September, 21, 0, 0, 0, 0, time.UTC),
			loc:  ny,
			want: nyTime(ny, 2026, time.September, 22, 16, 0),
		},
		{
			// 2026-03-08 is the spring transition and 2026-11-01 the fall one;
			// both are Sundays, so the weekend skip carries the Friday release
			// across the change of offset.
			name: "the Friday before the spring transition schedules the Monday after it",
			now:  nyTime(ny, 2026, time.March, 6, 17, 0),
			row:  localDay(2026, time.March, 6),
			loc:  ny,
			want: nyTime(ny, 2026, time.March, 9, 16, 0),
		},
		{
			name: "the Monday after the spring transition releases at 16:00 local",
			now:  nyTime(ny, 2026, time.March, 9, 0, 5),
			row:  localDay(2026, time.March, 6),
			loc:  ny,
			want: nyTime(ny, 2026, time.March, 9, 16, 0),
		},
		{
			name: "the Friday before the fall transition schedules the Monday after it",
			now:  nyTime(ny, 2026, time.October, 30, 17, 0),
			row:  localDay(2026, time.October, 30),
			loc:  ny,
			want: nyTime(ny, 2026, time.November, 2, 16, 0),
		},
		{
			name: "the Monday after the fall transition releases at 16:00 local",
			now:  nyTime(ny, 2026, time.November, 2, 0, 5),
			row:  localDay(2026, time.October, 30),
			loc:  ny,
			want: nyTime(ny, 2026, time.November, 2, 16, 0),
		},
		{
			// 2027-01-01 is a Friday, so the schedule keeps it: without a holiday
			// calendar a weekday holiday is treated as a publication day, and the
			// bounded late-publish retry is what covers the missing row.
			name: "a year rollover lands on the next weekday, holiday or not",
			now:  nyTime(ny, 2026, time.December, 31, 17, 0),
			row:  localDay(2026, time.December, 31),
			loc:  ny,
			want: nyTime(ny, 2027, time.January, 1, 16, 0),
		},
		{
			name: "a nil location is treated as UTC",
			now:  nyTime(time.UTC, 2026, time.September, 21, 9, 0),
			row:  localDay(2026, time.September, 18),
			want: nyTime(time.UTC, 2026, time.September, 21, 16, 0),
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got := NextCheckAt(tt.now, tt.row, tt.loc)
			if !got.Equal(tt.want) {
				t.Fatalf("NextCheckAt() = %v, want %v", got, tt.want)
			}
			if tt.loc != nil {
				// Every point of the schedule is aligned to the publication
				// interval on the publication clock: the release hour, the next
				// midnight, and every retry in between.
				inLoc := got.In(tt.loc)
				if inLoc.Second() != 0 || inLoc.Nanosecond() != 0 || inLoc.Minute()%int(publicationRetryInterval.Minutes()) != 0 {
					t.Errorf("NextCheckAt() = %v, want an instant aligned to the publication interval in %v", inLoc, tt.loc)
				}
			}
		})
	}
}

func TestNextCheckAtKeepsTheDSTOffsetCorrect(t *testing.T) {
	ny := newYork(t)

	// The release stays at 16:00 local wall time across both transitions: the
	// schedule is built with time.Date in the publication zone, never by adding
	// 24 hours to an instant. The Friday before the spring transition is EST
	// (-5) and the Monday after it is EDT (-4); the Friday before the fall
	// transition is EDT (-4) and the Monday after it is EST (-5).
	spring := NextCheckAt(nyTime(ny, 2026, time.March, 6, 17, 0), localDay(2026, time.March, 6), ny)
	if want := nyTime(ny, 2026, time.March, 9, 16, 0); !spring.Equal(want) {
		t.Errorf("NextCheckAt() before the spring transition = %v, want %v", spring, want)
	}
	if _, offset := spring.Zone(); offset != -4*3600 {
		t.Errorf("NextCheckAt() before the spring transition = %v, offset = %ds, want EDT", spring, offset)
	}

	fall := NextCheckAt(nyTime(ny, 2026, time.October, 30, 17, 0), localDay(2026, time.October, 30), ny)
	if want := nyTime(ny, 2026, time.November, 2, 16, 0); !fall.Equal(want) {
		t.Errorf("NextCheckAt() before the fall transition = %v, want %v", fall, want)
	}
	if _, offset := fall.Zone(); offset != -5*3600 {
		t.Errorf("NextCheckAt() before the fall transition = %v, offset = %ds, want EST", fall, offset)
	}
}

// testBusinessDay reports whether a calendar date is a Treasury publication day.
// The simulation below keeps its own weekday rule on purpose: a broken
// production predicate must not be able to make the fake source agree with the
// schedule it is there to check.
func testBusinessDay(day time.Time) bool {
	return day.Weekday() != time.Saturday && day.Weekday() != time.Sunday
}

// publishedDate is the newest row the real source would publish by the given
// New York instant: the previous business day before the 16:00 release, that
// day itself once the release has happened.
func publishedDate(et time.Time, loc *time.Location) time.Time {
	day := time.Date(et.Year(), et.Month(), et.Day(), 0, 0, 0, 0, time.UTC)
	release := time.Date(day.Year(), day.Month(), day.Day(), publicationHour, 0, 0, 0, loc)
	if testBusinessDay(day) && !et.Before(release) {
		return day
	}
	for {
		day = day.AddDate(0, 0, -1)
		if testBusinessDay(day) {
			return day
		}
	}
}

func TestCacheMakesOneRequestAcrossAWeekendWindow(t *testing.T) {
	// The dashboard heartbeat runs every 30 seconds. Replayed across a weekend
	// window — Friday 15:00 to Monday 09:00 New York time — a session must ask
	// the source exactly once: after Friday's 16:00 release. The schedule this
	// replaces treated Saturday and Sunday as release days, so it re-checked
	// every 15 minutes from 16:00 to midnight on each of them (67 requests over
	// the same window, measured with this harness).
	ny := newYork(t)
	clock := nyTime(ny, 2026, time.September, 18, 9, 0)
	windowStart := nyTime(ny, 2026, time.September, 18, 15, 0)
	end := nyTime(ny, 2026, time.September, 21, 9, 0)

	requests := 0
	cache := newCache(func(context.Context) (Curve, error) {
		requests++
		return testCurve(publishedDate(clock.In(ny), ny)), nil
	}, func() time.Time { return clock }, ny)

	// Friday morning: a running session already holds Thursday's row, so warm
	// the cache first and measure the window on its own.
	if _, err := cache.Get(context.Background(), false); err != nil {
		t.Fatalf("priming Get() error = %v", err)
	}
	requests = 0
	clock = windowStart

	var fetches []time.Time
	for seen := 0; !clock.After(end); clock = clock.Add(30 * time.Second) {
		if _, err := cache.Get(context.Background(), false); err != nil {
			t.Fatalf("Get() at %v error = %v", clock.In(ny), err)
		}
		if requests != seen {
			seen = requests
			fetches = append(fetches, clock.In(ny))
		}
	}

	for _, at := range fetches {
		t.Logf("source request at %s", at.Format("Mon 2006-01-02 15:04:05 MST"))
	}
	if len(fetches) != 1 {
		t.Errorf("source requests from Friday 15:00 to Monday 09:00 = %d, want 1 (Friday's 16:00 release)", len(fetches))
	}
}

// fakeFetch is a counting Curve fetcher.
type fakeFetch struct {
	mu    sync.Mutex
	calls int
	curve Curve
	err   error
}

func (f *fakeFetch) fetchFn(context.Context) (Curve, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return Curve{}, f.err
	}
	return f.curve, nil
}

func (f *fakeFetch) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *fakeFetch) fail(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

func (f *fakeFetch) succeed(curve Curve) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = nil
	f.curve = curve
}

// testCurve is a curve dated on the given civil day.
func testCurve(date time.Time) Curve {
	points := make([]Point, 0, len(tenors))
	for _, tenor := range tenors {
		yield := 4.0 + float64(len(tenor.Key))/10
		delta := 3.0
		points = append(points, Point{Tenor: tenor.Key, Label: tenor.Label, Yield: yield, DeltaBp: &delta})
	}
	return Curve{Date: date, Points: points}
}

func TestCacheFetchesOncePerBusinessDay(t *testing.T) {
	ny := newYork(t)
	clock := nyTime(ny, 2026, time.September, 21, 17, 0)
	source := &fakeFetch{curve: testCurve(localDay(2026, time.September, 21))}
	cache := newCache(source.fetchFn, func() time.Time { return clock }, ny)

	for i := 0; i < 5; i++ {
		curve, err := cache.Get(context.Background(), false)
		if err != nil {
			t.Fatalf("Get() #%d error = %v", i, err)
		}
		if len(curve.Points) != len(tenors) {
			t.Fatalf("Get() #%d returned %d points, want %d", i, len(curve.Points), len(tenors))
		}
	}
	if got := source.callCount(); got != 1 {
		t.Fatalf("fetch calls = %d, want 1: the same business day must be served from the cache", got)
	}

	// Still the same business day, before the next release.
	clock = nyTime(ny, 2026, time.September, 22, 15, 0)
	if _, err := cache.Get(context.Background(), false); err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got := source.callCount(); got != 1 {
		t.Errorf("fetch calls = %d, want 1 before the next release", got)
	}

	// The next release has passed.
	clock = nyTime(ny, 2026, time.September, 22, 16, 1)
	source.succeed(testCurve(localDay(2026, time.September, 22)))
	curve, err := cache.Get(context.Background(), false)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got := source.callCount(); got != 2 {
		t.Errorf("fetch calls = %d, want 2 after the next release", got)
	}
	if want := localDay(2026, time.September, 22); !curve.Date.Equal(want) {
		t.Errorf("Get() date = %v, want the newly published %v", curve.Date, want)
	}
}

func TestCacheForceBypassesTheSchedule(t *testing.T) {
	ny := newYork(t)
	clock := nyTime(ny, 2026, time.September, 21, 17, 0)
	source := &fakeFetch{curve: testCurve(localDay(2026, time.September, 21))}
	cache := newCache(source.fetchFn, func() time.Time { return clock }, ny)

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
	if got := source.callCount(); got != 2 {
		t.Errorf("fetch calls = %d, want 2 after a forced refresh", got)
	}
	if _, err := cache.Get(context.Background(), true); err != nil {
		t.Fatalf("Get(force) error = %v", err)
	}
	if got := source.callCount(); got != 3 {
		t.Errorf("fetch calls = %d, want 3: force must bypass the schedule every time", got)
	}
}

func TestCacheSchedulesAShortRetryAfterFailure(t *testing.T) {
	ny := newYork(t)
	boom := errors.New("treasury unreachable")
	clock := nyTime(ny, 2026, time.September, 21, 17, 0)
	source := &fakeFetch{curve: testCurve(localDay(2026, time.September, 21))}
	cache := newCache(source.fetchFn, func() time.Time { return clock }, ny)

	source.fail(boom)
	_, err := cache.Get(context.Background(), false)
	if !errors.Is(err, boom) {
		t.Fatalf("Get() error = %v, want %v", err, boom)
	}
	if got := source.callCount(); got != 1 {
		t.Fatalf("fetch calls = %d, want 1", got)
	}

	// Inside the retry window the failure is reported again without hitting the
	// network: a failing source must not be re-fetched on every dashboard tick.
	clock = nyTime(ny, 2026, time.September, 21, 17, 3)
	_, err = cache.Get(context.Background(), false)
	if !errors.Is(err, boom) {
		t.Fatalf("Get() inside the retry window error = %v, want %v", err, boom)
	}
	if got := source.callCount(); got != 1 {
		t.Errorf("fetch calls = %d, want 1 inside the retry window", got)
	}

	// Once the window expires the cache tries again, and a recovery resumes the
	// publication schedule.
	source.succeed(testCurve(localDay(2026, time.September, 21)))
	clock = nyTime(ny, 2026, time.September, 21, 17, 6)
	curve, err := cache.Get(context.Background(), false)
	if err != nil {
		t.Fatalf("Get() after the retry window error = %v", err)
	}
	if got := source.callCount(); got != 2 {
		t.Errorf("fetch calls = %d, want 2 after the retry window", got)
	}
	if len(curve.Points) != len(tenors) {
		t.Errorf("Get() returned %d points, want %d", len(curve.Points), len(tenors))
	}
	if _, err := cache.Get(context.Background(), false); err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got := source.callCount(); got != 2 {
		t.Errorf("fetch calls = %d, want the recovered cache to serve hits again", got)
	}
}

func TestCacheForceRetriesImmediatelyAfterFailure(t *testing.T) {
	ny := newYork(t)
	boom := errors.New("treasury unreachable")
	clock := nyTime(ny, 2026, time.September, 21, 17, 0)
	source := &fakeFetch{curve: testCurve(localDay(2026, time.September, 21))}
	cache := newCache(source.fetchFn, func() time.Time { return clock }, ny)

	source.fail(boom)
	if _, err := cache.Get(context.Background(), false); !errors.Is(err, boom) {
		t.Fatalf("Get() error = %v, want %v", err, boom)
	}

	// The dashboard's `r` key must not be swallowed by the retry window.
	source.succeed(testCurve(localDay(2026, time.September, 21)))
	if _, err := cache.Get(context.Background(), true); err != nil {
		t.Fatalf("Get(force) error = %v, want a fresh attempt", err)
	}
	if got := source.callCount(); got != 2 {
		t.Errorf("fetch calls = %d, want 2: force bypasses the retry window", got)
	}
}

func TestCacheTreatsNoDataAsAFailure(t *testing.T) {
	ny := newYork(t)
	clock := nyTime(ny, 2026, time.September, 21, 17, 0)
	source := &fakeFetch{err: ErrNoData}
	cache := newCache(source.fetchFn, func() time.Time { return clock }, ny)

	if _, err := cache.Get(context.Background(), false); !errors.Is(err, ErrNoData) {
		t.Fatalf("Get() error = %v, want ErrNoData", err)
	}

	// Inside the retry window the same failure is reported without a fetch.
	clock = nyTime(ny, 2026, time.September, 21, 17, 3)
	if _, err := cache.Get(context.Background(), false); !errors.Is(err, ErrNoData) {
		t.Fatalf("Get() error = %v, want ErrNoData", err)
	}
	if got := source.callCount(); got != 1 {
		t.Errorf("fetch calls = %d, want 1 inside the retry window", got)
	}

	// A year with no data is retried on the failure schedule, not on the
	// publication schedule, and never cached as a curve.
	clock = nyTime(ny, 2026, time.September, 21, 17, 6)
	if _, err := cache.Get(context.Background(), false); !errors.Is(err, ErrNoData) {
		t.Fatalf("Get() error = %v, want a second attempt to report ErrNoData", err)
	}
	if got := source.callCount(); got != 2 {
		t.Errorf("fetch calls = %d, want 2", got)
	}
}

func TestCacheSurvivesConcurrentReaders(t *testing.T) {
	ny := newYork(t)
	clock := nyTime(ny, 2026, time.September, 21, 17, 0)
	source := &fakeFetch{curve: testCurve(localDay(2026, time.September, 21))}
	cache := newCache(source.fetchFn, func() time.Time { return clock }, ny)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 5; j++ {
				curve, err := cache.Get(context.Background(), false)
				if err != nil {
					t.Errorf("Get() error = %v", err)
					return
				}
				// Whatever the interleaving, a caller must never observe a
				// half-built curve.
				if len(curve.Points) != len(tenors) {
					t.Errorf("Get() returned %d points, want %d", len(curve.Points), len(tenors))
					return
				}
			}
		}()
	}
	wg.Wait()
}

func TestPublicationLocationComesFromTheEmbeddedDatabase(t *testing.T) {
	loc := publicationLocation()
	if loc.String() != publicationTimezone {
		t.Errorf("publicationLocation() = %q, want %q", loc, publicationTimezone)
	}
}

func TestNewPublisherCacheAppliesDefaults(t *testing.T) {
	cache := NewPublisherCache(func(context.Context) (Curve, error) { return Curve{}, nil })
	if cache.Now == nil {
		t.Fatal("NewPublisherCache().Now is nil, want a clock default")
	}
	if cache.loc == nil || cache.loc.String() != publicationTimezone {
		t.Errorf("NewPublisherCache() schedule location = %v, want %q", cache.loc, publicationTimezone)
	}
	if cache.fetch == nil {
		t.Fatal("NewPublisherCache().fetch is nil")
	}
}

func TestFailureBackoffDoublesToACeiling(t *testing.T) {
	cases := []struct {
		failures int
		want     time.Duration
	}{
		{0, failureRetryInterval},
		{1, failureRetryInterval},
		{2, 2 * failureRetryInterval},
		{3, 4 * failureRetryInterval},
		{4, failureRetryCeiling},
		{5, failureRetryCeiling},
		{1000, failureRetryCeiling},
	}
	for _, tt := range cases {
		if got := failureBackoff(tt.failures); got != tt.want {
			t.Errorf("failureBackoff(%d) = %v, want %v", tt.failures, got, tt.want)
		}
	}
}

func TestCacheBacksOffOnConsecutiveFailures(t *testing.T) {
	ny := newYork(t)
	boom := errors.New("treasury unreachable")
	clock := nyTime(ny, 2026, time.September, 21, 17, 0)
	source := &fakeFetch{curve: testCurve(localDay(2026, time.September, 21))}
	cache := newCache(source.fetchFn, func() time.Time { return clock }, ny)

	// Four consecutive failures, each measured at its own schedule: 5 min,
	// 10 min, 20 min, then the 30-minute ceiling.
	want := []time.Duration{
		5 * time.Minute,
		10 * time.Minute,
		20 * time.Minute,
		30 * time.Minute,
		30 * time.Minute,
	}
	source.fail(boom)
	for attempt, gap := range want {
		if _, err := cache.Get(context.Background(), false); !errors.Is(err, boom) {
			t.Fatalf("attempt %d: Get() error = %v, want %v", attempt+1, err, boom)
		}
		if got := source.callCount(); got != attempt+1 {
			t.Fatalf("attempt %d: fetch calls = %d, want %d", attempt+1, got, attempt+1)
		}
		// Just before the window closes nothing is fetched; at the window edge
		// exactly one attempt is made.
		clock = clock.Add(gap - time.Second)
		if _, err := cache.Get(context.Background(), false); !errors.Is(err, boom) {
			t.Fatalf("attempt %d: Get() inside the window error = %v, want %v", attempt+1, err, boom)
		}
		if got := source.callCount(); got != attempt+1 {
			t.Fatalf("attempt %d: fetch calls = %d inside the retry window, want %d", attempt+1, got, attempt+1)
		}
		clock = clock.Add(time.Second)
	}
}

func TestCacheResetsTheFailureBackoffOnSuccess(t *testing.T) {
	ny := newYork(t)
	boom := errors.New("treasury unreachable")
	clock := nyTime(ny, 2026, time.September, 21, 17, 0)
	source := &fakeFetch{curve: testCurve(localDay(2026, time.September, 21))}
	cache := newCache(source.fetchFn, func() time.Time { return clock }, ny)

	// Three consecutive failures push the retry window out to 20 minutes.
	source.fail(boom)
	for _, advance := range []time.Duration{0, 5*time.Minute + time.Second, 10*time.Minute + time.Second} {
		clock = clock.Add(advance)
		if _, err := cache.Get(context.Background(), false); !errors.Is(err, boom) {
			t.Fatalf("Get() error = %v, want %v", err, boom)
		}
	}
	clock = clock.Add(20*time.Minute + time.Second)

	// A success resets the progression, so the next failure waits the base
	// interval again rather than the ceiling the run had reached.
	source.succeed(testCurve(localDay(2026, time.September, 21)))
	if _, err := cache.Get(context.Background(), false); err != nil {
		t.Fatalf("Get() error = %v, want the recovered source", err)
	}
	source.fail(boom)
	clock = clock.Add(time.Second)
	if _, err := cache.Get(context.Background(), true); !errors.Is(err, boom) {
		t.Fatalf("forced Get() error = %v, want %v", err, boom)
	}
	before := source.callCount()

	clock = clock.Add(failureRetryInterval - time.Second)
	if _, err := cache.Get(context.Background(), false); !errors.Is(err, boom) {
		t.Fatalf("Get() inside the reset window error = %v, want %v", err, boom)
	}
	if got := source.callCount(); got != before {
		t.Errorf("fetch calls = %d inside the reset retry window, want %d: the backoff did not restart at the base interval", got, before)
	}
	clock = clock.Add(time.Second)
	if _, err := cache.Get(context.Background(), false); !errors.Is(err, boom) {
		t.Fatalf("Get() after the reset window error = %v, want %v", err, boom)
	}
	if got := source.callCount(); got != before+1 {
		t.Errorf("fetch calls = %d after the reset retry window, want %d", got, before+1)
	}
}

// countOutageRequests replays the dashboard heartbeat (30 seconds) from start to
// end against a source that only ever fails, and returns the instants at which
// the cache actually asked it.
func countOutageRequests(t *testing.T, loc *time.Location, start, end time.Time) (int, []time.Time) {
	t.Helper()
	clock := start
	requests := 0
	cache := newCache(func(context.Context) (Curve, error) {
		requests++
		return Curve{}, errors.New("treasury unreachable")
	}, func() time.Time { return clock }, loc)

	var asked []time.Time
	for seen := 0; !clock.After(end); clock = clock.Add(30 * time.Second) {
		if _, err := cache.Get(context.Background(), false); err == nil {
			t.Fatalf("Get() at %v error = nil, want the outage reported", clock.In(loc))
		}
		if requests != seen {
			seen = requests
			asked = append(asked, clock.In(loc))
		}
	}
	return requests, asked
}

func TestCacheBoundsOutageRequests(t *testing.T) {
	// A source outage must not become a heartbeat of its own. With a 30-minute
	// ceiling the worst case is two attempts per hour, however long the outage
	// lasts; the fixed 5-minute cadence this replaces asked 289 times in the
	// first window and 8641 times in the second.
	ny := newYork(t)
	cases := []struct {
		name       string
		start, end time.Time
		max        int
	}{
		{
			name:  "24-hour outage",
			start: nyTime(ny, 2026, time.September, 18, 9, 0),
			end:   nyTime(ny, 2026, time.September, 19, 9, 0),
			max:   52, // 4 attempts to reach the ceiling, then 2 per hour
		},
		{
			name:  "30-day outage",
			start: nyTime(ny, 2026, time.September, 18, 9, 0),
			end:   nyTime(ny, 2026, time.October, 18, 9, 0),
			max:   1444, // 2 per hour for 30 days, plus the ramp
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			requests, asked := countOutageRequests(t, ny, tt.start, tt.end)
			gaps := make([]string, 0, 5)
			for i := 1; i < len(asked) && i <= 5; i++ {
				gaps = append(gaps, asked[i].Sub(asked[i-1]).Round(time.Second).String())
			}
			t.Logf("%s: %d source requests; first gaps %v", tt.name, requests, gaps)
			if requests > tt.max {
				t.Errorf("%s: %d source requests, want at most %d with a %v ceiling", tt.name, requests, tt.max, failureRetryCeiling)
			}
		})
	}
}
