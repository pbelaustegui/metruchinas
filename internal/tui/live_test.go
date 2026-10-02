//go:build integration

package tui

import (
	"fmt"
	"math"
	"testing"
	"time"

	"metruchinas/internal/tesoro"
)

// TestLiveSourcesFetchRealData hits every public API for real. It is a smoke
// test with structural assertions, not a value check: levels change every
// business day, so nothing here pins a number. What it does pin is the shape the
// dashboard depends on — every tracked tenor present, every daily change
// computed against the previous business day, a plausible publication date, and
// the 10Y-2Y spread derived from that same curve. A curve with plausible levels
// and no deltas, or a curve of the wrong length, fails here.
//
// It is excluded from the default suite. Run it explicitly:
//
//	go test -tags=integration ./internal/tui -run TestLiveSourcesFetchRealData -v
func TestLiveSourcesFetchRealData(t *testing.T) {
	m := New()
	msg, ok := m.refresh(false)().(dataMsg)
	if !ok {
		t.Fatal("refresh() command did not produce a dataMsg")
	}
	if msg.quotesErr != nil {
		t.Errorf("dolar fetch failed: %v", msg.quotesErr)
	}
	if msg.riesgoErr != nil {
		t.Errorf("riesgo fetch failed: %v", msg.riesgoErr)
	}
	if msg.bondsErr != nil {
		t.Errorf("bonos fetch failed: %v", msg.bondsErr)
	}
	if msg.treasuryErr != nil {
		t.Errorf("treasury fetch failed: %v", msg.treasuryErr)
	}
	if msg.fedErr != nil {
		t.Errorf("fed fetch failed: %v", msg.fedErr)
	}
	if msg.itcrmErr != nil {
		t.Errorf("itcrm fetch failed: %v", msg.itcrmErr)
	}
	if msg.ipcErr != nil {
		t.Errorf("ipc fetch failed: %v", msg.ipcErr)
	}
	if msg.uscpiErr != nil {
		t.Errorf("uscpi fetch failed: %v", msg.uscpiErr)
	}
	if rows := displayRows(msg.quotes); len(rows) == 0 {
		t.Error("no displayed houses parsed from the live payload")
	}
	if msg.riesgo.Value <= 0 {
		t.Errorf("riesgo value = %v, want a positive index", msg.riesgo.Value)
	}

	// The curve is the part of the pipeline most sensitive to a payload change:
	// Treasury adds and retires tenor columns and the parser resolves them by
	// header name, so a renamed column now reports an error instead of an empty
	// section (see internal/tesoro). These assertions pin what must survive.
	if msg.curve.Date.IsZero() {
		t.Error("curve publication date is zero")
	}
	if msg.curve.Date.After(time.Now()) {
		t.Errorf("curve publication date %v is in the future", msg.curve.Date)
	}

	wantTenors := tesoro.Tenors()
	if len(msg.curve.Points) != len(wantTenors) {
		t.Fatalf("curve publishes %d tenors, want %d: a tracked column is missing from the live payload",
			len(msg.curve.Points), len(wantTenors))
	}
	levels := make(map[string]float64, len(msg.curve.Points))
	for i, tenor := range wantTenors {
		point := msg.curve.Points[i]
		if point.Tenor != tenor.Key || point.Label != tenor.Label {
			t.Errorf("curve point %d = %q/%q, want %q/%q", i, point.Tenor, point.Label, tenor.Key, tenor.Label)
			continue
		}
		if point.Yield <= 0 || point.Yield > 25 {
			t.Errorf("%s: implausible par yield %v", point.Tenor, point.Yield)
		}
		if point.DeltaBp == nil {
			t.Errorf("%s: no daily change against the previous business day", point.Tenor)
		}
		levels[point.Tenor] = point.Yield

		delta := "—"
		if point.DeltaBp != nil {
			delta = fmt.Sprintf("%+.0f pb", *point.DeltaBp)
		}
		t.Logf("curve %-10s %-5s %6.2f%%  %s", point.Tenor, point.Label, point.Yield, delta)
	}

	// The spread must be the one this curve implies, not a plausible number.
	spread, ok := msg.curve.SpreadBp("10Y", "2Y")
	if !ok {
		t.Error("the live curve is missing the 10Y or the 2Y tenor, so no spread can be computed")
	} else {
		want := (levels["10 Yr"] - levels["2 Yr"]) * 100
		if math.Abs(spread-want) > 1e-9 {
			t.Errorf("spread = %v bp, want %v bp from 10Y %v minus 2Y %v", spread, want, levels["10 Yr"], levels["2 Yr"])
		}
		t.Logf("curve spread: 10Y-2Y = %+.0f pb", spread)
	}

	// The reference rate is the other payload most sensitive to a source
	// change: three single-series CSVs, one of which lags with empty cells, and
	// a delta derived from the last non-empty row. These assertions pin the
	// structure, not the level.
	if msg.rate.TargetLow <= 0 || msg.rate.TargetHigh <= 0 || msg.rate.Effective <= 0 {
		t.Errorf("fed levels = %v/%v/%v, want all three positive", msg.rate.TargetLow, msg.rate.TargetHigh, msg.rate.Effective)
	}
	if msg.rate.TargetLow >= msg.rate.TargetHigh {
		t.Errorf("fed target range = %v-%v, want the lower bound below the upper", msg.rate.TargetLow, msg.rate.TargetHigh)
	}
	if msg.rate.Date.IsZero() {
		t.Error("fed observation date is zero")
	}
	if msg.rate.Date.After(time.Now()) {
		t.Errorf("fed observation date %v is in the future", msg.rate.Date)
	}
	if msg.rate.EffectiveDeltaBp != nil {
		if delta := *msg.rate.EffectiveDeltaBp; math.IsNaN(delta) || math.IsInf(delta, 0) {
			t.Errorf("fed effective change = %v, want a finite value or nil", delta)
		}
	}

	// ITCRM comes from an XLSX the BCRA republishes, so the failure modes are a
	// moved file or a reshuffled sheet; both would surface as an error above or
	// as an implausible index here. The freshness bound catches a source that
	// stopped updating without failing.
	if msg.itcrm.Value <= 0 || math.IsNaN(msg.itcrm.Value) || math.IsInf(msg.itcrm.Value, 0) {
		t.Errorf("itcrm value = %v, want a positive finite index", msg.itcrm.Value)
	}
	if !finite(msg.itcrm.Variation) {
		t.Errorf("itcrm variation = %v, want a finite value", msg.itcrm.Variation)
	}
	assertRecent(t, "itcrm date", msg.itcrm.Date, 30*24*time.Hour)

	// Both CPIs are derived from index levels, so a wrong base month would
	// still produce plausible-looking numbers. The bounds below only reject the
	// absurd (a level mistaken for a rate); the exact derivation is pinned by
	// the offline suites. Argentine inflation can legitimately reach tens of
	// percent a year, hence the wider bound.
	assertCPI(t, "ipc", msg.ipc.Month, msg.ipc.Monthly, msg.ipc.Year12, msg.ipc.YTD, 1000, 100)
	assertCPI(t, "uscpi", msg.uscpi.Month, msg.uscpi.Monthly, msg.uscpi.Year12, msg.uscpi.YTD, 50, 5)

	t.Logf("live macro: itcrm %.2f (%+.2f%%) dated %s, ipc %s %.2f/%.2f/%.2f%%, uscpi %s %.2f/%.2f/%.2f%%",
		msg.itcrm.Value, msg.itcrm.Variation, msg.itcrm.Date.Format("02-01-2006"),
		msg.ipc.Month.Format("2006-01"), msg.ipc.Monthly, msg.ipc.Year12, msg.ipc.YTD,
		msg.uscpi.Month.Format("2006-01"), msg.uscpi.Monthly, msg.uscpi.Year12, msg.uscpi.YTD)

	t.Logf("live data: %d quotes (%d displayed), riesgo %.0f (%.2f%%, %s), %d bonds, %d curve tenors dated %s, fed %.2f-%.2f%% effective %.2f%%",
		len(msg.quotes), len(displayRows(msg.quotes)), msg.riesgo.Value, msg.riesgo.Variation, msg.riesgo.VariationClass,
		len(msg.bonds), len(msg.curve.Points), msg.curve.Date.Format("02-01-2006"),
		msg.rate.TargetLow, msg.rate.TargetHigh, msg.rate.Effective)
}

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

