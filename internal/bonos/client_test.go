package bonos

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// testPayload builds a source-shaped /api/bonos body. Entries are given as
// (ticker, precio, pctChange); a nil precio models a "no published price"
// entry and a nil pctChange models a missing daily variation.
func testPayload(t *testing.T, entries ...[3]interface{}) []byte {
	t.Helper()
	bonds := make([]map[string]interface{}, 0, len(entries))
	for _, e := range entries {
		b := map[string]interface{}{"ticker": e[0], "tickerBase": strings.TrimSuffix(e[0].(string), "D")}
		if e[1] != nil {
			b["precio"] = e[1]
		} else {
			b["precio"] = nil
		}
		if e[2] != nil {
			b["pctChange"] = e[2]
		}
		bonds = append(bonds, b)
	}
	body, err := json.Marshal(map[string]interface{}{"status": "ok", "count": len(bonds), "bonds": bonds})
	if err != nil {
		t.Fatalf("build payload: %v", err)
	}
	return body
}

// newBondsServer serves the given payload at /api/bonos and asserts the path
// and User-Agent on every request.
func newBondsServer(t *testing.T, body []byte) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/bonos" {
			t.Errorf("unexpected path %q, want /api/bonos", r.URL.Path)
		}
		if !strings.Contains(r.Header.Get("User-Agent"), "Chrome") {
			t.Errorf("User-Agent not set correctly")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(body)
	}))
}

func TestClientFetch_HappyPath(t *testing.T) {
	// A single base ticker resolves to its dollar-conversion variant.
	body := testPayload(t, [3]interface{}{"GD30D", 57.57, -0.06})
	server := newBondsServer(t, body)
	defer server.Close()

	client := &Client{BaseURL: server.URL}
	quotes, err := client.Fetch(context.Background(), "GD30")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(quotes) != 1 {
		t.Fatalf("expected 1 quote, got %d", len(quotes))
	}
	q := quotes[0]
	if q.Ticker != "GD30" {
		t.Errorf("expected requested ticker GD30 echoed, got %s", q.Ticker)
	}
	if q.Err != nil {
		t.Errorf("expected no error, got %v", q.Err)
	}
	if diff := abs(q.Ultimo - 57.57); diff > 1e-9 {
		t.Errorf("expected Ultimo 57.57, got %f", q.Ultimo)
	}
	if diff := abs(q.Variacion - (-0.06)); diff > 1e-9 {
		t.Errorf("expected Variacion -0.06, got %f", q.Variacion)
	}
}

func TestClientFetch_MultipleTickersOneRequest(t *testing.T) {
	// Six tickers are served from a single payload in input order.
	body := testPayload(t,
		[3]interface{}{"GD29D", 55.52, -0.34},
		[3]interface{}{"GD30D", 57.57, -0.06},
		[3]interface{}{"GD35D", 77.84, -0.46},
		[3]interface{}{"GD38D", 81.86, -0.17},
		[3]interface{}{"GD41D", 72.45, -0.75},
		[3]interface{}{"GD46D", 66.11, -1.23},
	)
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.Header().Set("Content-Type", "application/json")
		w.Write(body)
	}))
	defer server.Close()

	client := &Client{BaseURL: server.URL}
	quotes, err := client.Fetch(context.Background(), "GD29", "GD30", "GD35", "GD38", "GD41", "GD46")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(quotes) != 6 {
		t.Fatalf("expected 6 quotes, got %d", len(quotes))
	}
	wantPrices := []float64{55.52, 57.57, 77.84, 81.86, 72.45, 66.11}
	wantVars := []float64{-0.34, -0.06, -0.46, -0.17, -0.75, -1.23}
	for i, q := range quotes {
		if q.Err != nil {
			t.Errorf("quote %d (%s): expected no error, got %v", i, q.Ticker, q.Err)
			continue
		}
		if diff := abs(q.Ultimo - wantPrices[i]); diff > 1e-9 {
			t.Errorf("quote %d: expected Ultimo %f, got %f", i, wantPrices[i], q.Ultimo)
		}
		if diff := abs(q.Variacion - wantVars[i]); diff > 1e-9 {
			t.Errorf("quote %d: expected Variacion %f, got %f", i, wantVars[i], q.Variacion)
		}
	}
	if requestCount != 1 {
		t.Errorf("expected exactly 1 HTTP request for 6 tickers, got %d", requestCount)
	}
}

