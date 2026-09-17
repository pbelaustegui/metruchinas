package dolar

import (
	"context"
	"errors"
	"io"
	"math"
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
		if r.URL.Path != quotesPath {
			t.Errorf("request path = %q, want %q", r.URL.Path, quotesPath)
		}
		w.WriteHeader(status)
		if body != "" {
			io.WriteString(w, body)
		}
	}))
	t.Cleanup(srv.Close)
	return &Client{HTTPClient: srv.Client(), BaseURL: srv.URL}
}

func TestFetchReturnsAllQuotes(t *testing.T) {
	const body = `[
		{"moneda":"USD","casa":"oficial","nombre":"Oficial","compra":1485,"venta":1535,"fechaActualizacion":"2026-09-17T18:00:00.000Z"},
		{"moneda":"USD","casa":"blue","nombre":"Blue","compra":1535,"venta":1555,"fechaActualizacion":"2026-09-17T21:00:00.500Z"},
		{"moneda":"USD","casa":"bolsa","nombre":"Bolsa","compra":1528.4,"venta":1534.7,"fechaActualizacion":"2026-09-17T21:00:00.000Z"},
		{"moneda":"USD","casa":"contadoconliqui","nombre":"Contado con liquidación","compra":1591.7,"venta":1593.4,"fechaActualizacion":"2026-09-17T21:00:00.000Z"}
	]`
	quotes, err := newTestClient(t, http.StatusOK, body).Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if len(quotes) != 4 {
		t.Fatalf("Fetch() returned %d quotes, want 4", len(quotes))
	}

	first := quotes[0]
	if first.Casa != "oficial" {
		t.Errorf("quotes[0].Casa = %q, want %q", first.Casa, "oficial")
	}
	if first.Nombre != "Oficial" {
		t.Errorf("quotes[0].Nombre = %q, want %q", first.Nombre, "Oficial")
	}
	compra := 1485.0
	if first.Compra == nil || *first.Compra != compra {
		t.Errorf("quotes[0].Compra = %v, want %v", first.Compra, compra)
	}
	venta := 1535.0
	if first.Venta == nil || *first.Venta != venta {
		t.Errorf("quotes[0].Venta = %v, want %v", first.Venta, venta)
	}
	wantTime := time.Date(2026, 9, 17, 18, 0, 0, 0, time.UTC)
	if !first.FechaActualizacion.Equal(wantTime) {
		t.Errorf("quotes[0].FechaActualizacion = %v, want %v", first.FechaActualizacion, wantTime)
	}

	// Fractional-second timestamps must survive parsing (RFC3339 with millis).
	bolsa := quotes[2]
	if math.Abs(*bolsa.Compra-1528.4) > 1e-9 {
		t.Errorf("quotes[2].Compra = %v, want 1528.4", *bolsa.Compra)
	}
	blue := quotes[1]
	if blue.FechaActualizacion.Nanosecond() != 500_000_000 {
		t.Errorf("quotes[1].FechaActualizacion nanosecond = %d, want 500ms", blue.FechaActualizacion.Nanosecond())
	}
}

func TestFetchToleratesUnpublishedRates(t *testing.T) {
	const body = `[
		{"moneda":"USD","casa":"cripto","nombre":"Cripto","compra":null,"venta":null,"fechaActualizacion":"2026-09-17T21:00:00.000Z"},
		{"moneda":"USD","casa":"tarjeta","nombre":"Tarjeta","compra":1580.5,"venta":null,"fechaActualizacion":"2026-09-17T21:00:00.000Z"}
	]`
	quotes, err := newTestClient(t, http.StatusOK, body).Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if len(quotes) != 2 {
		t.Fatalf("Fetch() returned %d quotes, want 2", len(quotes))
	}

	cripto := quotes[0]
	if cripto.Compra != nil || cripto.Venta != nil {
		t.Errorf("cripto rates = %v/%v, want nil/nil (not published)", cripto.Compra, cripto.Venta)
	}

	tarjeta := quotes[1]
	compra := 1580.5
	if tarjeta.Compra == nil || *tarjeta.Compra != compra {
		t.Errorf("tarjeta Compra = %v, want %v", tarjeta.Compra, compra)
	}
	if tarjeta.Venta != nil {
		t.Errorf("tarjeta Venta = %v, want nil (not published)", tarjeta.Venta)
	}
}

func TestFetchFailsOnNon200Status(t *testing.T) {
	_, err := newTestClient(t, http.StatusServiceUnavailable, "unavailable").Fetch(context.Background())
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("Fetch() error = %v, want *HTTPError", err)
	}
	if httpErr.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("HTTPError.StatusCode = %d, want %d", httpErr.StatusCode, http.StatusServiceUnavailable)
	}
}

func TestFetchFailsOnMalformedPayloads(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"truncated json array", `[{"moneda":"USD","casa":"ofic`},
		{"json object instead of array", `{"moneda":"USD"}`},
		{"non-json body", `hello world`},
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
