// Package ipc is a minimal client for Argentina's national consumer price
// index (IPC, INDEC), read from the open data series API of datos.gob.ar:
// GET https://apis.datos.gob.ar/series/api/series/?ids=148.3_INIVELNAL_DICI_M_26&limit=14&sort=desc&format=json
//
// The series is the monthly index level (national general level, base
// December 2016 = 100), not a variation, so the three figures the dashboard
// shows are derived locally from levels:
//
//   - monthly: L[m] / L[m-1] - 1
//   - 12 months: L[m] / L[m-12] - 1
//   - year to date: L[m] / L[December of the previous year] - 1
//
// Fourteen rows sorted newest first cover month m back to m-13, which is
// enough for all three bases (the December base is m-1 in January and m-12 in
// December). The rows must be consecutive calendar months: a gap would silently
// pick the wrong base, so it is reported as ErrMalformed instead.
package ipc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"time"
)

const (
	defaultBaseURL = "https://apis.datos.gob.ar"
	seriesPath     = "/series/api/series/"
	seriesID       = "148.3_INIVELNAL_DICI_M_26"
	// rowLimit is how many monthly rows are requested: the newest month plus
	// the 13 before it.
	rowLimit       = 14
	defaultTimeout = 15 * time.Second

	// neededRows is how many consecutive rows from the newest are required:
	// months m down to m-12.
	neededRows = 13

	// maxBodyBytes bounds what is read from the network.
	maxBodyBytes = 1 << 20
)

// ErrMalformed reports a response body that is not the expected series, has
// unusable levels, or lacks consecutive monthly rows covering the last 12
// months.
var ErrMalformed = errors.New("ipc: malformed response payload")

// HTTPError reports a non-2xx response from the server.
type HTTPError struct {
	// StatusCode is the numeric status code returned by the server.
	StatusCode int
	// Status is the full status line, e.g. "503 Service Unavailable".
	Status string
}

// Error implements the error interface.
func (e *HTTPError) Error() string {
	return fmt.Sprintf("ipc: unexpected status %s", e.Status)
}

// Indicator is the latest published IPC with its three derived variations.
type Indicator struct {
	// Month is the first day of the latest published month, at midnight in the
	// system's local timezone like the other sources.
	Month time.Time
	// Monthly is the percent change against the previous month, e.g. 1.7.
	Monthly float64
	// Year12 is the percent change against the same month of the previous year.
	Year12 float64
	// YTD is the percent change against December of the previous year.
	YTD float64
}

// Client fetches the indicator from apis.datos.gob.ar.
//
// A zero-value Client is usable and resolves the same defaults as NewClient.
type Client struct {
	// HTTPClient performs the HTTP requests. It defaults to an http.Client with
	// a 15-second timeout covering connection and body download.
	HTTPClient *http.Client
	// BaseURL is the server base URL. It defaults to https://apis.datos.gob.ar.
	BaseURL string
}

// NewClient returns a Client with the default timeout and base URL.
func NewClient() *Client {
	return &Client{
		HTTPClient: &http.Client{Timeout: defaultTimeout},
		BaseURL:    defaultBaseURL,
	}
}

// Fetch downloads the series and returns the latest indicator.
//
// Error classes:
//   - *HTTPError for non-2xx responses, carrying the status code.
//   - an error wrapping ErrMalformed when the body is not the expected series
//     or its rows are not 13 consecutive months with usable levels.
//   - a wrapped error for network, read, or context failures, detectable with
//     errors.Is against context.Canceled / context.DeadlineExceeded.
func (c *Client) Fetch(ctx context.Context) (Indicator, error) {
	body, err := c.get(ctx)
	if err != nil {
		return Indicator{}, err
	}
	return parseIndicator(body)
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

func (c *Client) get(ctx context.Context) ([]byte, error) {
	httpClient := c.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultTimeout}
	}
	endpoint, err := url.JoinPath(c.baseURL(), seriesPath)
	if err != nil {
		return nil, fmt.Errorf("ipc: build URL: %w", err)
	}
	query := url.Values{
		"ids":    {seriesID},
		"limit":  {fmt.Sprint(rowLimit)},
		"sort":   {"desc"},
		"format": {"json"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?"+query.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("ipc: build request: %w", err)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ipc: request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, &HTTPError{StatusCode: resp.StatusCode, Status: resp.Status}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return nil, fmt.Errorf("ipc: read response: %w", err)
	}
	return body, nil
}

// response is the series API envelope: each row is ["YYYY-MM-DD", level].
type response struct {
	Data [][]any `json:"data"`
}

// parseIndicator validates the rows and derives the three variations.
func parseIndicator(body []byte) (Indicator, error) {
	var resp response
	if err := json.Unmarshal(body, &resp); err != nil {
		return Indicator{}, fmt.Errorf("ipc: %w: %v", ErrMalformed, err)
	}
	if len(resp.Data) < neededRows {
		return Indicator{}, fmt.Errorf("ipc: %w: need %d rows, found %d", ErrMalformed, neededRows, len(resp.Data))
	}

	// levels[k] is the level k months before the newest row.
	levels := make([]float64, neededRows)
	var newest time.Time
	for k := 0; k < neededRows; k++ {
		row := resp.Data[k]
		if len(row) < 2 {
			return Indicator{}, fmt.Errorf("ipc: %w: row %d has %d fields", ErrMalformed, k, len(row))
		}
		text, ok := row[0].(string)
		if !ok {
			return Indicator{}, fmt.Errorf("ipc: %w: row %d date is not a string", ErrMalformed, k)
		}
		month, err := time.Parse("2006-01-02", text)
		if err != nil {
			return Indicator{}, fmt.Errorf("ipc: %w: row %d date: %v", ErrMalformed, k, err)
		}
		if k == 0 {
			newest = month
		} else if want := newest.AddDate(0, -k, 0); !month.Equal(want) {
			return Indicator{}, fmt.Errorf("ipc: %w: row %d is %s, want %s (gap or wrong order)",
				ErrMalformed, k, text, want.Format("2006-01-02"))
		}
		level, ok := row[1].(float64)
		if !ok || level <= 0 || math.IsNaN(level) || math.IsInf(level, 0) {
			return Indicator{}, fmt.Errorf("ipc: %w: row %d level %v is unusable", ErrMalformed, k, row[1])
		}
		levels[k] = level
	}

	// The previous December is as many months back as the newest month's
	// position in its year: 1 in January, 12 in December.
	decBack := int(newest.Month())
	pct := func(base float64) float64 { return (levels[0]/base - 1) * 100 }
	return Indicator{
		Month:   time.Date(newest.Year(), newest.Month(), 1, 0, 0, 0, 0, time.Local),
		Monthly: pct(levels[1]),
		Year12:  pct(levels[12]),
		YTD:     pct(levels[decBack]),
	}, nil
}
