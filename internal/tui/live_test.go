//go:build integration

package tui

import (
	"testing"
)

// TestLiveSourcesFetchRealData hits both public APIs for real, proving the
// full data path (fetchers + parsing + display selection) against live data.
//
// It is excluded from the default suite. Run it explicitly:
//
//	go test -tags=integration ./internal/tui -run TestLiveSourcesFetchRealData -v
func TestLiveSourcesFetchRealData(t *testing.T) {
	m := New()
	msg, ok := m.refresh()().(dataMsg)
	if !ok {
		t.Fatal("refresh() command did not produce a dataMsg")
	}
	if msg.quotesErr != nil {
		t.Errorf("dolar fetch failed: %v", msg.quotesErr)
	}
	if msg.riesgoErr != nil {
		t.Errorf("riesgo fetch failed: %v", msg.riesgoErr)
	}
	if rows := displayRows(msg.quotes); len(rows) == 0 {
		t.Error("no displayed houses parsed from the live payload")
	}
	if msg.riesgo.Value <= 0 {
		t.Errorf("riesgo value = %v, want a positive index", msg.riesgo.Value)
	}
	t.Logf("live data: %d quotes (%d displayed), riesgo %.0f (%.2f%%, %s)",
		len(msg.quotes), len(displayRows(msg.quotes)), msg.riesgo.Value, msg.riesgo.Variation, msg.riesgo.VariationClass)
}
