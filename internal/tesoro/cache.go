package tesoro

import (
	"context"
	"sync"
	"time"

	// The publication schedule is an external fact expressed in a named
	// timezone. Embedding the zone database keeps `go build` output a single
	// self-contained binary that does not depend on the host having a tzdata
	// package installed.
	_ "time/tzdata"
)

const (
	// publicationTimezone is where Treasury releases the curve: roughly 16:00
	// New York time on business days. It is used only to schedule the cache and
	// never to render anything, so the dashboard keeps showing times in the
	// system's timezone.
	publicationTimezone = "America/New_York"
	// publicationHour is the hour of the release in publicationTimezone.
	publicationHour = 16
	// publicationRetryInterval is how often the cache re-checks the source after
	// the release hour when the newest row it holds is still older than today:
	// Treasury does publish late, and a holiday delays the row further. The
	// retry never runs past the next publication-zone midnight, so a holiday
	// evening cannot become an all-night request loop.
	publicationRetryInterval = 15 * time.Minute
	// failureRetryInterval is how long the cache waits after a failed fetch
	// before looking at the source again. It is deliberately shorter than a
	// publication cycle: a transient failure should not hide the curve for a day.
	failureRetryInterval = 5 * time.Minute
	// failureRetryCeiling caps the failure backoff, so an outage costs at most
	// two requests per hour however long it lasts. Without a ceiling a source
	// that stays down would be asked on a fixed short cadence for as long as the
	// dashboard runs: a 30-day outage at the base interval is 8641 requests.
	failureRetryCeiling = 30 * time.Minute
)

// failureBackoff returns how long the cache waits before the next attempt after
// consecutiveFailures failures in a row: failureRetryInterval doubled per
// consecutive failure, capped at failureRetryCeiling. Every successful fetch
// resets the progression, so an isolated hiccup costs one short retry while a
// long outage settles at the ceiling.
func failureBackoff(consecutiveFailures int) time.Duration {
	if consecutiveFailures < 1 {
		consecutiveFailures = 1
	}
	backoff := failureRetryInterval
	for attempt := 1; attempt < consecutiveFailures; attempt++ {
		if backoff >= failureRetryCeiling {
			break
		}
		backoff *= 2
	}
	if backoff > failureRetryCeiling {
		return failureRetryCeiling
	}
	return backoff
}

// Cache wraps a curve fetcher so the dashboard hits the network at most once per
// business-day publication cycle instead of on every refresh tick.
//
// This is not a plain TTL. NextCheckAt decides when a new publication can exist
// at all, which is what makes one request per business day possible: after
// 16:00 New York time with today's row in hand, no newer row can appear until
// the next release, so the cache answers from memory for the rest of the day.
type Cache struct {
	// Now returns the current time. It defaults to time.Now and exists so the
	// publication schedule can be exercised with an injected clock.
	Now func() time.Time

	fetch func(context.Context) (Curve, error)
	loc   *time.Location

	mu sync.Mutex
	// curve is the last successfully fetched curve, served on a cache hit.
	curve Curve
	// fetchOK reports whether the last fetch succeeded. A failed fetch leaves
	// curve in place but reports the error instead of the curve, so the
	// dashboard's own stale rendering stays the single source of truth about
	// what is on screen.
	fetchOK bool
	// lastErr is the failure reported while the retry window is open.
	lastErr error
	// failures counts consecutive failed fetches, which the retry backoff is
	// computed from.
	failures int
	// nextCheck is the instant the cache may look at the source again.
	nextCheck time.Time
}

// NewPublisherCache returns a cache around fetch that follows Treasury's
// publication schedule in America/New_York.
func NewPublisherCache(fetch func(context.Context) (Curve, error)) *Cache {
	return newCache(fetch, time.Now, publicationLocation())
}

// newCache builds a cache with an explicit clock and schedule location.
func newCache(fetch func(context.Context) (Curve, error), now func() time.Time, loc *time.Location) *Cache {
	if now == nil {
		now = time.Now
	}
	if loc == nil {
		loc = time.UTC
	}
	return &Cache{Now: now, fetch: fetch, loc: loc}
}

// publicationLocation resolves publicationTimezone, falling back to UTC.
//
// The embedded time/tzdata database makes the lookup succeed on hosts without a
// tzdata package; the fallback keeps a corrupt database from taking the whole
// dashboard down, and costs only a less precise schedule.
func publicationLocation() *time.Location {
	loc, err := time.LoadLocation(publicationTimezone)
	if err != nil {
		return time.UTC
	}
	return loc
}

