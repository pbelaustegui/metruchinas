//go:build integration

package bonos_test

import (
	"context"
	"testing"
	"time"

	"metruchinas/internal/bonos"
)

func TestLiveBondQuotes(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	tickers := []string{"GD29", "GD30", "GD35", "GD38", "GD41", "GD46"}
	c := bonos.NewClient()
	quotes, err := c.Fetch(ctx, tickers...)
	if err != nil {
		t.Fatalf("Fetch failed: %v", err)
	}
	if len(quotes) != len(tickers) {
		t.Fatalf("expected %d quotes, got %d", len(tickers), len(quotes))
	}

	for _, q := range quotes {
		if q.Err != nil {
			t.Errorf("%s: per-ticker error: %v", q.Ticker, q.Err)
			continue
		}
		if q.Ultimo <= 0 {
			t.Errorf("%s: Ultimo should be positive, got %f", q.Ticker, q.Ultimo)
		}
		t.Logf("%s: Último=%.2f, Variación=%.2f%%, Cierre=%.2f",
			q.Ticker, q.Ultimo, q.Variacion, q.Cierre)
	}
}

func TestLiveBondParity(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	c := bonos.NewClient()
	quotes, err := c.Fetch(ctx, "GD30")
	if err != nil {
		t.Fatalf("Fetch GD30 failed: %v", err)
	}
	if len(quotes) == 0 || quotes[0].Err != nil {
		t.Fatal("no valid GD30 quote")
	}

	// Use a mock CCL rate for the test (or you could fetch real dolar).
	// For a spot check, we just verify the math works with a reasonable CCL.
	cclVenta := 1200.0 // approximate
	priceUSD := quotes[0].Ultimo / cclVenta

	now := time.Now()
	tv, err := bonos.TechnicalValue("GD30", now)
	if err != nil {
		t.Fatalf("TechnicalValue failed: %v", err)
	}
	parity, err := bonos.Parity(priceUSD, "GD30", now)
	if err != nil {
		t.Fatalf("Parity failed: %v", err)
	}

	t.Logf("GD30: Último ARS=%.2f, CCL=%.2f, USD=%.4f, VT=%.4f, Paridad=%.2f%%",
		quotes[0].Ultimo, cclVenta, priceUSD, tv, parity)

	// Sanity: parity should be between 1% and 200%.
	if parity < 1 || parity > 200 {
		t.Errorf("parity %.2f%% seems unreasonable", parity)
	}
}
