// Package uscpi is a minimal client for the US consumer price index (CPI-U,
// all items, U.S. city average, BLS) as published by FRED, the St. Louis Fed's
// data service:
//
//	GET https://fred.stlouisfed.org/graph/fredgraph.csv?id=<SERIES>&cosd=<YYYY-MM-DD>
//
// The endpoint is keyless and returns one series as CSV: the header
// "observation_date,<SERIES>" and one row per month, in ascending date order.
// The series are index levels (1982-84 = 100), not variations, so the three
// figures the dashboard shows are derived locally. Two series are needed, which
// mirrors the BLS headline convention:
//
//   - CPIAUCSL (seasonally adjusted) for the monthly variation:
//     SA[m] / SA[m-1] - 1
//   - CPIAUCNS (not seasonally adjusted) for the annual variations:
//     12 months: NS[m] / NS[m-12] - 1
//     year to date: NS[m] / NS[December of the previous year] - 1
//
// Each series is a separate single-series request: cosd limits the lookback of
// one series but is ignored when several ids share a request. Fourteen months
// of lookback cover month m back to m-12 (and the December base, which is m-1
// in January and m-12 in December). FRED can list a month with an empty value
// before it is published, or publish one series a day before the other, so the
// reported month is the latest one present in both series. Levels are looked up
// by calendar month, never by row position, and every base month must exist
// (SA m and m-1; NSA m, m-12 and the previous December): a missing base is
// reported as ErrMalformed instead of silently picking a neighbour. A month
// missing between the bases is irrelevant and tolerated: the BLS did not publish
// October 2025 (US federal shutdown), and FRED lists it with an empty value, so
// requiring 13 consecutive months would break the indicator for a year.
package uscpi

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	defaultBaseURL = "https://fred.stlouisfed.org"
	// csvPath is the FRED graph CSV endpoint. One series per request.
	csvPath        = "/graph/fredgraph.csv"
	defaultTimeout = 15 * time.Second

	// seasonallyAdjustedSeries is CPI-U, all items, seasonally adjusted.
	seasonallyAdjustedSeries = "CPIAUCSL"
	// notAdjustedSeries is CPI-U, all items, not seasonally adjusted.
	notAdjustedSeries = "CPIAUCNS"

	// lookbackMonths is how many months before the current one the request
	// window starts, so 13 consecutive published months fit even when the
	// newest month is not published yet.
	lookbackMonths = 14
	// dateColumn is the name of the observation date column.
	dateColumn = "observation_date"
	// csvDateLayout is the layout FRED uses in the observation date column.
	csvDateLayout = "2006-01-02"
	// byteOrderMark is stripped from the first header cell so it cannot hide
	// the date column from the name lookup.
	byteOrderMark = "\ufeff"
	// maxBodyBytes bounds what is read from the network.
	maxBodyBytes = 1 << 20
)

// ErrMalformed reports a response body that is not the expected CSV series, has
// unusable levels, or lacks consecutive monthly rows covering the last 12
// months.
var ErrMalformed = errors.New("uscpi: malformed response payload")

// HTTPError reports a non-2xx response from the server.
type HTTPError struct {
	// StatusCode is the numeric status code returned by the server.
	StatusCode int
	// Status is the full status line, e.g. "503 Service Unavailable".
	Status string
}

// Error implements the error interface.
func (e *HTTPError) Error() string {
	return fmt.Sprintf("uscpi: unexpected status %s", e.Status)
}

// Indicator is the latest published US CPI with its three derived variations.
type Indicator struct {
	// Month is the first day of the latest month present in both series, at
	// midnight in the system's local timezone like the other sources.
	Month time.Time
	// Monthly is the seasonally adjusted percent change against the previous
	// month, e.g. 0.4.
	Monthly float64
	// Year12 is the not-adjusted percent change against the same month of the
	// previous year.
	Year12 float64
	// YTD is the not-adjusted percent change against December of the previous
	// year.
	YTD float64
}

// Client fetches the indicator from FRED.
//
// A zero-value Client is usable and resolves the same defaults as NewClient.
type Client struct {
	// HTTPClient performs the HTTP requests. It defaults to an http.Client with
	// a 15-second timeout covering connection and body download.
	HTTPClient *http.Client
	// BaseURL is the server base URL. It defaults to https://fred.stlouisfed.org.
	BaseURL string
	// Now returns the current time. It defaults to time.Now and is used only to
	// choose the start of the requested window.
	Now func() time.Time
}

// NewClient returns a Client with the default timeout, base URL and clock.
func NewClient() *Client {
	return &Client{
		HTTPClient: &http.Client{Timeout: defaultTimeout},
		BaseURL:    defaultBaseURL,
		Now:        time.Now,
	}
}

// Fetch downloads both series and returns the latest indicator. If either
// request fails the whole fetch fails: half an indicator is never reported.
//
// Error classes:
//   - *HTTPError for non-2xx responses, carrying the status code.
//   - an error wrapping ErrMalformed when a body is not the expected series or
//     its rows are not consecutive months with usable levels.
//   - a wrapped error for network, read, or context failures, detectable with
//     errors.Is against context.Canceled / context.DeadlineExceeded.
func (c *Client) Fetch(ctx context.Context) (Indicator, error) {
	now := c.now()
	start := time.Date(now.Year(), now.Month()-lookbackMonths, 1, 0, 0, 0, 0, time.UTC)

	sa, err := c.get(ctx, seasonallyAdjustedSeries, start)
	if err != nil {
		return Indicator{}, err
	}
	nsa, err := c.get(ctx, notAdjustedSeries, start)
	if err != nil {
		return Indicator{}, err
	}
	return parseIndicator(sa, nsa)
}