func TestClientFetch_MissingTickerErrorIsolation(t *testing.T) {
	// One requested ticker is absent from the payload: it carries an error,
	// the rest parse; no top-level error.
	body := testPayload(t,
		[3]interface{}{"GD29D", 55.52, -0.34},
		[3]interface{}{"GD35D", 77.84, -0.46},
	)
	server := newBondsServer(t, body)
	defer server.Close()

	client := &Client{BaseURL: server.URL}
	quotes, err := client.Fetch(context.Background(), "GD29", "GD30", "GD35")
	if err != nil {
		t.Fatalf("expected no top-level error, got %v", err)
	}
	if len(quotes) != 3 {
		t.Fatalf("expected 3 quotes, got %d", len(quotes))
	}
	if quotes[0].Err != nil {
		t.Errorf("quote 0: expected no error, got %v", quotes[0].Err)
	}
	if quotes[1].Err == nil {
		t.Errorf("quote 1: expected missing-ticker error, got nil")
	} else if !errors.Is(quotes[1].Err, ErrMalformed) {
		t.Errorf("quote 1: expected error to wrap ErrMalformed, got %v", quotes[1].Err)
	}
	if quotes[2].Err != nil {
		t.Errorf("quote 2: expected no error, got %v", quotes[2].Err)
	}
}

func TestClientFetch_NullPriceForTicker(t *testing.T) {
	// A ticker present in the payload but with a null precio is a per-ticker
	// error, not a zero price.
	body := testPayload(t, [3]interface{}{"GD30D", nil, -0.06})
	server := newBondsServer(t, body)
	defer server.Close()

	client := &Client{BaseURL: server.URL}
	quotes, err := client.Fetch(context.Background(), "GD30")
	if err == nil {
		t.Fatalf("expected top-level error when all tickers fail, got nil")
	}
	if !errors.Is(quotes[0].Err, ErrMalformed) {
		t.Errorf("expected error to wrap ErrMalformed, got %v", quotes[0].Err)
	}
	if quotes[0].Ultimo != 0 {
		t.Errorf("expected zero Ultimo on error, got %f", quotes[0].Ultimo)
	}
}

func TestClientFetch_MissingPctChangeDefaultsToZero(t *testing.T) {
	// The payload omits pctChange: variation renders as a flat 0, price valid.
	body := testPayload(t, [3]interface{}{"GD30D", 57.57, nil})
	server := newBondsServer(t, body)
	defer server.Close()

	client := &Client{BaseURL: server.URL}
	quotes, err := client.Fetch(context.Background(), "GD30")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if quotes[0].Err != nil {
		t.Fatalf("expected no error, got %v", quotes[0].Err)
	}
	if quotes[0].Variacion != 0 {
		t.Errorf("expected Variacion 0 for missing pctChange, got %f", quotes[0].Variacion)
	}
	if diff := abs(quotes[0].Ultimo - 57.57); diff > 1e-9 {
		t.Errorf("expected Ultimo 57.57, got %f", quotes[0].Ultimo)
	}
}

func TestClientFetch_ExactTickerMatchPreferred(t *testing.T) {
	// When the requested ticker exists verbatim AND a "D" variant exists, the
	// verbatim entry wins.
	body := testPayload(t,
		[3]interface{}{"GD30", 10.0, 1.0},
		[3]interface{}{"GD30D", 57.57, -0.06},
	)
	server := newBondsServer(t, body)
	defer server.Close()

	client := &Client{BaseURL: server.URL}
	quotes, err := client.Fetch(context.Background(), "GD30")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if diff := abs(quotes[0].Ultimo - 10.0); diff > 1e-9 {
		t.Errorf("expected verbatim match Ultimo 10.0, got %f", quotes[0].Ultimo)
	}
}

