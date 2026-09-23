// Package fed is a minimal client for the Federal Reserve's reference rate as
// published by FRED, the St. Louis Fed's data service.
//
// The endpoint
//
//	GET https://fred.stlouisfed.org/graph/fredgraph.csv?id=<SERIES>&cosd=<YYYY-MM-DD>
//
// returns the observations of one series as CSV: the header
// "observation_date,<SERIES>" and one row per day in ascending date order. No
// authentication is required and a bounded window is a couple of kilobytes.
//
// Three properties of the payload shape this package:
//
//   - cosd limits the lookback of a single-series request but is ignored when
//     several ids share one request. A multi-series request therefore returns
//     the whole history (~800 KB and growing every year), so the client issues
//     three single-series requests with cosd instead of one combined request.
//   - The series are forward-filled daily: weekends and holidays repeat the
//     last published value. An empty cell means the value is not published yet
//     (DFF lags the target range by a day or two), never that the value is zero,
//     so every series is read as its last non-empty row.
//   - The FOMC target range is published as two series, DFEDTARL (lower bound)
//     and DFEDTARU (upper bound), and the effective federal funds rate (EFFR)
//     as DFF. The policy rate is quoted as a range, so both bounds are needed.
package fed

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"slices"
)

const (
	defaultBaseURL = "https://fred.stlouisfed.org"
	// csvPath is the FRED graph CSV endpoint. One series per request.
	csvPath = "/graph/fredgraph.csv"
	// defaultTimeout bounds a single series request.
	defaultTimeout = 10 * time.Second
	// defaultLookbackDays is how far back the client asks for. It is always
	// longer than any plausible publication gap of the tracked series, so the
	// window never runs out before the newest observation.
	defaultLookbackDays = 30

	// targetLowSeries is the FOMC target range lower bound.
	targetLowSeries = "DFEDTARL"
	// targetHighSeries is the FOMC target range upper bound.
	targetHighSeries = "DFEDTARU"
	// effectiveSeries is the effective federal funds rate (EFFR), the volume
	// weighted median of overnight federal funds transactions.
	effectiveSeries = "DFF"

	// dateColumn is the name of the observation date column.
	dateColumn = "observation_date"
	// csvDateLayout is the layout FRED uses in the observation date column.
	csvDateLayout = "2006-01-02"
	// bpPerPercent converts a rate change in percentage points to basis points.
	bpPerPercent = 100
	// byteOrderMark is stripped from the first header cell: a BOM is invisible
	// in a terminal but would hide the date column from the name lookup.
	byteOrderMark = "\ufeff"
)

// ErrMalformed reports a response body that is not the expected CSV payload or
// that carries an unparseable date or value.
var ErrMalformed = errors.New("fed: malformed response payload")

// ErrNoData reports a well-formed response that publishes no value in the
// requested window, or an empty body. It is distinct from ErrMalformed: a
// series with an unpublished window is not a payload the parser failed to
// understand.
var ErrNoData = errors.New("fed: no published data in the requested window")

// HTTPError reports a non-2xx response from the endpoint.
type HTTPError struct {
	// StatusCode is the numeric status code returned by the server.
	StatusCode int
	// Status is the full status line, e.g. "503 Service Unavailable".
	Status string
}

// Error implements the error interface.
func (e *HTTPError) Error() string {
	return fmt.Sprintf("fed: unexpected status %s", e.Status)
}

// Rate is the Federal Reserve reference rate: the FOMC target range and the
// effective federal funds rate.
type Rate struct {
	// Date is the observation date of the newest non-empty DFF row, anchored to
	// midnight in the system's local timezone and never converted, so the
	// rendered date is the same whatever timezone the host runs in. It can lag
	// the policy rate by a day or two, because EFFR is published later.
	Date time.Time
	// TargetLow is the newest non-empty DFEDTARL value in percent, e.g. 3.75.
	TargetLow float64
	// TargetHigh is the newest non-empty DFEDTARU value in percent, e.g. 4.00.
	TargetHigh float64
	// Effective is the newest non-empty DFF value in percent, e.g. 3.88.
	Effective float64
	// EffectiveDeltaBp is the EFFR change against the previous non-empty DFF
	// row in basis points. It is nil when fewer than two non-empty rows are
	// published, so an unknown change is never rendered as zero.
	EffectiveDeltaBp *float64
}