// Get returns the curve, fetching only when the publication schedule says a new
// one can exist. force bypasses both the schedule and the retry window, which is
// what the dashboard's `r` key asks for.
//
// A successful fetch is cached and the next check is scheduled from the fetched
// publication date. A failed fetch returns the error, schedules a short retry,
// and keeps the last good curve in place — but returns the error, never the
// curve, so the caller never mistakes a failure for fresh data.
//
// Get is intended to be called by one caller at a time: the dashboard holds a
// single in-flight fetch. The mutex keeps the cached state consistent for any
// other reader, and TestCacheSurvivesConcurrentReaders exercises concurrent
// readers to prove a caller never observes a partially built curve.
func (c *Cache) Get(ctx context.Context, force bool) (Curve, error) {
	now := c.now()

	if !force {
		c.mu.Lock()
		fresh := c.fetchOK && now.Before(c.nextCheck)
		cached := c.curve
		retrying := !c.fetchOK && c.lastErr != nil && now.Before(c.nextCheck)
		lastErr := c.lastErr
		c.mu.Unlock()

		switch {
		case fresh:
			return cached, nil
		case retrying:
			// The failure is still inside its retry window: report it again
			// instead of hammering a source that just failed.
			return Curve{}, lastErr
		}
	}

	curve, err := c.fetch(ctx)

	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil {
		c.fetchOK = false
		c.lastErr = err
		c.failures++
		c.nextCheck = now.Add(failureBackoff(c.failures))
		return Curve{}, err
	}
	c.curve = curve
	c.fetchOK = true
	c.lastErr = nil
	c.failures = 0
	c.nextCheck = NextCheckAt(now, curve.Date, c.loc)
	return curve, nil
}

func (c *Cache) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// NextCheckAt returns the next instant the cache should look at the source,
// given the clock and the publication date of the newest row it holds.
//
// The schedule follows Treasury's release calendar in the timezone Treasury
// publishes in:
//
//   - Before the release hour on a business day, the next check is that day's
//     release.
//   - Once the row of the current business day is held, nothing newer can exist
//     until a later release, so the next check is the next business day at the
//     release hour.
//   - After the release hour on a business day with a row older than that day —
//     a late or holiday-delayed release — the cache retries every
//     publicationRetryInterval, never past the next local midnight, at which
//     point the schedule restarts.
//   - On a Saturday or Sunday there is no release at all, so there is nothing to
//     wait for and nothing to retry: the next check is the next business day's
//     release.
//
// The schedule knows weekdays but not holidays. A published holiday (a Monday,
// say) is therefore treated as a publication day: its release passes without a
// row and the bounded retry runs that evening until local midnight. That is the
// accepted limit of a schedule with no holiday calendar, and it costs one
// evening of retries rather than a day of waiting.
//
// now and rowDate are compared as civil dates, so the answer does not depend on
// the zones they were built in: the client's row date is anchored to the
// system's local timezone, and moving it into the publication zone would shift
// its calendar day. A zero rowDate means no row is held yet; a nil loc is
// treated as UTC.
func NextCheckAt(now, rowDate time.Time, loc *time.Location) time.Time {
	if loc == nil {
		loc = time.UTC
	}
	nowIn := now.In(loc)
	today := civilDay(nowIn)
	release := releaseAt(today, loc)

	// A zero rowDate means no row is held yet; civilDay keeps the comparison on
	// calendar dates rather than instants.
	if !rowDate.IsZero() && !civilDay(rowDate).Before(today) {
		// The row of the current day (or a newer one) is already in hand: the
		// next row can only come from a later release.
		return nextReleaseAfter(nowIn, loc)
	}
	if !isBusinessDay(today) {
		// Treasury publishes nothing today, so there is no release to wait for
		// and no row to expect — and therefore nothing to retry either.
		return nextReleaseAfter(nowIn, loc)
	}
	if nowIn.Before(release) {
		// Today's release has not happened yet.
		return release
	}
	// Released late, or not released today at all. Retry soon, but let the new
	// day reset the schedule instead of looping through the night.
	if retry := nowIn.Add(publicationRetryInterval); !retry.After(nextMidnight(today, loc)) {
		return retry
	}
	return nextMidnight(today, loc)
}

// nextReleaseAfter returns the first business-day release strictly after the
// given instant: the next Monday-to-Friday publicationHour in loc. Treasury
// publishes on business days only, so a Friday evening schedules Monday and a
// weekend day schedules the Monday after it.
func nextReleaseAfter(after time.Time, loc *time.Location) time.Time {
	day := civilDay(after)
	if isBusinessDay(day) {
		if release := releaseAt(day, loc); release.After(after) {
			return release
		}
	}
	// The next business day, which is at most three days away.
	day = day.AddDate(0, 0, 1)
	for !isBusinessDay(day) {
		day = day.AddDate(0, 0, 1)
	}
	return releaseAt(day, loc)
}

// isBusinessDay reports whether the calendar date falls Monday to Friday. It
// deliberately knows nothing about holidays: see NextCheckAt.
func isBusinessDay(day time.Time) bool {
	switch day.Weekday() {
	case time.Saturday, time.Sunday:
		return false
	default:
		return true
	}
}

// releaseAt returns the publication instant of a civil date.
func releaseAt(day time.Time, loc *time.Location) time.Time {
	return time.Date(day.Year(), day.Month(), day.Day(), publicationHour, 0, 0, 0, loc)
}

// nextMidnight returns the local midnight that closes a civil date.
func nextMidnight(day time.Time, loc *time.Location) time.Time {
	return time.Date(day.Year(), day.Month(), day.Day()+1, 0, 0, 0, 0, loc)
}

// civilDay returns the calendar date of t as a UTC-anchored midnight, so two
// instants can be compared by their calendar date alone, whoever built them and
// in whatever zone.
func civilDay(t time.Time) time.Time {
	year, month, day := t.Date()
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}