// Fetch returns the latest indicator using a default client. See Client.Fetch.
func Fetch(ctx context.Context) (Indicator, error) {
	return NewClient().Fetch(ctx)
}

func (c *Client) baseURL() string {
	if c.BaseURL == "" {
		return defaultBaseURL
	}
	return c.BaseURL
}

func (c *Client) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *Client) get(ctx context.Context, series string, start time.Time) ([]byte, error) {
	httpClient := c.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultTimeout}
	}
	endpoint, err := url.JoinPath(c.baseURL(), csvPath)
	if err != nil {
		return nil, fmt.Errorf("uscpi: build URL: %w", err)
	}
	query := url.Values{
		"id":   {series},
		"cosd": {start.Format(csvDateLayout)},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?"+query.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("uscpi: build request: %w", err)
	}
	// FRED's edge rejects browser-shaped user agents from a Go client, so the
	// transport's default is left in place (see internal/fed).
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("uscpi: request %s: %w", series, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, &HTTPError{StatusCode: resp.StatusCode, Status: resp.Status}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return nil, fmt.Errorf("uscpi: read %s: %w", series, err)
	}
	return body, nil
}

// monthKey identifies a calendar month as a single integer, so consecutive
// months differ by exactly one.
type monthKey int

func keyOf(t time.Time) monthKey { return monthKey(t.Year()*12 + int(t.Month()) - 1) }

func (k monthKey) time() time.Time {
	return time.Date(int(k)/12, time.Month(int(k)%12+1), 1, 0, 0, 0, 0, time.Local)
}

// parseIndicator validates both series and derives the three variations.
func parseIndicator(saBody, nsaBody []byte) (Indicator, error) {
	sa, err := parseSeries(saBody, seasonallyAdjustedSeries)
	if err != nil {
		return Indicator{}, err
	}
	nsa, err := parseSeries(nsaBody, notAdjustedSeries)
	if err != nil {
		return Indicator{}, err
	}

	// The reported month is the latest one both series publish.
	var newest monthKey
	found := false
	for k := range nsa {
		if _, ok := sa[k]; ok && (!found || k > newest) {
			newest, found = k, true
		}
	}
	if !found {
		return Indicator{}, fmt.Errorf("uscpi: %w: the series share no month", ErrMalformed)
	}

	// back returns the level n months before the newest month, or an error when
	// that month is missing (a gap would silently pick the wrong base).
	back := func(series map[monthKey]float64, name string, n int) (float64, error) {
		level, ok := series[newest-monthKey(n)]
		if !ok {
			return 0, fmt.Errorf("uscpi: %w: %s has no %s (gap or short window)",
				ErrMalformed, name, (newest - monthKey(n)).time().Format("2006-01"))
		}
		return level, nil
	}
	// The previous December is as many months back as the newest month's
	// position in its year: 1 in January, 12 in December.
	decBack := int(newest.time().Month())
	level := func(series map[monthKey]float64, name string, n int) float64 {
		if err != nil {
			return 0
		}
		var v float64
		v, err = back(series, name, n)
		return v
	}
	nsaNow := level(nsa, notAdjustedSeries, 0)
	nsaYear := level(nsa, notAdjustedSeries, 12)
	nsaDec := level(nsa, notAdjustedSeries, decBack)
	saNow := level(sa, seasonallyAdjustedSeries, 0)
	saPrev := level(sa, seasonallyAdjustedSeries, 1)
	if err != nil {
		return Indicator{}, err
	}

	pct := func(level, base float64) float64 { return (level/base - 1) * 100 }
	return Indicator{
		Month:   newest.time(),
		Monthly: pct(saNow, saPrev),
		Year12:  pct(nsaNow, nsaYear),
		YTD:     pct(nsaNow, nsaDec),
	}, nil
}

// parseSeries parses one single-series payload into its published levels by
// month. The date column is resolved by its published name. A row with an empty
// value cell is a month FRED has not published yet, never a zero, so it is
// skipped.
func parseSeries(body []byte, series string) (map[monthKey]float64, error) {
	reader := csv.NewReader(strings.NewReader(string(body)))
	header, err := reader.Read()
	if err != nil {
		return nil, fmt.Errorf("uscpi: %w: %s header: %v", ErrMalformed, series, err)
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
	if valueIdx < 0 && len(header) == 2 && dateIdx >= 0 {
		valueIdx = 1 - dateIdx
	}
	if dateIdx < 0 || valueIdx < 0 {
		return nil, fmt.Errorf("uscpi: %w: %s header lacks %q or the series column", ErrMalformed, series, dateColumn)
	}

	levels := make(map[monthKey]float64)
	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("uscpi: %w: %s: %v", ErrMalformed, series, err)
		}
		if dateIdx >= len(record) {
			return nil, fmt.Errorf("uscpi: %w: %s record has no date cell", ErrMalformed, series)
		}
		rawDate := strings.TrimSpace(record[dateIdx])
		date, err := time.Parse(csvDateLayout, rawDate)
		if err != nil {
			return nil, fmt.Errorf("uscpi: %w: %s invalid date %q", ErrMalformed, series, rawDate)
		}
		if valueIdx >= len(record) || strings.TrimSpace(record[valueIdx]) == "" {
			continue
		}
		cell := strings.TrimSpace(record[valueIdx])
		level, err := strconv.ParseFloat(cell, 64)
		if err != nil || level <= 0 || math.IsNaN(level) || math.IsInf(level, 0) {
			return nil, fmt.Errorf("uscpi: %w: %s unusable level %q for %s", ErrMalformed, series, cell, rawDate)
		}
		key := keyOf(date)
		if _, dup := levels[key]; dup {
			return nil, fmt.Errorf("uscpi: %w: %s lists %s twice", ErrMalformed, series, date.Format("2006-01"))
		}
		levels[key] = level
	}
	return levels, nil
}
