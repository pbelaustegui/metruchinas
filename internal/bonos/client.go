// Package bonos is a minimal client for the Ámbito public bond quotes endpoint.
//
// The endpoints GET https://mercados.ambito.com/bono/{TICKER}/variacion return
// the latest bond quote for each ticker (GD29, GD30, GD35, GD38, GD41, GD46).
// All values arrive as strings with comma decimal separators. No authentication
// is required. The client must provide a browser-like User-Agent header or
// requests will be blocked.
package bonos

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
	bonoPath       = "/bono"
	variacionPath  = "/variacion"
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

// BondQuote is a single bond quote returned by the API for a given ticker.
// If Err is non-nil, it indicates a per-bond error (e.g. network failure or
// malformed response for that specific ticker); the other fields are invalid.
type BondQuote struct {
	// Ticker is the bond ticker (e.g. "GD30").
	Ticker string
	// Ultimo is the last price in ARS, parsed from the comma-decimal string.
	Ultimo float64
	// Variacion is the daily variation as a percentage, parsed from the
	// comma-decimal string (e.g. -2.86 for "-2,86").
	Variacion float64
	// Cierre is the previous close price in ARS, parsed from the comma-decimal
	// string.
	Cierre float64
	// Err is non-nil if the request or parse for this specific ticker failed;
	// nil means Ultimo, Variacion, and Cierre are valid.
	Err error
}

// Client fetches bond quotes from mercados.ambito.com.
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

// Fetch returns bond quotes for each ticker provided.
//
// Each ticker is fetched individually (one HTTP request per ticker). Errors
// are handled per-ticker: a failing ticker returns a BondQuote with its Err
// field set, not a top-level error.
//
// Top-level errors are returned only for:
//   - no tickers provided (ErrNoTickers)
//   - ALL tickers failed (wrapped context or network error)
//   - context cancelled before any fetch completes
//
// Results are returned in the same order as the input tickers.
//
// Error classes:
//   - An error wrapping ErrNoTickers when no tickers are provided.
//   - Per-bond *HTTPError for non-2xx responses on that ticker.
//   - Per-bond error wrapping ErrMalformed when the body is not the expected
//     JSON or carries missing or unparseable fields.
//   - Top-level wrapped error for context cancellation or if ALL tickers fail.
func (c *Client) Fetch(ctx context.Context, tickers ...string) ([]BondQuote, error) {
	if len(tickers) == 0 {
		return nil, fmt.Errorf("bonos: no tickers provided")
	}

	quotes := make([]BondQuote, len(tickers))
	failureCount := 0

	for i, ticker := range tickers {
		body, err := c.get(ctx, ticker)
		if err != nil {
			quotes[i] = BondQuote{Ticker: ticker, Err: err}
			failureCount++
			continue
		}
		quote, err := parseQuote(ticker, body)
		if err != nil {
			quotes[i] = BondQuote{Ticker: ticker, Err: err}
			failureCount++
			continue
		}
		quotes[i] = quote
	}

	// If all tickers failed, return a top-level error.
	if failureCount == len(tickers) {
		// Return the first ticker's error as the top-level error.
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

func (c *Client) get(ctx context.Context, ticker string) ([]byte, error) {
	httpClient := c.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultTimeout}
	}
	base := c.BaseURL
	if base == "" {
		base = defaultBaseURL
	}
	endpoint, err := url.JoinPath(base, bonoPath, ticker, variacionPath)
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
	// Check for security block detection.
	if strings.Contains(string(body), "Request blocked") {
		return nil, fmt.Errorf("bonos: %w: security block detected", ErrMalformed)
	}
	return body, nil
}

// wireQuote mirrors the raw API payload, where every value is a string.
type wireQuote struct {
	Ultimo    string `json:"ultimo"`
	Variacion string `json:"variacion"`
	Cierre    string `json:"cierre"`
}

func parseQuote(ticker string, body []byte) (BondQuote, error) {
	var wire wireQuote
	if err := json.Unmarshal(body, &wire); err != nil {
		return BondQuote{Ticker: ticker}, fmt.Errorf("bonos: %w: %v", ErrMalformed, err)
	}

	ultimo, err := parseFloatField("ultimo", wire.Ultimo)
	if err != nil {
		return BondQuote{Ticker: ticker}, err
	}
	variacion, err := parseFloatField("variacion", wire.Variacion)
	if err != nil {
		return BondQuote{Ticker: ticker}, err
	}
	cierre, err := parseFloatField("cierre", wire.Cierre)
	if err != nil {
		return BondQuote{Ticker: ticker}, err
	}

	return BondQuote{
		Ticker:    ticker,
		Ultimo:    ultimo,
		Variacion: variacion,
		Cierre:    cierre,
	}, nil
}

// parseFloatField parses a numeric string value with a comma decimal separator.
// Surrounding whitespace is trimmed. Missing or unparseable values are errors.
func parseFloatField(name, raw string) (float64, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return 0, fmt.Errorf("bonos: %w: field %q is missing or empty", ErrMalformed, name)
	}
	normalized := strings.ReplaceAll(s, ",", ".")
	v, err := strconv.ParseFloat(normalized, 64)
	if err != nil {
		return 0, fmt.Errorf("bonos: %w: invalid %s value %q", ErrMalformed, name, raw)
	}
	return v, nil
}