// assertRecent fails when a source date is zero, in the future, or older than
// maxAge, which catches a source that keeps answering but stopped publishing.
func assertRecent(t *testing.T, name string, date time.Time, maxAge time.Duration) {
	t.Helper()
	switch {
	case date.IsZero():
		t.Errorf("%s is zero", name)
	case date.After(time.Now()):
		t.Errorf("%s %v is in the future", name, date)
	case time.Since(date) > maxAge:
		t.Errorf("%s %v is older than %v: the source may have stopped publishing", name, date, maxAge)
	}
}

// assertCPI pins the shape of a CPI indicator: a first-of-month reference month
// that is recent, finite variations, and variations inside absurdity bounds.
func assertCPI(t *testing.T, name string, month time.Time, monthly, year12, ytd, maxYear12, maxMonthly float64) {
	t.Helper()
	if month.Day() != 1 {
		t.Errorf("%s month %v is not the first of a month", name, month)
	}
	assertRecent(t, name+" month", month, 120*24*time.Hour)
	for label, v := range map[string]float64{"monthly": monthly, "12m": year12, "ytd": ytd} {
		if !finite(v) {
			t.Errorf("%s %s = %v, want a finite value", name, label, v)
		}
	}
	if math.Abs(year12) > maxYear12 {
		t.Errorf("%s 12m = %v%%, beyond the +/-%v%% sanity bound", name, year12, maxYear12)
	}
	if math.Abs(monthly) > maxMonthly {
		t.Errorf("%s monthly = %v%%, beyond the +/-%v%% sanity bound", name, monthly, maxMonthly)
	}
}
