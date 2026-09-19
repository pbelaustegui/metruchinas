// Package dolar is a minimal client for the public dolarapi.com API.
//
// The endpoint GET https://dolarapi.com/v1/dolares returns every USD price
// quote the API publishes (oficial, blue, bolsa, contadoconliqui, mayorista,
// cripto, tarjeta, ...). No authentication is required.
package dolar

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
	defaultBaseURL = "https://dolarapi.com"
	quotesPath     = "/v1/dolares"
	defaultTimeout = 10 * time.Second
)

// ErrMalformed reports a response body that is not the expected JSON payload.
var ErrMalformed = errors.New("dolar: malformed response payload")

// HTTPError reports a non-2xx response from the API.
type HTTPError struct {
	// StatusCode is the numeric status code returned by the server.
	StatusCode int
	// Status is the full status line, e.g. "503 Service Unavailable".
	Status string
}

// Error implements the error interface.
func (e *HTTPError) Error() string {
	return fmt.Sprintf("dolar: unexpected status %s", e.Status)
}

// Quote is a single USD price quote published by the API, identified by its
// casa (market house).
//
// Compra and Venta are pointers because the API may report null for a house
// that has not published a rate; a nil pointer means "not published", never
// zero. A null field does not fail the parse of the rest of the payload.
type Quote struct {
	// Casa identifies the market house: "oficial", "blue", "bolsa",
	// "contadoconliqui", "mayorista", "cripto", "tarjeta", ...
	Casa string
	// Nombre is the display name of the house ("Oficial", "Blue", ...).
	Nombre string
	// Compra is the buy rate in ARS per USD; nil when not published.
	Compra *float64
	// Venta is the sell rate in ARS per USD; nil when not published.
	Venta *float64
	// FechaActualizacion is the last update time of the quote,
	// parsed from the RFC3339 timestamp the API sends.
	FechaActualizacion time.Time
}

// Client fetches quotes from dolarapi.com.
//
// A zero-value Client is usable and resolves the same defaults as NewClient.
type Client struct {
	// HTTPClient performs the HTTP requests. It defaults to an http.Client
	// with a 10-second timeout that covers both connection (dial) and
	// response read.
	HTTPClient *http.Client
	// BaseURL is the API base URL. It defaults to https://dolarapi.com.
	BaseURL string
}

// NewClient returns a Client with the default timeout and base URL.
func NewClient() *Client {
	return &Client{
		HTTPClient: &http.Client{Timeout: defaultTimeout},
		BaseURL:    defaultBaseURL,
	}
}

// Fetch returns all quotes currently published by the API, in API order.
// The client stays generic: it returns every quote and lets the caller
// decide which houses to display.
//
// Error classes:
//   - *HTTPError for non-2xx responses, carrying the status code.
//   - an error wrapping ErrMalformed when the body is not the expected JSON.
//   - a wrapped error for network, read, or context failures, detectable
//     with errors.Is against context.Canceled / context.DeadlineExceeded.
func (c *Client) Fetch(ctx context.Context) ([]Quote, error) {
	body, err := c.get(ctx, quotesPath)
	if err != nil {
		return nil, err
	}
	quotes, err := parseQuotes(body)
	if err != nil {
		return nil, err
	}
	return quotes, nil
}

// Fetch returns all quotes using a default client. See Client.Fetch.
func Fetch(ctx context.Context) ([]Quote, error) {
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
		return nil, fmt.Errorf("dolar: build URL: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("dolar: build request: %w", err)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("dolar: request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, &HTTPError{StatusCode: resp.StatusCode, Status: resp.Status}
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("dolar: read response: %w", err)
	}
	return body, nil
}

func parseQuotes(body []byte) ([]Quote, error) {
	var quotes []Quote
	if err := json.Unmarshal(body, &quotes); err != nil {
		return nil, fmt.Errorf("dolar: %w: %v", ErrMalformed, err)
	}
	return quotes, nil
}