// Client fetches the reference rate from FRED.
//
// A zero-value Client is usable and resolves the same defaults as NewClient.
type Client struct {
	// HTTPClient performs the HTTP requests. It defaults to an http.Client
	// with a 10-second timeout that covers both connection (dial) and
	// response read.
	HTTPClient *http.Client
	// BaseURL is the endpoint base URL. It defaults to
	// https://fred.stlouisfed.org.
	BaseURL string
	// Now returns the current time. It defaults to time.Now and is used only to
	// choose the start of the requested window; it never affects parsing or
	// display.
	Now func() time.Time
	// LookbackDays is how many days before Now the requested window starts. It
	// defaults to defaultLookbackDays and exists so the window can be exercised
	// with a deterministic clock.
	LookbackDays int
}

// NewClient returns a Client with the default timeout, base URL, clock and
// lookback.
func NewClient() *Client {
	return &Client{
		HTTPClient:   &http.Client{Timeout: defaultTimeout},
		BaseURL:      defaultBaseURL,
		Now:          time.Now,
		LookbackDays: defaultLookbackDays,
	}
}

// Fetch returns the current reference rate.
//
// It issues three single-series requests — DFEDTARL, DFEDTARU and DFF — because
// cosd bounds one series but is ignored when several ids share one request. The
// reported rate is the last non-empty row of each series: an empty cell is a
// value FRED has not published yet, so it must never be read as zero.
//
// Error classes:
//   - *HTTPError for non-2xx responses, carrying the status code.
//   - an error wrapping ErrMalformed when a body is not the expected CSV or
//     carries an unparseable date or value.
//   - an error wrapping ErrNoData when a body is empty, a header has no rows, or
//     a required series publishes no non-empty row in the window.
//   - a wrapped error for network, read, or context failures, detectable with
//     errors.Is against context.Canceled / context.DeadlineExceeded.
func (c *Client) Fetch(ctx context.Context) (Rate, error) {
	start := c.now().AddDate(0, 0, -c.lookbackDays())

	low, err := c.fetchSeries(ctx, targetLowSeries, start)
	if err != nil {
		return Rate{}, err
	}
	high, err := c.fetchSeries(ctx, targetHighSeries, start)
	if err != nil {
		return Rate{}, err
	}
	effective, err := c.fetchSeries(ctx, effectiveSeries, start)
	if err != nil {
		return Rate{}, err
	}

	newest := effective[len(effective)-1]
	rate := Rate{
		Date:       localCivilDate(newest.date),
		TargetLow:  low[len(low)-1].value,
		TargetHigh: high[len(high)-1].value,
		Effective:  newest.value,
	}
	if len(effective) > 1 {
		delta := (newest.value - effective[len(effective)-2].value) * bpPerPercent
		rate.EffectiveDeltaBp = &delta
	}
	return rate, nil
}

func (c *Client) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *Client) lookbackDays() int {
	if c.LookbackDays <= 0 {
		return defaultLookbackDays
	}
	return c.LookbackDays
}

// fetchSeries requests and parses one series.
func (c *Client) fetchSeries(ctx context.Context, series string, start time.Time) ([]observation, error) {
	body, err := c.get(ctx, series, start)
	if err != nil {
		return nil, err
	}
	return parseSeries(body, series)
}

func (c *Client) get(ctx context.Context, series string, start time.Time) ([]byte, error) {
	httpClient := c.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultTimeout}
	}
	base := c.BaseURL
	if base == "" {
		base = defaultBaseURL
	}
	endpoint, err := url.JoinPath(base, csvPath)
	if err != nil {
		return nil, fmt.Errorf("fed: build URL: %w", err)
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("fed: build URL: %w", err)
	}
	query := url.Values{}
	query.Set("id", series)
	query.Set("cosd", start.Format(csvDateLayout))
	parsed.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("fed: build request: %w", err)
	}
	// Unlike home.treasury.gov, FRED does not want a browser user agent. Its
	// edge answers a browser agent from a Go client with an HTTP/2 "stream
	// error ... INTERNAL_ERROR", while a non-browser agent is served the CSV
	// (reprobed 2026-09-23). The client therefore leaves the transport's default
	// user agent in place and sends no browser-shaped headers.
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fed: request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, &HTTPError{StatusCode: resp.StatusCode, Status: resp.Status}
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("fed: read response: %w", err)
	}
	return body, nil
}

// observation is one published row of a series.
type observation struct {
	date  time.Time
	value float64
}

