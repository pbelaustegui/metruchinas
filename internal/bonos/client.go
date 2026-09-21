// Package bonos is a minimal client for the compararfondos.com.ar public bond
// quotes API.
//
// The endpoint GET https://compararfondos.com.ar/api/bonos returns every
// tracked bond (300+) in a single JSON payload, including the six GD sovereign
// bonds under their dollar-conversion tickers (GD29D, GD30D, GD35D, GD38D,
// GD41D, GD46D). Prices are JSON numbers quoted in USD. No authentication is
// required. The dashboard displays the USD price directly and derives parity
// locally; the old mercados.ambito.com source was retired because it froze
// (its quotes dated back to May 2024 as of September 2026).
package bonos

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

const (
	defaultBaseURL = "https://compararfondos.com.ar"
	bonosPath      = "/api/bonos"
	defaultTimeout = 10 * time.Second
	browserUA      = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/137.0.0.0 Safari/537.36"
)

// ErrMalformed reports a response body that is not the expected JSON payload
// or that carries missing or unparseable fields.
var ErrMalformed = errors.New("bonos: malformed response payload")

// HTTPError reports a non-2xx response from the API.
type HTTPError struct {
	// StatusCode is the numeric status code returned by the server.
	StatusCode int
	// Status is the full status line, e.g. "503 Service Unavailable".
	Status string
}

// Error implements the error interface.
func (e *HTTPError) Error() string {
	return fmt.Sprintf("bonos: unexpected status %s", e.Status)
}

// BondQuote is a single bond quote for a given ticker.
// If Err is non-nil, it indicates a per-bond error (e.g. the ticker was absent
// from the payload); the other fields are invalid.
type BondQuote struct {
	// Ticker is the bond ticker (e.g. "GD30D").
	Ticker string
	// Ultimo is the last price in USD as published by the source.
	Ultimo float64
	// Variacion is the daily variation as a percentage (e.g. -0.06 for -0.06%).
	Variacion float64
	// Err is non-nil if the quote for this specific ticker failed; nil means
	// Ultimo and Variacion are valid.
	Err error
}

// Client fetches bond quotes from compararfondos.com.ar.
//
// A zero-value Client is usable and resolves the same defaults as NewClient.
type Client struct {
	// HTTPClient performs the HTTP requests. It defaults to an http.Client
	// with a 10-second timeout that covers both connection (dial) and
	// response read.
	HTTPClient *http.Client
	// BaseURL is the API base URL. It defaults to
	// https://compararfondos.com.ar.
	BaseURL string
}

// NewClient returns a Client with the default timeout and base URL.
func NewClient() *Client {
	return &Client{
		HTTPClient: &http.Client{Timeout: defaultTimeout},
		BaseURL:    defaultBaseURL,
	}
}

