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

func TestClientFetch_HappyPath(t *testing.T) {
	// Single ticker returns valid JSON and parses successfully.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("User-Agent"), "Chrome") {
			t.Errorf("User-Agent not set correctly")
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"ultimo":    "67550,00",
			"variacion": "-2,86",
			"cierre":    "69540,000",
		})
	}))
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
		t.Errorf("expected ticker GD30, got %s", q.Ticker)
	}
	if q.Err != nil {
		t.Errorf("expected no error, got %v", q.Err)
	}
	if diff := abs(q.Ultimo - 67550.00); diff > 1e-6 {
		t.Errorf("expected Ultimo 67550.00, got %f", q.Ultimo)
	}
	if diff := abs(q.Variacion - (-2.86)); diff > 1e-6 {
		t.Errorf("expected Variacion -2.86, got %f", q.Variacion)
	}
	if diff := abs(q.Cierre - 69540.00); diff > 1e-6 {
		t.Errorf("expected Cierre 69540.00, got %f", q.Cierre)
	}
}

func TestClientFetch_MultipleTickers(t *testing.T) {
	// Three tickers, all succeed, returned in order.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Return different data based on the ticker in the path.
		if strings.Contains(r.URL.Path, "GD29") {
			json.NewEncoder(w).Encode(map[string]string{
				"ultimo":    "100,00",
				"variacion": "0,50",
				"cierre":    "99,50",
			})
		} else if strings.Contains(r.URL.Path, "GD30") {
			json.NewEncoder(w).Encode(map[string]string{
				"ultimo":    "95,00",
				"variacion": "-1,00",
				"cierre":    "96,00",
			})
		} else if strings.Contains(r.URL.Path, "GD35") {
			json.NewEncoder(w).Encode(map[string]string{
				"ultimo":    "80,00",
				"variacion": "-0,75",
				"cierre":    "80,75",
			})
		}
	}))
	defer server.Close()

	client := &Client{BaseURL: server.URL}
	quotes, err := client.Fetch(context.Background(), "GD29", "GD30", "GD35")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(quotes) != 3 {
		t.Fatalf("expected 3 quotes, got %d", len(quotes))
	}
	tickers := []string{"GD29", "GD30", "GD35"}
	for i, ticker := range tickers {
		if quotes[i].Ticker != ticker {
			t.Errorf("quote %d: expected ticker %s, got %s", i, ticker, quotes[i].Ticker)
		}
		if quotes[i].Err != nil {
			t.Errorf("quote %d: expected no error, got %v", i, quotes[i].Err)
		}
	}
}

func TestClientFetch_PerTickerErrorIsolation(t *testing.T) {
	// Three tickers: first OK, second returns 500, third OK.
	// Expected: first and third have data, second has Err set, no top-level error.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "GD30") {
			w.WriteHeader(500)
			w.Write([]byte("Internal Server Error"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"ultimo":    "100,00",
			"variacion": "0,00",
			"cierre":    "100,00",
		})
	}))
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
		t.Errorf("quote 1: expected error, got nil")
	}
	if quotes[2].Err != nil {
		t.Errorf("quote 2: expected no error, got %v", quotes[2].Err)
	}
}

func TestClientFetch_AllTickersFail(t *testing.T) {
	// All tickers return 500 → top-level error.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte("Internal Server Error"))
	}))
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
	// No tickers provided → error.
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
	// Invalid JSON body → BondQuote.Err wraps ErrMalformed.
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
	if len(quotes) != 1 {
		t.Fatalf("expected 1 quote, got %d", len(quotes))
	}
	if quotes[0].Err == nil {
		t.Errorf("expected error in quote, got nil")
	}
	if !errors.Is(quotes[0].Err, ErrMalformed) {
		t.Errorf("expected error to wrap ErrMalformed, got %v", quotes[0].Err)
	}
}

func TestClientFetch_SecurityBlock(t *testing.T) {
	// Body contains "Request blocked" → BondQuote.Err wraps ErrMalformed.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<html>Request blocked</html>"))
	}))
	defer server.Close()

	client := &Client{BaseURL: server.URL}
	quotes, err := client.Fetch(context.Background(), "GD30")
	if err == nil {
		t.Fatalf("expected top-level error, got nil")
	}
	if len(quotes) != 1 {
		t.Fatalf("expected 1 quote, got %d", len(quotes))
	}
	if quotes[0].Err == nil {
		t.Errorf("expected error in quote, got nil")
	}
	if !errors.Is(quotes[0].Err, ErrMalformed) {
		t.Errorf("expected error to wrap ErrMalformed, got %v", quotes[0].Err)
	}
}

func TestClientFetch_ContextCancellation(t *testing.T) {
	// Cancelled context → error.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"ultimo":    "100,00",
			"variacion": "0,00",
			"cierre":    "100,00",
		})
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
	// Since context is cancelled immediately, we should get a top-level error.
}

func TestClientFetch_CommaDecimalParsing(t *testing.T) {
	// Test various comma-decimal formats: "67550,00", "-2,86", "69540,000"
	tests := []struct {
		name      string
		ultimo    string
		variacion string
		cierre    string
		want      [3]float64
	}{
		{
			name:      "standard format",
			ultimo:    "67550,00",
			variacion: "-2,86",
			cierre:    "69540,000",
			want:      [3]float64{67550.00, -2.86, 69540.00},
		},
		{
			name:      "no decimal part",
			ultimo:    "100,00",
			variacion: "0,00",
			cierre:    "100,00",
			want:      [3]float64{100.00, 0.00, 100.00},
		},
		{
			name:      "single decimal digit",
			ultimo:    "50,5",
			variacion: "-1,5",
			cierre:    "52,0",
			want:      [3]float64{50.5, -1.5, 52.0},
		},
		{
			name:      "large values",
			ultimo:    "999999,99",
			variacion: "-99,99",
			cierre:    "1000000,00",
			want:      [3]float64{999999.99, -99.99, 1000000.00},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]string{
					"ultimo":    tt.ultimo,
					"variacion": tt.variacion,
					"cierre":    tt.cierre,
				})
			}))
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
			if q.Err != nil {
				t.Fatalf("unexpected quote error: %v", q.Err)
			}
			if diff := abs(q.Ultimo - tt.want[0]); diff > 1e-6 {
				t.Errorf("expected Ultimo %f, got %f", tt.want[0], q.Ultimo)
			}
			if diff := abs(q.Variacion - tt.want[1]); diff > 1e-6 {
				t.Errorf("expected Variacion %f, got %f", tt.want[1], q.Variacion)
			}
			if diff := abs(q.Cierre - tt.want[2]); diff > 1e-6 {
				t.Errorf("expected Cierre %f, got %f", tt.want[2], q.Cierre)
			}
		})
	}
}

func TestClientFetch_UserAgentHeader(t *testing.T) {
	// Verify the request includes the browser User-Agent header.
	var userAgent string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userAgent = r.Header.Get("User-Agent")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"ultimo":    "100,00",
			"variacion": "0,00",
			"cierre":    "100,00",
		})
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

// Helper function to compute absolute difference.
func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