// parseSeries parses one single-series payload into its non-empty observations
// in ascending date order.
//
// The date column is resolved by its published name, never by position. Only
// rows carrying a value are returned: an empty value cell means FRED has not
// published that day's observation, and treating it as zero would invent a rate
// the source never published.
func parseSeries(body []byte, series string) ([]observation, error) {
	reader := csv.NewReader(strings.NewReader(string(body)))
	header, err := reader.Read()
	if err == io.EOF {
		// An empty body is a bounded window with nothing in it, not a payload
		// the parser failed to understand.
		return nil, fmt.Errorf("%w: empty response body", ErrNoData)
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformed, err)
	}

	dateIdx, valueIdx := -1, -1
	for i, name := range header {
		name = strings.TrimSpace(name)
		if i == 0 {
			name = strings.TrimPrefix(name, byteOrderMark)
		}
		switch {
		case name == dateColumn:
			dateIdx = i
		case name == series && valueIdx < 0:
			valueIdx = i
		}
	}
	if dateIdx < 0 {
		return nil, fmt.Errorf("%w: no %q column in header %s", ErrMalformed, dateColumn, headerSummary(header))
	}
	if valueIdx < 0 {
		// A single-series payload has exactly one value column, named after the
		// requested series. A header that spells it differently still carries a
		// single series, so the only other cell is the value.
		if len(header) == 2 {
			valueIdx = 1 - dateIdx
		}
	}
	if valueIdx < 0 || valueIdx == dateIdx {
		return nil, fmt.Errorf("%w: no value column for series %s in header %s", ErrMalformed, series, headerSummary(header))
	}

	var observations []observation
	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrMalformed, err)
		}
		obs, ok, err := parseRecord(record, dateIdx, valueIdx)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		observations = append(observations, obs)
	}
	if len(observations) == 0 {
		return nil, fmt.Errorf("%w: series %s publishes no value in the requested window", ErrNoData, series)
	}
	slices.SortStableFunc(observations, func(a, b observation) int { return a.date.Compare(b.date) })
	return observations, nil
}

// parseRecord converts one CSV record into an observation. The second result is
// false when the record carries no value, which is a missing observation rather
// than an error.
func parseRecord(record []string, dateIdx, valueIdx int) (observation, bool, error) {
	if dateIdx >= len(record) {
		return observation{}, false, fmt.Errorf("%w: record %q has no date cell", ErrMalformed, strings.Join(record, ","))
	}
	rawDate := strings.TrimSpace(record[dateIdx])
	if rawDate == "" {
		return observation{}, false, fmt.Errorf("%w: empty date cell in record %q", ErrMalformed, strings.Join(record, ","))
	}
	date, err := time.Parse(csvDateLayout, rawDate)
	if err != nil {
		return observation{}, false, fmt.Errorf("%w: invalid date %q", ErrMalformed, rawDate)
	}
	if valueIdx >= len(record) {
		return observation{}, false, nil
	}
	cell := strings.TrimSpace(record[valueIdx])
	if cell == "" {
		// An empty cell is a value FRED has not published yet, never a zero.
		return observation{}, false, nil
	}
	value, err := strconv.ParseFloat(cell, 64)
	if err != nil {
		return observation{}, false, fmt.Errorf("%w: invalid value %q for %s", ErrMalformed, cell, date.Format(csvDateLayout))
	}
	return observation{date: date, value: value}, true, nil
}

// headerSummary renders a bounded summary of a CSV header for error messages.
// The parsed body may be anything at all, so a wide or hostile header must not
// produce an unbounded diagnosis.
func headerSummary(header []string) string {
	const (
		maxCells    = 8
		maxCellRune = 24
	)
	shown := header
	if len(shown) > maxCells {
		shown = shown[:maxCells]
	}
	cells := make([]string, 0, len(shown))
	for _, cell := range shown {
		cells = append(cells, strconv.Quote(clipRunes(cell, maxCellRune)))
	}
	summary := strings.Join(cells, ", ")
	if len(header) > maxCells {
		summary += fmt.Sprintf(", … (%d columns)", len(header))
	}
	return summary
}

// clipRunes truncates s to at most max runes, marking the cut.
func clipRunes(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "…"
}

// localCivilDate re-anchors a parsed observation date to midnight in the
// system's local timezone. The payload carries a calendar date with no time and
// no zone; converting the instant instead of re-anchoring would move a
// UTC-midnight value to the previous day west of Greenwich.
func localCivilDate(t time.Time) time.Time {
	year, month, day := t.Date()
	return time.Date(year, month, day, 0, 0, 0, 0, time.Local)
}
