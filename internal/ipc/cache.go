package ipc

import (
	"context"
	"sync"
	"time"
)

const (
	// failureRetryInterval is how long the cache waits after a failed fetch
	// before looking at the source again. It is deliberately short: a transient
	// failure should not hide the index for a whole calendar day.
	failureRetryInterval = 5 * time.Minute
	// failureRetryCeiling caps the failure backoff, so an outage costs at most
	// two requests per hour however long it lasts.
	failureRetryCeiling = 30 * time.Minute
)

// failureBackoff returns how long the cache waits before the next attempt after
// consecutiveFailures failures in a row: failureRetryInterval doubled per
// consecutive failure, capped at failureRetryCeiling.
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

// Cache wraps an indicator fetcher so the dashboard requests the series at
// most once per calendar day instead of on every refresh tick.
//
// INDEC publishes the CPI once a month at a time the dashboard cannot know, so
// this is the calendar-day pattern of itcrm.Cache. The accepted limit is that a
// new month appears after the next local midnight (or on the `r` key).
type Cache struct {
	// Now returns the current time. It defaults to time.Now and exists so the
	// calendar-day schedule can be exercised with an injected clock.
	Now func() time.Time

	fetch func(context.Context) (Indicator, error)

	mu sync.Mutex
	// indicator is the last successfully fetched value, served on a cache hit.
	indicator Indicator
	// fetchOK reports whether the last fetch succeeded. A failed fetch reports
	// the error instead of the stale value, so the dashboard's own stale
	// rendering stays the single source of truth about what is on screen.
	fetchOK bool
	// lastErr is the failure reported while the retry window is open.
	lastErr error
	// failures counts consecutive failed fetches for the retry backoff.
	failures int
	// nextCheck is the instant the cache may look at the source again.
	nextCheck time.Time
}

// NewDailyCache returns a cache around fetch that refreshes at most once per
// calendar day.
func NewDailyCache(fetch func(context.Context) (Indicator, error)) *Cache {
	return &Cache{Now: time.Now, fetch: fetch}
}

// Get returns the indicator, fetching only when the calendar day allows it.
// force bypasses both the day boundary and the retry window, which is what the
// dashboard's `r` key asks for.
//
// A successful fetch is cached until the next local midnight. A failed fetch
// returns the error, schedules a short retry with backoff, and never returns
// the old value, so the caller never mistakes a failure for fresh data.
func (c *Cache) Get(ctx context.Context, force bool) (Indicator, error) {
	now := c.now()

	if !force {
		c.mu.Lock()
		fresh := c.fetchOK && now.Before(c.nextCheck)
		cached := c.indicator
		retrying := !c.fetchOK && c.lastErr != nil && now.Before(c.nextCheck)
		lastErr := c.lastErr
		c.mu.Unlock()

		switch {
		case fresh:
			return cached, nil
		case retrying:
			return Indicator{}, lastErr
		}
	}

	ind, err := c.fetch(ctx)

	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil {
		c.fetchOK = false
		c.lastErr = err
		c.failures++
		c.nextCheck = now.Add(failureBackoff(c.failures))
		return Indicator{}, err
	}
	c.indicator = ind
	c.fetchOK = true
	c.lastErr = nil
	c.failures = 0
	year, month, day := now.Date()
	c.nextCheck = time.Date(year, month, day+1, 0, 0, 0, 0, now.Location())
	return ind, nil
}

func (c *Cache) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}
