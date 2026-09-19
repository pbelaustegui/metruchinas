// Package riesgo is a minimal client for the Ámbito public country-risk
// endpoint.
//
// The endpoint GET https://mercados.ambito.com/riesgopais/variacion returns
// the latest Índice de Riesgo País figure. All values arrive as strings:
// "variacion" uses a comma as decimal separator plus a trailing "%", and
// "fecha" is DD-MM-YYYY. No authentication is required.
package riesgo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	defaultBaseURL = "https://mercados.ambito.com"
	variacionPath  = "/riesgopais/variacion"
	defaultTimeout = 10 * time.Second

	// fechaLayout is the layout the API uses for "fecha": DD-MM-YYYY.
	fechaLayout = "02-01-2006"
)

// ErrMalformed reports a response body that is not the expected JSON payload
// or that carries missing or unparseable fields.
var ErrMalformed = errors.New("riesgo: malformed response payload")

// HTTPError reports a non-2xx response from the API.
type HTTPError struct {
	// StatusCode is the numeric status code returned by the server.
	StatusCode int
	// Status is the full status line, e.g. "503 Service Unavailable".
	Status string
}

// Error implements the error interface.
func (e *HTTPError) Error() string {
	return fmt.Sprintf("riesgo: unexpected status %s", e.Status)
}

// Indicator is the latest country-risk figure published by the API.
type Indicator struct {
	// Value is the latest index value ("ultimo"), e.g. 515.
	Value float64
	// Variation is the daily variation in percentage points ("variacion"),
	// e.g. 0.98 for the raw "0,98%" and -1.25 for "-1,25%".
	Variation float64
	// VariationClass is the raw "class-variacion" string the API sends,
	// used for UI coloring: "up-red", "down-green", ...
	VariationClass string
	// Date is the publication date ("fecha"). It is parsed from the
	// DD-MM-YYYY layout and anchored to midnight in the system's local
	// timezone, so it agrees with the clock the dashboard displays.
	Date time.Time
}

// Client fetches the indicator from mercados.ambito.com.
//
// A zero-value Client is usable and resolves the same defaults as NewClient.
type Client struct {
	// HTTPClient performs the HTTP requests. It defaults to an http.Client
	// with a 10-second timeout that covers both connection (dial) and
	// response read.
	HTTPClient *http.Client
	// BaseURL is the API base URL. It defaults to
	// https://mercados.ambito.com.
	BaseURL string
}

// NewClient returns a Client with the default timeout and base URL.
func NewClient() *Client {
	return &Client{
		HTTPClient: &http.Client{Timeout: defaultTimeout},
		BaseURL:    defaultBaseURL,
	}
}

// Fetch returns the latest indicator.
//
// Error classes:
//   - *HTTPError for non-2xx responses, carrying the status code.
//   - an error wrapping ErrMalformed when the body is not the expected JSON
//     or carries missing or unparseable fields.
//   - a wrapped error for network, read, or context failures, detectable
//     with errors.Is against context.Canceled / context.DeadlineExceeded.
func (c *Client) Fetch(ctx context.Context) (Indicator, error) {
	body, err := c.get(ctx, variacionPath)
	if err != nil {
		return Indicator{}, err
	}
	indicator, err := parseIndicator(body)
	if err != nil {
		return Indicator{}, err
	}
	return indicator, nil
}

// Fetch returns the latest indicator using a default client.
// See Client.Fetch.
func Fetch(ctx context.Context) (Indicator, error) {
	return NewClient().Fetch(ctx)
}

func (c *Client) get(ctx context.Context, path string) ([]byte, error) {
	httpClient := c.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultTimeout}
	}
	base := c.BaseURL
	if base == "" {
		base = defaultBaseURL
	}
	endpoint, err := url.JoinPath(base, path)
	if err != nil {
		return nil, fmt.Errorf("riesgo: build URL: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("riesgo: build request: %w", err)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("riesgo: request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, &HTTPError{StatusCode: resp.StatusCode, Status: resp.Status}
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("riesgo: read response: %w", err)
	}
	return body, nil
}

// wireIndicator mirrors the raw API payload, where every value is a string.
type wireIndicator struct {
	Ultimo         string `json:"ultimo"`
	Fecha          string `json:"fecha"`
	Variacion      string `json:"variacion"`
	ClassVariacion string `json:"class-variacion"`
}

func parseIndicator(body []byte) (Indicator, error) {
	var wire wireIndicator
	if err := json.Unmarshal(body, &wire); err != nil {
		return Indicator{}, fmt.Errorf("riesgo: %w: %v", ErrMalformed, err)
	}
	if strings.TrimSpace(wire.ClassVariacion) == "" {
		return Indicator{}, fmt.Errorf("riesgo: %w: field %q is missing or empty", ErrMalformed, "class-variacion")
	}

	value, err := parseFloatField("ultimo", wire.Ultimo)
	if err != nil {
		return Indicator{}, err
	}
	variation, err := parseFloatField("variacion", strings.TrimSuffix(wire.Variacion, "%"))
	if err != nil {
		return Indicator{}, err
	}
	date, err := parseDate(wire.Fecha)
	if err != nil {
		return Indicator{}, err
	}
	return Indicator{
		Value:          value,
		Variation:      variation,
		VariationClass: strings.TrimSpace(wire.ClassVariacion),
		Date:           date,
	}, nil
}

// parseFloatField parses a percentage-style string value: surrounding
// whitespace is trimmed and a comma decimal separator is accepted as the API
// sends it ("0,98" -> 0.98). Missing or unparseable values are errors.
func parseFloatField(name, raw string) (float64, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return 0, fmt.Errorf("riesgo: %w: field %q is missing or empty", ErrMalformed, name)
	}
	normalized := strings.ReplaceAll(s, ",", ".")
	v, err := strconv.ParseFloat(normalized, 64)
	if err != nil {
		return 0, fmt.Errorf("riesgo: %w: invalid %s value %q", ErrMalformed, name, raw)
	}
	return v, nil
}

// parseDate parses the API date layout DD-MM-YYYY into midnight of that
// calendar date in the system's local timezone.
func parseDate(raw string) (time.Time, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return time.Time{}, fmt.Errorf("riesgo: %w: field %q is missing or empty", ErrMalformed, "fecha")
	}
	t, err := time.Parse(fechaLayout, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("riesgo: %w: invalid fecha value %q", ErrMalformed, raw)
	}
	// The payload carries a calendar date with no time and no zone. Re-anchor
	// it to local midnight instead of converting the instant: converting would
	// move a UTC-midnight value to the previous day west of Greenwich.
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.Local), nil
}
