package riesgo

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// newTestClient spins up an httptest server that answers with the given
// status and body, and returns a Client pointed at it. The server asserts
// that the client requests the expected endpoint path.
func newTestClient(t *testing.T, status int, body string) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != variacionPath {
			t.Errorf("request path = %q, want %q", r.URL.Path, variacionPath)
		}
		w.WriteHeader(status)
		if body != "" {
			io.WriteString(w, body)
		}
	}))
	t.Cleanup(srv.Close)
	return &Client{HTTPClient: srv.Client(), BaseURL: srv.URL}
}

func TestFetchReturnsParsedIndicator(t *testing.T) {
	const body = `{"ultimo":"515","fecha":"17-09-2026","variacion":"0,98%","class-variacion":"up-red"}`
	ind, err := newTestClient(t, http.StatusOK, body).Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if ind.Value != 515 {
		t.Errorf("Value = %v, want 515", ind.Value)
	}
	if ind.Variation != 0.98 {
		t.Errorf("Variation = %v, want 0.98", ind.Variation)
	}
	if ind.VariationClass != "up-red" {
		t.Errorf("VariationClass = %q, want %q", ind.VariationClass, "up-red")
	}
	wantDate := time.Date(2026, 9, 17, 0, 0, 0, 0, time.Local)
	if !ind.Date.Equal(wantDate) {
		t.Errorf("Date = %v, want %v", ind.Date, wantDate)
	}
	if ind.Date.Location() != time.Local {
		t.Errorf("Date location = %v, want %v", ind.Date.Location(), time.Local)
	}
}

func TestParsesCommaDecimalVariation(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want float64
	}{
		{"positive percent", "0,98%", 0.98},
		{"zero variation", "0,00%", 0},
		{"negative variation", "-1,25%", -1.25},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			body := fmt.Sprintf(`{"ultimo":"515","fecha":"17-09-2026","variacion":%q,"class-variacion":"down-green"}`, tt.raw)
			ind, err := newTestClient(t, http.StatusOK, body).Fetch(context.Background())
			if err != nil {
				t.Fatalf("Fetch() error = %v", err)
			}
			if ind.Variation != tt.want {
				t.Errorf("Variation = %v, want %v", ind.Variation, tt.want)
			}
		})
	}
}

func TestFetchFailsOnNon200Status(t *testing.T) {
	_, err := newTestClient(t, http.StatusBadGateway, "bad gateway").Fetch(context.Background())
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("Fetch() error = %v, want *HTTPError", err)
	}
	if httpErr.StatusCode != http.StatusBadGateway {
		t.Errorf("HTTPError.StatusCode = %d, want %d", httpErr.StatusCode, http.StatusBadGateway)
	}
}

func TestFetchFailsOnMalformedPayloads(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"empty body", ""},
		{"truncated json object", `{"ultimo":"515","fecha":"17-09-2026","variaci`},
		{"non-json body", `servicio temporalmente no disponible`},
		{"non-numeric value", `{"ultimo":"abc","fecha":"17-09-2026","variacion":"0,98%","class-variacion":"up-red"}`},
		{"malformed variation", `{"ultimo":"515","fecha":"17-09-2026","variacion":"0,9x%","class-variacion":"up-red"}`},
		{"wrong date format", `{"ultimo":"515","fecha":"2026-09-17","variacion":"0,98%","class-variacion":"up-red"}`},
		{"impossible date", `{"ultimo":"515","fecha":"32-13-2026","variacion":"0,98%","class-variacion":"up-red"}`},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			_, err := newTestClient(t, http.StatusOK, tt.body).Fetch(context.Background())
			if !errors.Is(err, ErrMalformed) {
				t.Fatalf("Fetch() error = %v, want ErrMalformed", err)
			}
		})
	}
}

func TestFetchFailsOnMissingOrEmptyFields(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"missing value", `{"fecha":"17-09-2026","variacion":"0,98%","class-variacion":"up-red"}`},
		{"empty value", `{"ultimo":"","fecha":"17-09-2026","variacion":"0,98%","class-variacion":"up-red"}`},
		{"missing variation", `{"ultimo":"515","fecha":"17-09-2026","class-variacion":"up-red"}`},
		{"empty variation", `{"ultimo":"515","fecha":"17-09-2026","variacion":"","class-variacion":"up-red"}`},
		{"missing date", `{"ultimo":"515","variacion":"0,98%","class-variacion":"up-red"}`},
		{"empty date", `{"ultimo":"515","fecha":"","variacion":"0,98%","class-variacion":"up-red"}`},
		{"missing variation class", `{"ultimo":"515","fecha":"17-09-2026","variacion":"0,98%"}`},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			_, err := newTestClient(t, http.StatusOK, tt.body).Fetch(context.Background())
			if !errors.Is(err, ErrMalformed) {
				t.Fatalf("Fetch() error = %v, want ErrMalformed", err)
			}
		})
	}
}

func TestNewClientAppliesDefaults(t *testing.T) {
	c := NewClient()
	if c.BaseURL != defaultBaseURL {
		t.Errorf("NewClient().BaseURL = %q, want %q", c.BaseURL, defaultBaseURL)
	}
	if c.HTTPClient == nil {
		t.Fatal("NewClient().HTTPClient is nil")
	}
	if c.HTTPClient.Timeout != defaultTimeout {
		t.Errorf("NewClient().HTTPClient.Timeout = %v, want %v", c.HTTPClient.Timeout, defaultTimeout)
	}
}
