package fed

import (
	"context"
	"sync"
	"time"
)

const (
	// failureRetryInterval is how long the cache waits after a failed fetch
	// before looking at the source again. It is deliberately short: a transient
	// failure should not hide the rate for a whole calendar day.
	failureRetryInterval = 5 * time.Minute
	// failureRetryCeiling caps the failure backoff, so an outage costs at most
	// two requests per hour however long it lasts.
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

// Cache wraps a rate fetcher so the dashboard hits the network at most once per
// calendar day instead of on every refresh tick.
//
// Unlike the Treasury curve, the reference rate has no publication calendar to
// follow: the FOMC changes the target range at a scheduled meeting, but the
// dashboard cannot know that schedule and must not try to. The accepted limit of
// this cache is that it refreshes on the local calendar day alone, so a session
// running across a weekend makes one request and keeps showing the same values
// until FRED publishes a change.
type Cache struct {
	// Now returns the current time. It defaults to time.Now and exists so the
	// calendar-day schedule can be exercised with an injected clock.
	Now func() time.Time

	fetch func(context.Context) (Rate, error)

	mu sync.Mutex
	// rate is the last successfully fetched rate, served on a cache hit.
	rate Rate
	// fetchOK reports whether the last fetch succeeded. A failed fetch leaves
	// rate in place but reports the error instead of the rate, so the
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

// NewDailyCache returns a cache around fetch that refreshes at most once per
// calendar day.
func NewDailyCache(fetch func(context.Context) (Rate, error)) *Cache {
	return &Cache{Now: time.Now, fetch: fetch}
}

// Get returns the rate, fetching only when the calendar day allows it. force
// bypasses both the day boundary and the retry window, which is what the
// dashboard's `r` key asks for.
//
// A successful fetch is cached and the next check is the next local midnight,
// after which a new value may exist. A failed fetch returns the error, schedules
// a short retry, and keeps the last good rate in place — but returns the error,
// never the rate, so the caller never mistakes a failure for fresh data.
//
// Get is intended to be called by one caller at a time: the dashboard holds a
// single in-flight fetch. The mutex keeps the cached state consistent for any
// other reader, and the concurrent-reader test proves a caller never observes a
// partially built rate.
func (c *Cache) Get(ctx context.Context, force bool) (Rate, error) {
	now := c.now()

	if !force {
		c.mu.Lock()
		fresh := c.fetchOK && now.Before(c.nextCheck)
		cached := c.rate
		retrying := !c.fetchOK && c.lastErr != nil && now.Before(c.nextCheck)
		lastErr := c.lastErr
		c.mu.Unlock()

		switch {
		case fresh:
			return cached, nil
		case retrying:
			// The failure is still inside its retry window: report it again
			// instead of hammering a source that just failed.
			return Rate{}, lastErr
		}
	}

	rate, err := c.fetch(ctx)

	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil {
		c.fetchOK = false
		c.lastErr = err
		c.failures++
		c.nextCheck = now.Add(failureBackoff(c.failures))
		return Rate{}, err
	}
	c.rate = rate
	c.fetchOK = true
	c.lastErr = nil
	c.failures = 0
	c.nextCheck = nextLocalMidnight(now)
	return rate, nil
}

func (c *Cache) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// nextLocalMidnight returns the midnight that opens the calendar day after the
// day now falls on, in now's own location. A hit is therefore valid for the rest
// of the calendar day the fetch happened on, whatever the host timezone and
// whatever the injected clock.
func nextLocalMidnight(now time.Time) time.Time {
	year, month, day := now.Date()
	return time.Date(year, month, day+1, 0, 0, 0, 0, now.Location())
}