// Fetch returns bond quotes for each ticker provided.
//
// The source payload contains every tracked bond, so a single HTTP request is
// made regardless of how many tickers are requested. Results are returned in
// the same order as the input tickers, each echoing its requested ticker.
//
// Ticker resolution: an input is first matched verbatim against the payload
// tickers; if absent, the dollar-conversion variant (input + "D", the MEP
// board quote published in USD) is tried. "GD30" therefore resolves to the
// source's "GD30D" entry.
//
// Top-level errors are returned only for:
//   - no tickers provided
//   - transport failures (network, timeout, context cancellation)
//   - a non-2xx response or unparseable payload (every ticker then carries the
//     same error and the first one is returned as the top-level error)
//
// Error classes:
//   - An error containing "no tickers" when no tickers are provided.
//   - Top-level wrapped error for context cancellation or transport failure.
//   - Per-bond *HTTPError for non-2xx responses on the single request.
//   - Per-bond error wrapping ErrMalformed when the payload is invalid or the
//     ticker is missing / has no price.
func (c *Client) Fetch(ctx context.Context, tickers ...string) ([]BondQuote, error) {
	if len(tickers) == 0 {
		return nil, fmt.Errorf("bonos: no tickers provided")
	}

	body, err := c.get(ctx)
	if err != nil {
		// Transport, HTTP status, or unreadable body: fail every ticker and
		// surface the first error as the top-level error.
		quotes := make([]BondQuote, len(tickers))
		for i, ticker := range tickers {
			quotes[i] = BondQuote{Ticker: ticker, Err: err}
		}
		return quotes, err
	}

	byTicker, err := parsePayload(body)
	if err != nil {
		quotes := make([]BondQuote, len(tickers))
		for i, ticker := range tickers {
			quotes[i] = BondQuote{Ticker: ticker, Err: err}
		}
		return quotes, err
	}

	quotes := make([]BondQuote, len(tickers))
	failureCount := 0
	for i, ticker := range tickers {
		wire, ok := lookup(byTicker, ticker)
		if !ok || wire.Precio == nil {
			quotes[i] = BondQuote{Ticker: ticker, Err: fmt.Errorf("bonos: %w: ticker %q missing from payload or has no price", ErrMalformed, ticker)}
			failureCount++
			continue
		}
		// pctChange is optional in the payload; a missing value means the
		// source published no daily variation, which renders as a flat 0.
		variacion := 0.0
		if wire.PctChange != nil {
			variacion = *wire.PctChange
		}
		quotes[i] = BondQuote{Ticker: ticker, Ultimo: *wire.Precio, Variacion: variacion}
	}

	// If all requested tickers failed, surface the first error top-level.
	if failureCount == len(tickers) {
		for _, q := range quotes {
			if q.Err != nil {
				return quotes, q.Err
			}
		}
	}

	return quotes, nil
}

// Fetch returns bond quotes for each ticker using a default client.
// See Client.Fetch.
func Fetch(ctx context.Context, tickers ...string) ([]BondQuote, error) {
	return NewClient().Fetch(ctx, tickers...)
}

func (c *Client) get(ctx context.Context) ([]byte, error) {
	httpClient := c.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultTimeout}
	}
	base := c.BaseURL
	if base == "" {
		base = defaultBaseURL
	}
	endpoint, err := url.JoinPath(base, bonosPath)
	if err != nil {
		return nil, fmt.Errorf("bonos: build URL: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("bonos: build request: %w", err)
	}
	req.Header.Set("User-Agent", browserUA)
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("bonos: request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, &HTTPError{StatusCode: resp.StatusCode, Status: resp.Status}
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("bonos: read response: %w", err)
	}
	return body, nil
}

// wireBond mirrors one entry of the source payload. The API also publishes
// many other fields (tir, duration, paridad, flujos, ...); only the ones the
// dashboard needs are modelled here.
type wireBond struct {
	Ticker    string   `json:"ticker"`
	Precio    *float64 `json:"precio"`
	PctChange *float64 `json:"pctChange"`
}

// parsePayload decodes the full bonds payload and indexes it by ticker.
func parsePayload(body []byte) (map[string]wireBond, error) {
	var payload struct {
		Status string     `json:"status"`
		Bonds  []wireBond `json:"bonds"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("bonos: %w: %v", ErrMalformed, err)
	}
	if payload.Status != "ok" || len(payload.Bonds) == 0 {
		return nil, fmt.Errorf("bonos: %w: unexpected status %q or empty bond list", ErrMalformed, payload.Status)
	}
	byTicker := make(map[string]wireBond, len(payload.Bonds))
	for _, b := range payload.Bonds {
		byTicker[b.Ticker] = b
	}
	return byTicker, nil
}

// lookup resolves a requested ticker against the payload index, falling back
// to the dollar-conversion variant (input + "D") published in USD.
func lookup(byTicker map[string]wireBond, ticker string) (wireBond, bool) {
	if wire, ok := byTicker[ticker]; ok {
		return wire, true
	}
	if wire, ok := byTicker[ticker+"D"]; ok {
		return wire, true
	}
	return wireBond{}, false
}