func TestClientFetch_AllTickersFail(t *testing.T) {
	// Every requested ticker is missing → top-level error, per-ticker errors.
	body := testPayload(t, [3]interface{}{"AL30D", 85.93, 0.6})
	server := newBondsServer(t, body)
	defer server.Close()

	client := &Client{BaseURL: server.URL}
	quotes, err := client.Fetch(context.Background(), "GD29", "GD30", "GD35")
	if err == nil {
		t.Fatalf("expected top-level error, got nil")
	}
	if len(quotes) != 3 {
		t.Fatalf("expected 3 quotes, got %d", len(quotes))
	}
	for i := range quotes {
		if quotes[i].Err == nil {
			t.Errorf("quote %d: expected error, got nil", i)
		}
	}
}

func TestClientFetch_NoTickers(t *testing.T) {
	client := &Client{}
	quotes, err := client.Fetch(context.Background())
	if err == nil {
		t.Fatalf("expected error for no tickers, got nil")
	}
	if len(quotes) != 0 {
		t.Fatalf("expected 0 quotes, got %d", len(quotes))
	}
	if !strings.Contains(err.Error(), "no tickers") {
		t.Errorf("expected 'no tickers' in error message, got %v", err)
	}
}

func TestClientFetch_MalformedJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"invalid json"`))
	}))
	defer server.Close()

	client := &Client{BaseURL: server.URL}
	quotes, err := client.Fetch(context.Background(), "GD30")
	if err == nil {
		t.Fatalf("expected top-level error, got nil")
	}
	if !errors.Is(err, ErrMalformed) {
		t.Errorf("expected top-level error to wrap ErrMalformed, got %v", err)
	}
	if len(quotes) != 1 {
		t.Fatalf("expected 1 quote, got %d", len(quotes))
	}
	if !errors.Is(quotes[0].Err, ErrMalformed) {
		t.Errorf("expected error to wrap ErrMalformed, got %v", quotes[0].Err)
	}
}

func TestClientFetch_HTTPError(t *testing.T) {
	// Non-2xx on the single request: every ticker carries *HTTPError and the
	// first is returned top-level.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte("Internal Server Error"))
	}))
	defer server.Close()

	client := &Client{BaseURL: server.URL}
	quotes, err := client.Fetch(context.Background(), "GD29", "GD30")
	if err == nil {
		t.Fatalf("expected top-level error, got nil")
	}
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) {
		t.Errorf("expected top-level error to be *HTTPError, got %v", err)
	}
	for i := range quotes {
		if !errors.As(quotes[i].Err, &httpErr) {
			t.Errorf("quote %d: expected *HTTPError, got %v", i, quotes[i].Err)
		}
	}
}

func TestClientFetch_ContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		w.Write(testPayload(t, [3]interface{}{"GD30D", 57.57, -0.06}))
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	client := &Client{BaseURL: server.URL}
	_, err := client.Fetch(ctx, "GD30")
	if err == nil {
		t.Fatalf("expected error for cancelled context, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected error to wrap context.Canceled, got %v", err)
	}
}

func TestClientFetch_UserAgentHeader(t *testing.T) {
	var userAgent string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userAgent = r.Header.Get("User-Agent")
		w.Header().Set("Content-Type", "application/json")
		w.Write(testPayload(t, [3]interface{}{"GD30D", 57.57, -0.06}))
	}))
	defer server.Close()

	client := &Client{BaseURL: server.URL}
	_, err := client.Fetch(context.Background(), "GD30")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if userAgent != browserUA {
		t.Errorf("expected User-Agent %q, got %q", browserUA, userAgent)
	}
}

func TestClientFetch_UnexpectedStatus(t *testing.T) {
	// A payload with status != "ok" or an empty bond list is malformed.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"error","bonds":[]}`))
	}))
	defer server.Close()

	client := &Client{BaseURL: server.URL}
	_, err := client.Fetch(context.Background(), "GD30")
	if !errors.Is(err, ErrMalformed) {
		t.Errorf("expected error to wrap ErrMalformed, got %v", err)
	}
}

// Helper function to compute absolute difference.
func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
