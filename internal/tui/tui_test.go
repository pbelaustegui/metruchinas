package tui

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"metruchinas/internal/bonos"
	"metruchinas/internal/dolar"
	"metruchinas/internal/fed"
	"metruchinas/internal/riesgo"
	"metruchinas/internal/tesoro"
)

func rate(v float64) *float64 { return &v }

// newTestModel returns a Model with injected fetchers and no pending refresh,
// so tests exercise one message at a time.
func newTestModel(quotes QuotesFetcher, r RiesgoFetcher) Model {
	m := New()
	m.fetchQuotes = quotes
	m.fetchRiesgo = r
	m.fetchBonds = func(ctx context.Context) ([]bonos.BondQuote, error) {
		return nil, nil
	}
	m.fetchTreasury = func(ctx context.Context, force bool) (tesoro.Curve, error) {
		return tesoro.Curve{}, nil
	}
	m.fetchFed = func(ctx context.Context, force bool) (fed.Rate, error) {
		return okFed(), nil
	}
	m.refreshing = false
	return m
}

func okQuotes(context.Context) ([]dolar.Quote, error) {
	return []dolar.Quote{
		{Casa: "oficial", Nombre: "Oficial", Compra: rate(1485), Venta: rate(1535)},
		{Casa: "blue", Nombre: "Blue", Compra: rate(1535), Venta: rate(1555)},
		{Casa: "bolsa", Nombre: "Bolsa", Compra: rate(1528.4), Venta: rate(1534.7)},
		{Casa: "contadoconliqui", Nombre: "Contado con liquidación", Compra: rate(1591.7), Venta: rate(1593.4)},
	}, nil
}

func okRiesgo(context.Context) (riesgo.Indicator, error) {
	return riesgo.Indicator{
		Value:          515,
		Variation:      0.98,
		VariationClass: "up-red",
		Date:           time.Date(2026, 9, 17, 0, 0, 0, 0, time.UTC),
	}, nil
}

func okBonds() []bonos.BondQuote {
	return []bonos.BondQuote{
		{Ticker: "GD30", Ultimo: 57.57, Variacion: -2.86},
		{Ticker: "GD29", Ultimo: 55.52, Variacion: 1.50},
	}
}

// fedDelta is the EFFR daily change used by the fed fixtures; a fixed value
// keeps every fed assertion off a shared pointer.
func fedDelta(v float64) *float64 { return &v }

// okFed is the reference rate the plan of record probed live on 2026-09-23: an
// FOMC target range of 3.75%-4.00% and an EFFR of 3.88% observed on 2026-09-21.
func okFed() fed.Rate {
	return fed.Rate{
		Date:             time.Date(2026, 9, 21, 0, 0, 0, 0, time.UTC),
		TargetLow:        3.75,
		TargetHigh:       4.00,
		Effective:        3.88,
		EffectiveDeltaBp: fedDelta(25),
	}
}

func TestUpdateQuitsOnQuitKeys(t *testing.T) {
	cases := []struct {
		name string
		key  tea.KeyMsg
	}{
		{"q", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}}},
		{"ctrl+c", tea.KeyMsg{Type: tea.KeyCtrlC}},
		{"esc", tea.KeyMsg{Type: tea.KeyEsc}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			m := newTestModel(okQuotes, okRiesgo)
			_, cmd := m.Update(tt.key)
			if cmd == nil {
				t.Fatal("Update() cmd = nil, want quit command")
			}
			if _, ok := cmd().(tea.QuitMsg); !ok {
				t.Errorf("Update() cmd produced %T, want tea.QuitMsg", cmd())
			}
		})
	}
}

func TestUpdateRefreshKeyStartsRefreshOnlyWhenIdle(t *testing.T) {
	rKey := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}}

	t.Run("starts when idle", func(t *testing.T) {
		m := newTestModel(okQuotes, okRiesgo)
		updated, cmd := m.Update(rKey)
		m = updated.(Model)
		if !m.refreshing {
			t.Error("refreshing = false, want true after r")
		}
		if cmd == nil {
			t.Error("Update() cmd = nil, want refresh command")
		}
	})

	t.Run("ignored while refreshing", func(t *testing.T) {
		m := newTestModel(okQuotes, okRiesgo)
		m.refreshing = true
		updated, cmd := m.Update(rKey)
		m = updated.(Model)
		if !m.refreshing {
			t.Error("refreshing = false, want true kept from the in-flight fetch")
		}
		if cmd != nil {
			t.Error("Update() cmd != nil, want nil while a fetch is in flight")
		}
	})
}

func TestUpdateTickStartsRefreshOnlyWhenIdle(t *testing.T) {
	t.Run("starts when idle", func(t *testing.T) {
		m := newTestModel(okQuotes, okRiesgo)
		updated, cmd := m.Update(tickMsg(time.Now()))
		m = updated.(Model)
		if !m.refreshing {
			t.Error("refreshing = false, want true after tick")
		}
		if cmd == nil {
			t.Error("Update() cmd = nil, want batched heartbeat command")
		}
	})

	t.Run("heartbeat survives an in-flight fetch", func(t *testing.T) {
		m := newTestModel(okQuotes, okRiesgo)
		m.refreshing = true
		updated, cmd := m.Update(tickMsg(time.Now()))
		m = updated.(Model)
		if !m.refreshing {
			t.Error("refreshing = false, want true kept")
		}
		if cmd == nil {
			t.Error("Update() cmd = nil, want the next heartbeat command")
		}
	})
}

func TestUpdateStoresFetchedDataAndClearsErrors(t *testing.T) {
	m := newTestModel(okQuotes, okRiesgo)
	m.quotesErr = errors.New("previous failure")
	m.riesgoErr = errors.New("previous failure")
	m.refreshing = true
	at := time.Date(2026, 9, 17, 21, 0, 0, 0, time.UTC)

	updated, _ := m.Update(dataMsg{
		quotes:    []dolar.Quote{{Casa: "blue", Nombre: "Blue"}},
		riesgo:    riesgo.Indicator{Value: 515},
		fetchedAt: at,
	})
	m = updated.(Model)

	if m.refreshing {
		t.Error("refreshing = true, want false after data arrives")
	}
	if m.quotesErr != nil || m.riesgoErr != nil {
		t.Errorf("errors = %v / %v, want nil after a successful fetch", m.quotesErr, m.riesgoErr)
	}
	if len(m.quotes) != 1 {
		t.Errorf("len(quotes) = %d, want 1", len(m.quotes))
	}
	if m.riesgo.Value != 515 {
		t.Errorf("riesgo.Value = %v, want 515", m.riesgo.Value)
	}
	if !m.quotesAt.Equal(at) || !m.riesgoAt.Equal(at) {
		t.Errorf("success stamps = %v / %v, want %v", m.quotesAt, m.riesgoAt, at)
	}
	if !m.loaded {
		t.Error("loaded = false, want true after data arrives")
	}
}

func TestUpdateKeepsPerSourceFailuresIsolated(t *testing.T) {
	boom := errors.New("source down")

	t.Run("quotes fail, riesgo survives", func(t *testing.T) {
		m := newTestModel(okQuotes, okRiesgo)
		updated, _ := m.Update(dataMsg{
			quotesErr: boom,
			riesgo:    riesgo.Indicator{Value: 515},
			fetchedAt: time.Now(),
		})
		m = updated.(Model)
		if !errors.Is(m.quotesErr, boom) {
			t.Errorf("quotesErr = %v, want %v", m.quotesErr, boom)
		}
		if m.riesgo.Value != 515 {
			t.Errorf("riesgo.Value = %v, want 515 to survive the other source failing", m.riesgo.Value)
		}
	})

	t.Run("riesgo fails, quotes survive", func(t *testing.T) {
		m := newTestModel(okQuotes, okRiesgo)
		updated, _ := m.Update(dataMsg{
			quotes:    []dolar.Quote{{Casa: "blue", Nombre: "Blue"}},
			riesgoErr: boom,
			fetchedAt: time.Now(),
		})
		m = updated.(Model)
		if !errors.Is(m.riesgoErr, boom) {
			t.Errorf("riesgoErr = %v, want %v", m.riesgoErr, boom)
		}
		if len(m.quotes) != 1 {
			t.Errorf("len(quotes) = %d, want 1 to survive the other source failing", len(m.quotes))
		}
	})
}

func TestRefreshCommandReportsPerSourceErrors(t *testing.T) {
	boom := errors.New("source down")
	boomQuotes := func(context.Context) ([]dolar.Quote, error) { return nil, boom }
	boomRiesgo := func(context.Context) (riesgo.Indicator, error) { return riesgo.Indicator{}, boom }

	cases := []struct {
		name          string
		quotes        QuotesFetcher
		riesgo        RiesgoFetcher
		wantQuotesErr bool
		wantRiesgoErr bool
	}{
		{"quotes fail, riesgo survives", boomQuotes, okRiesgo, true, false},
		{"riesgo fails, quotes survive", okQuotes, boomRiesgo, false, true},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			m := newTestModel(tt.quotes, tt.riesgo)

			msg, ok := m.refresh(false)().(dataMsg)
			if !ok {
				t.Fatal("refresh() command did not produce a dataMsg")
			}
			if got := errors.Is(msg.quotesErr, boom); got != tt.wantQuotesErr {
				t.Errorf("quotesErr = %v, want a failure = %v", msg.quotesErr, tt.wantQuotesErr)
			}
			if got := errors.Is(msg.riesgoErr, boom); got != tt.wantRiesgoErr {
				t.Errorf("riesgoErr = %v, want a failure = %v", msg.riesgoErr, tt.wantRiesgoErr)
			}
			// The healthy source must still carry its data in the same cycle.
			if !tt.wantQuotesErr && len(msg.quotes) == 0 {
				t.Error("quotes were dropped although that source was healthy")
			}
			if !tt.wantRiesgoErr && msg.riesgo.Value != 515 {
				t.Errorf("riesgo.Value = %v, want 515 from the healthy source", msg.riesgo.Value)
			}
			if msg.fetchedAt.IsZero() {
				t.Error("fetchedAt is zero, want the cycle timestamp")
			}
		})
	}
}

func TestRefreshCommandStoresBothSourcesOnSuccess(t *testing.T) {
	m := newTestModel(okQuotes, okRiesgo)

	msg, ok := m.refresh(false)().(dataMsg)
	if !ok {
		t.Fatal("refresh() command did not produce a dataMsg")
	}
	if msg.quotesErr != nil {
		t.Errorf("quotesErr = %v, want nil on a healthy source", msg.quotesErr)
	}
	if msg.riesgoErr != nil {
		t.Errorf("riesgoErr = %v, want nil on a healthy source", msg.riesgoErr)
	}
	if len(msg.quotes) != 4 {
		t.Errorf("len(quotes) = %d, want 4", len(msg.quotes))
	}
	if msg.riesgo.Value != 515 {
		t.Errorf("riesgo.Value = %v, want 515", msg.riesgo.Value)
	}
	if msg.fetchedAt.IsZero() {
		t.Error("fetchedAt is zero, want the cycle timestamp")
	}

	// The successful message must round-trip through Update into live data.
	updated, _ := m.Update(msg)
	view := updated.(Model).View()
	for _, want := range []string{"Oficial", "1.485,00", "515"} {
		if !strings.Contains(view, want) {
			t.Errorf("View() after a successful refresh missing %q", want)
		}
	}
}

func TestRefreshReportsExplicitTimeoutForSlowSource(t *testing.T) {
	slowRiesgo := func(ctx context.Context) (riesgo.Indicator, error) {
		<-time.After(50 * time.Millisecond)
		return riesgo.Indicator{Value: 515}, nil
	}
	m := newTestModel(okQuotes, slowRiesgo)
	m.timeout = 10 * time.Millisecond

	msg, ok := m.refresh(false)().(dataMsg)
	if !ok {
		t.Fatal("refresh() command did not produce a dataMsg")
	}
	if msg.riesgoErr == nil {
		t.Fatal("riesgoErr = nil, want an explicit timeout failure for the slow source")
	}
	if !errors.Is(msg.riesgoErr, context.DeadlineExceeded) {
		t.Errorf("riesgoErr = %v, want it to wrap context.DeadlineExceeded", msg.riesgoErr)
	}
	// The source that finished inside the budget must still deliver.
	if msg.quotesErr != nil {
		t.Errorf("quotesErr = %v, want nil for the fast source", msg.quotesErr)
	}
	if len(msg.quotes) != 4 {
		t.Errorf("len(quotes) = %d, want 4 to survive the other source timing out", len(msg.quotes))
	}
}

func TestRefreshRejectsDataFromAnExpiredCycle(t *testing.T) {
	ignoresContext := func(context.Context) ([]dolar.Quote, error) {
		<-time.After(50 * time.Millisecond)
		return []dolar.Quote{{Casa: "oficial", Nombre: "Oficial", Compra: rate(1485)}}, nil
	}
	m := newTestModel(ignoresContext, okRiesgo)
	m.timeout = 10 * time.Millisecond

	msg, ok := m.refresh(false)().(dataMsg)
	if !ok {
		t.Fatal("refresh() command did not produce a dataMsg")
	}
	if msg.quotesErr == nil {
		t.Fatal("quotesErr = nil, want the expired cycle reported instead of buffered data")
	}
	if !errors.Is(msg.quotesErr, context.DeadlineExceeded) {
		t.Errorf("quotesErr = %v, want it to wrap context.DeadlineExceeded", msg.quotesErr)
	}
	if len(msg.quotes) != 0 {
		t.Errorf("len(quotes) = %d, want 0: data from an expired cycle must not be accepted", len(msg.quotes))
	}
}

func TestDisplayRowsFiltersAndOrdersHouses(t *testing.T) {
	cases := []struct {
		name   string
		quotes []dolar.Quote
		want   []string
	}{
		{
			name: "every displayed house, shuffled",
			quotes: []dolar.Quote{
				{Casa: "contadoconliqui", Nombre: "Contado con liquidación", Compra: rate(1591.7), Venta: rate(1593.4)},
				{Casa: "oficial", Nombre: "Oficial", Compra: rate(1485), Venta: rate(1535)},
				{Casa: "bolsa", Nombre: "Bolsa", Compra: rate(1528.4), Venta: rate(1534.7)},
				{Casa: "blue", Nombre: "Blue", Compra: rate(1535), Venta: rate(1555)},
			},
			want: []string{"Oficial", "Blue", "Bolsa", "Contado con liquidación"},
		},
		{
			name: "unknown houses are ignored and missing ones skipped",
			quotes: []dolar.Quote{
				{Casa: "mayorista", Nombre: "Mayorista", Compra: rate(1501), Venta: rate(1510)},
				{Casa: "blue", Nombre: "Blue", Compra: rate(1535), Venta: rate(1555)},
				{Casa: "tarjeta", Nombre: "Tarjeta", Compra: rate(1930.5), Venta: rate(1995.5)},
			},
			want: []string{"Blue"},
		},
		{
			name:   "empty input",
			quotes: nil,
			want:   nil,
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			rows := displayRows(tt.quotes)
			if len(rows) != len(tt.want) {
				t.Fatalf("got %d rows, want %d", len(rows), len(tt.want))
			}
			for i, wantName := range tt.want {
				if rows[i].nombre != wantName {
					t.Errorf("rows[%d].nombre = %q, want %q", i, rows[i].nombre, wantName)
				}
			}
		})
	}
}

func TestDisplayRowsFormatsUnpublishedRatesAsDash(t *testing.T) {
	rows := displayRows([]dolar.Quote{
		{Casa: "oficial", Nombre: "Oficial", Compra: rate(1485), Venta: nil},
	})
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if rows[0].venta != "—" {
		t.Errorf("venta = %q, want %q for a null rate", rows[0].venta, "—")
	}
}

func TestFormatNumber(t *testing.T) {
	cases := []struct {
		name     string
		value    float64
		decimals int
		want     string
	}{
		{"two decimals get comma separator", 1485, 2, "1.485,00"},
		{"single decimal is padded", 1528.4, 2, "1.528,40"},
		{"no decimals for the index value", 515, 0, "515"},
		{"thousands separators repeat", 1234567.891, 2, "1.234.567,89"},
		{"four digits", 1534.7, 2, "1.534,70"},
		{"negative variation", -1.25, 2, "-1,25"},
		{"zero", 0, 2, "0,00"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatNumber(tt.value, tt.decimals); got != tt.want {
				t.Errorf("formatNumber(%v, %d) = %q, want %q", tt.value, tt.decimals, got, tt.want)
			}
		})
	}
}

func TestViewShowsLoadingBeforeFirstData(t *testing.T) {
	m := newTestModel(okQuotes, okRiesgo)
	view := m.View()
	if !strings.Contains(view, "cargando") {
		t.Errorf("View() = %q, want a loading placeholder before the first fetch", view)
	}
}

func TestViewRendersDataHintsAndPerSourceErrors(t *testing.T) {
	t.Run("data and hints", func(t *testing.T) {
		m := newTestModel(okQuotes, okRiesgo)
		quotes, _ := okQuotes(context.Background())
		ind, _ := okRiesgo(context.Background())
		updated, _ := m.Update(dataMsg{quotes: quotes, riesgo: ind, fetchedAt: time.Now()})
		view := updated.(Model).View()

		for _, want := range []string{"Oficial", "1.485,00", "515", "0,98%", "q salir", "r refrescar"} {
			if !strings.Contains(view, want) {
				t.Errorf("View() missing %q", want)
			}
		}
	})

	t.Run("error replaces only its section", func(t *testing.T) {
		m := newTestModel(okQuotes, okRiesgo)
		quotes, _ := okQuotes(context.Background())
		updated, _ := m.Update(dataMsg{
			quotes:    quotes,
			riesgoErr: errors.New("ambito timeout"),
			fetchedAt: time.Now(),
		})
		view := updated.(Model).View()

		if !strings.Contains(view, "ambito timeout") {
			t.Error("View() missing the riesgo error message")
		}
		if !strings.Contains(view, "Oficial") {
			t.Error("View() dropped the dolar section while the other source failed")
		}
	})
}

func TestViewShowsDownArrowForFallingIndex(t *testing.T) {
	m := newTestModel(okQuotes, okRiesgo)
	quotes, _ := okQuotes(context.Background())
	falling := riesgo.Indicator{
		Value:          505,
		Variation:      -1.25,
		VariationClass: "down-green",
		Date:           time.Date(2026, 9, 17, 0, 0, 0, 0, time.UTC),
	}
	updated, _ := m.Update(dataMsg{quotes: quotes, riesgo: falling, fetchedAt: time.Now()})
	view := updated.(Model).View()

	if !strings.Contains(view, "▼ -1,25%") {
		t.Errorf("View() missing the falling-index arrow and signed variation:\n%s", view)
	}
	if strings.Contains(view, "▲") {
		t.Errorf("View() shows the rising arrow for a negative variation:\n%s", view)
	}
}

func TestPadRightMeasuresDisplayCellsNotBytes(t *testing.T) {
	// "liquidación" carries a multibyte rune, so a byte count undershoots the
	// padding and shifts everything after it one cell to the left.
	const accented = "Contado con liquidación"
	if got := lipgloss.Width(padRight(accented, 26)); got != 26 {
		t.Errorf("padRight(%q, 26) is %d cells wide, want 26", accented, got)
	}
}

func TestRenderQuotesAlignsRateColumnsAcrossLabels(t *testing.T) {
	m := newTestModel(okQuotes, okRiesgo)
	quotes, _ := okQuotes(context.Background())
	m.quotes = quotes

	lines := strings.Split(m.renderQuotes(), "\n")
	if len(lines) != len(displayedHouses) {
		t.Fatalf("renderQuotes() returned %d lines, want %d", len(lines), len(displayedHouses))
	}

	// Every rate separator must start at the same display column, whatever the
	// label length. The prefix carries SGR codes, which Width ignores.
	sep := " / "
	want := lipgloss.Width(lines[0][:strings.Index(lines[0], sep)])
	for _, line := range lines {
		i := strings.Index(line, sep)
		if i < 0 {
			t.Fatalf("renderQuotes() line %q has no rate separator", line)
		}
		if got := lipgloss.Width(line[:i]); got != want {
			t.Errorf("rate column starts at cell %d, want %d, in line %q", got, want, line)
		}
	}
}

func TestUpdateStoresBondData(t *testing.T) {
	m := newTestModel(okQuotes, okRiesgo)
	quotes, _ := okQuotes(context.Background())
	ind, _ := okRiesgo(context.Background())
	updated, _ := m.Update(dataMsg{
		quotes:    quotes,
		riesgo:    ind,
		bonds:     okBonds(),
		fetchedAt: time.Now(),
	})
	model := updated.(Model)
	if len(model.bonds) != 2 {
		t.Errorf("expected 2 bonds, got %d", len(model.bonds))
	}
}

func TestUpdateKeepsBondFailureIsolated(t *testing.T) {
	m := newTestModel(okQuotes, okRiesgo)
	quotes, _ := okQuotes(context.Background())
	ind, _ := okRiesgo(context.Background())
	bondErr := errors.New("bond fetch failed")
	updated, _ := m.Update(dataMsg{
		quotes:    quotes,
		riesgo:    ind,
		bondsErr:  bondErr,
		fetchedAt: time.Now(),
	})
	model := updated.(Model)
	if model.quotesErr != nil {
		t.Error("quotes should be unaffected by bond failure")
	}
	if model.riesgoErr != nil {
		t.Error("riesgo should be unaffected by bond failure")
	}
	if model.bondsErr == nil {
		t.Error("bondsErr should be set")
	}
}

func TestRefreshCommandFetchesBonds(t *testing.T) {
	m := newTestModel(okQuotes, okRiesgo)
	m.fetchBonds = func(ctx context.Context) ([]bonos.BondQuote, error) {
		return okBonds(), nil
	}
	cmd := m.refresh(false)
	msg := cmd().(dataMsg)
	if len(msg.bonds) != 2 {
		t.Errorf("expected 2 bonds in refresh result, got %d", len(msg.bonds))
	}
}

func TestViewRendersBondsSection(t *testing.T) {
	m := newTestModel(okQuotes, okRiesgo)
	m.loaded = true
	m.bonds = okBonds()
	view := m.View()
	if !strings.Contains(view, "Bonos soberanos") {
		t.Error("view should contain bonds section header")
	}
	if !strings.Contains(view, "GD30") {
		t.Error("view should contain GD30 ticker")
	}
	if !strings.Contains(view, "USD 57,57") {
		t.Error("view should contain the USD price published by the source")
	}
}

func TestViewRendersBondError(t *testing.T) {
	m := newTestModel(okQuotes, okRiesgo)
	m.loaded = true
	m.bonds = []bonos.BondQuote{
		{Ticker: "GD30", Err: errors.New("test error")},
		{Ticker: "GD29", Ultimo: 55.52, Variacion: 1.50},
	}
	view := m.View()
	if !strings.Contains(view, "GD30") {
		t.Error("view should show GD30 even with error")
	}
	if !strings.Contains(view, "GD29") {
		t.Error("view should show GD29 with data")
	}
}

// staleAt is a fixed wall-clock time used by the stale-rendering tests.
var staleAt = time.Date(2026, 9, 17, 10, 30, 0, 0, time.Local)

func TestUpdateStampsPerSourceSuccessTimes(t *testing.T) {
	m := newTestModel(okQuotes, okRiesgo)
	quotes, _ := okQuotes(context.Background())
	ind, _ := okRiesgo(context.Background())
	updated, _ := m.Update(dataMsg{quotes: quotes, riesgo: ind, bonds: okBonds(), fetchedAt: staleAt})
	m = updated.(Model)

	if !m.quotesAt.Equal(staleAt) {
		t.Errorf("quotesAt = %v, want %v after a successful fetch", m.quotesAt, staleAt)
	}
	if !m.riesgoAt.Equal(staleAt) {
		t.Errorf("riesgoAt = %v, want %v after a successful fetch", m.riesgoAt, staleAt)
	}
	if !m.bondsAt.Equal(staleAt) {
		t.Errorf("bondsAt = %v, want %v after a successful fetch", m.bondsAt, staleAt)
	}

	// A later failed cycle must keep the previous success stamps and data.
	boom := errors.New("api caida")
	updated, _ = m.Update(dataMsg{quotesErr: boom, riesgoErr: boom, bondsErr: boom, fetchedAt: time.Now()})
	m = updated.(Model)

	if !m.quotesAt.Equal(staleAt) {
		t.Errorf("quotesAt = %v, want %v untouched by the failure", m.quotesAt, staleAt)
	}
	if !m.riesgoAt.Equal(staleAt) {
		t.Errorf("riesgoAt = %v, want %v untouched by the failure", m.riesgoAt, staleAt)
	}
	if !m.bondsAt.Equal(staleAt) {
		t.Errorf("bondsAt = %v, want %v untouched by the failure", m.bondsAt, staleAt)
	}
	if len(m.quotes) != 4 || m.riesgo.Value != 515 || len(m.bonds) != 2 {
		t.Error("failed cycle discarded last good data, want it kept for stale rendering")
	}
}

func TestViewRendersStaleQuotesWithErrorAndTimestamp(t *testing.T) {
	m := newTestModel(okQuotes, okRiesgo)
	quotes, _ := okQuotes(context.Background())
	updated, _ := m.Update(dataMsg{quotes: quotes, fetchedAt: staleAt})
	m = updated.(Model)
	m.quotesErr = errors.New("dolarapi timeout")

	view := m.View()
	for _, want := range []string{"Oficial", "1.485,00", "dolarapi timeout", "10:30:00", "desactualizado"} {
		if !strings.Contains(view, want) {
			t.Errorf("View() missing %q for stale quotes", want)
		}
	}
}

func TestViewRendersStaleRiesgoWithErrorAndTimestamp(t *testing.T) {
	m := newTestModel(okQuotes, okRiesgo)
	ind, _ := okRiesgo(context.Background())
	updated, _ := m.Update(dataMsg{riesgo: ind, fetchedAt: staleAt})
	m = updated.(Model)
	m.riesgoErr = errors.New("ambito timeout")

	view := m.View()
	for _, want := range []string{"515", "ambito timeout", "10:30:00", "desactualizado"} {
		if !strings.Contains(view, want) {
			t.Errorf("View() missing %q for stale riesgo", want)
		}
	}
}

func TestViewFailingSourceWithoutDataShowsUnavailable(t *testing.T) {
	m := newTestModel(okQuotes, okRiesgo)
	m.loaded = true
	m.quotesErr = errors.New("dolarapi timeout")
	m.riesgoErr = errors.New("ambito timeout")
	m.bondsErr = errors.New("bonos timeout")

	view := m.View()
	if !strings.Contains(view, "no disponible") {
		t.Errorf("View() = %q, want 'no disponible' when a source never succeeded", view)
	}
	if strings.Contains(view, "desactualizado") {
		t.Error("View() claims stale data although no data was ever fetched")
	}
}

func TestViewRendersStaleRiesgoWithZeroSourceDate(t *testing.T) {
	// The source may not publish a date for the indicator; the stale gate
	// must rely on the success stamp, not on riesgo.Date.
	m := newTestModel(okQuotes, okRiesgo)
	updated, _ := m.Update(dataMsg{
		riesgo:    riesgo.Indicator{Value: 700},
		fetchedAt: staleAt,
	})
	m = updated.(Model)
	m.riesgoErr = errors.New("ambito timeout")

	view := m.View()
	if strings.Contains(view, "no disponible") {
		t.Errorf("View() = %q, want stale data although the source date is zero", view)
	}
	for _, want := range []string{"700", "ambito timeout", "10:30:00", "desactualizado"} {
		if !strings.Contains(view, want) {
			t.Errorf("View() missing %q for stale riesgo with zero source date", want)
		}
	}
}

func TestRenderBondsStaleNoteStartsOnItsOwnLine(t *testing.T) {
	m := newTestModel(okQuotes, okRiesgo)
	updated, _ := m.Update(dataMsg{bonds: okBonds(), fetchedAt: staleAt})
	m = updated.(Model)
	m.bondsErr = errors.New("bonos timeout")

	out := m.renderBonds()
	idx := strings.Index(out, "  ⚠ desactualizado")
	if idx < 0 {
		t.Fatalf("renderBonds() missing the stale note: %q", out)
	}
	if idx == 0 || out[idx-1] != '\n' {
		t.Errorf("stale note does not start on its own line: %q", out)
	}
	if !strings.HasSuffix(out, "\n") || strings.HasSuffix(out, "\n\n") {
		t.Errorf("renderBonds() must end with exactly one newline: %q", out)
	}
}

func TestViewRendersStaleBondsInOrderWithValues(t *testing.T) {
	m := newTestModel(okQuotes, okRiesgo)
	updated, _ := m.Update(dataMsg{bonds: okBonds(), fetchedAt: staleAt})
	m = updated.(Model)
	m.bondsErr = errors.New("bonos timeout")

	view := m.View()
	rowsAt := strings.Index(view, "GD29")
	noteAt := strings.Index(view, "desactualizado")
	if rowsAt < 0 || noteAt < 0 {
		t.Fatalf("View() missing bond rows or stale note: %q", view)
	}
	if rowsAt > noteAt {
		t.Error("stale note renders before the bond rows, want rows first")
	}
	for _, want := range []string{"GD30", "57,57", "GD29", "55,52", "bonos timeout", "10:30:00"} {
		if !strings.Contains(view, want) {
			t.Errorf("View() missing %q for stale bonds", want)
		}
	}
}

func TestVariationStyleFollowsMarketConvention(t *testing.T) {
	// Bonds: green when the variation is positive, red when negative.
	// Country risk is the opposite convention, which is why the bond rows
	// cannot reuse upStyle/downStyle directly.
	if got := variationStyle(1.5); !reflect.DeepEqual(got, gainStyle) {
		t.Errorf("variationStyle(1.5) = %v, want gainStyle (green)", got)
	}
	if got := variationStyle(-2.86); !reflect.DeepEqual(got, lossStyle) {
		t.Errorf("variationStyle(-2.86) = %v, want lossStyle (red)", got)
	}
	if got := variationStyle(0); !reflect.DeepEqual(got, gainStyle) {
		t.Errorf("variationStyle(0) = %v, want gainStyle (only negatives are red)", got)
	}
}

func TestViewRendersBondVariationArrows(t *testing.T) {
	m := newTestModel(okQuotes, okRiesgo)
	updated, _ := m.Update(dataMsg{bonds: []bonos.BondQuote{
		{Ticker: "GD30", Ultimo: 57.57, Variacion: 1.5},
		{Ticker: "GD29", Ultimo: 55.52, Variacion: -2.86},
	}, fetchedAt: staleAt})
	view := updated.(Model).View()

	for _, want := range []string{"▲ 1.50%", "▼ -2.86%"} {
		if !strings.Contains(view, want) {
			t.Errorf("View() missing %q", want)
		}
	}
}

// curveYields are the curve levels the treasury tests render: the tenors the
// plan of record probed live on 2026-09-21, plus realistic short-end fixtures.
var curveYields = map[string]float64{
	"1 Mo":      4.05,
	"1.5 Month": 4.02,
	"2 Mo":      4.03,
	"3 Mo":      3.98,
	"4 Mo":      3.95,
	"6 Mo":      3.90,
	"1 Yr":      4.45,
	"2 Yr":      4.76,
	"3 Yr":      4.64,
	"5 Yr":      4.83,
	"7 Yr":      4.90,
	"10 Yr":     4.96,
	"20 Yr":     5.33,
	"30 Yr":     5.29,
}

// curveDeltas are the daily changes in basis points for curveYields.
var curveDeltas = map[string]float64{
	"1 Mo":      -3,
	"1.5 Month": -3,
	"2 Mo":      -3,
	"3 Mo":      -3,
	"4 Mo":      -2,
	"6 Mo":      -1,
	"1 Yr":      1,
	"2 Yr":      -1,
	"3 Yr":      2,
	"5 Yr":      3,
	"7 Yr":      4,
	"10 Yr":     3,
	"20 Yr":     0,
	"30 Yr":     -4,
}

// curveDate is the publication date of the test curve.
var curveDate = time.Date(2026, 9, 21, 0, 0, 0, 0, time.Local)

// testCurve builds a curve from level and delta maps keyed by Treasury column
// header, in the package's ascending tenor order. A level missing from yields is
// not published; a tenor missing from deltas has an unknown change.
func testCurve(yields, deltas map[string]float64) tesoro.Curve {
	points := make([]tesoro.Point, 0, len(tesoro.Tenors()))
	for _, tenor := range tesoro.Tenors() {
		yield, ok := yields[tenor.Key]
		if !ok {
			continue
		}
		point := tesoro.Point{Tenor: tenor.Key, Label: tenor.Label, Yield: yield}
		if delta, ok := deltas[tenor.Key]; ok {
			d := delta
			point.DeltaBp = &d
		}
		points = append(points, point)
	}
	return tesoro.Curve{Date: curveDate, Points: points}
}

func okCurve() tesoro.Curve { return testCurve(curveYields, curveDeltas) }

// curveFetcher returns the curve without recording the force intent.
func curveFetcher(curve tesoro.Curve) TreasuryFetcher {
	return func(context.Context, bool) (tesoro.Curve, error) { return curve, nil }
}

// runCmds executes a command and, when it is a batch (Init and the tick
// heartbeat both return one), every command inside it.
func runCmds(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	batch, ok := cmd().(tea.BatchMsg)
	if !ok {
		return
	}
	for _, c := range batch {
		if c != nil {
			c()
		}
	}
}

func TestRefreshCommandFetchesTreasuryCurve(t *testing.T) {
	m := newTestModel(okQuotes, okRiesgo)
	m.fetchTreasury = curveFetcher(okCurve())

	msg, ok := m.refresh(false)().(dataMsg)
	if !ok {
		t.Fatal("refresh() command did not produce a dataMsg")
	}
	if msg.treasuryErr != nil {
		t.Fatalf("treasuryErr = %v, want nil", msg.treasuryErr)
	}
	if len(msg.curve.Points) != len(tesoro.Tenors()) {
		t.Errorf("curve points = %d, want %d", len(msg.curve.Points), len(tesoro.Tenors()))
	}
	if !msg.curve.Date.Equal(curveDate) {
		t.Errorf("curve date = %v, want %v", msg.curve.Date, curveDate)
	}
}

func TestRefreshRecordsTheForceIntent(t *testing.T) {
	// The cache invalidates on `r` only. The fetcher signature carries the
	// intent, so the model needs no extra flag to express it.
	var forced []bool
	m := newTestModel(okQuotes, okRiesgo)
	m.fetchTreasury = func(_ context.Context, force bool) (tesoro.Curve, error) {
		forced = append(forced, force)
		return okCurve(), nil
	}

	// The heartbeat and the r key both answer with a command (the heartbeat
	// batches the next tick with the refresh), so the intent is observed by
	// running them. A short interval keeps the batched tick from stalling the
	// test.
	m.interval = time.Millisecond

	if _, tick := m.Update(tickMsg(time.Now())); tick != nil {
		runCmds(tick)
	}
	rKey := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}}
	if _, press := m.Update(rKey); press != nil {
		runCmds(press)
	}

	if len(forced) != 2 {
		t.Fatalf("fetcher called %d times, want 2", len(forced))
	}

	if forced[0] {
		t.Error("tick asked for a forced refetch, want the cached curve")
	}
	if !forced[1] {
		t.Error("the r key did not ask for a forced refetch")
	}
}

func TestUpdateStoresTreasuryCurve(t *testing.T) {
	m := newTestModel(okQuotes, okRiesgo)
	m.treasuryErr = errors.New("previous failure")

	updated, _ := m.Update(dataMsg{curve: okCurve(), fetchedAt: staleAt})
	m = updated.(Model)

	if m.treasuryErr != nil {
		t.Errorf("treasuryErr = %v, want nil after a successful fetch", m.treasuryErr)
	}
	if len(m.curve.Points) != len(tesoro.Tenors()) {
		t.Errorf("curve points = %d, want %d", len(m.curve.Points), len(tesoro.Tenors()))
	}
	if !m.treasuryAt.Equal(staleAt) {
		t.Errorf("treasuryAt = %v, want %v", m.treasuryAt, staleAt)
	}
}

func TestUpdateKeepsTreasuryFailureIsolated(t *testing.T) {
	boom := errors.New("treasury down")

	t.Run("treasury fails, the other sections survive", func(t *testing.T) {
		m := newTestModel(okQuotes, okRiesgo)
		quotes, _ := okQuotes(context.Background())
		ind, _ := okRiesgo(context.Background())
		updated, _ := m.Update(dataMsg{
			quotes:      quotes,
			riesgo:      ind,
			bonds:       okBonds(),
			treasuryErr: boom,
			fetchedAt:   time.Now(),
		})
		m = updated.(Model)

		if !errors.Is(m.treasuryErr, boom) {
			t.Errorf("treasuryErr = %v, want %v", m.treasuryErr, boom)
		}
		if len(m.quotes) != 4 || m.riesgo.Value != 515 || len(m.bonds) != 2 {
			t.Error("a treasury failure blanked another section")
		}
	})

	t.Run("another source fails, the treasury curve survives", func(t *testing.T) {
		m := newTestModel(okQuotes, okRiesgo)
		updated, _ := m.Update(dataMsg{
			curve:     okCurve(),
			quotesErr: boom,
			fetchedAt: time.Now(),
		})
		m = updated.(Model)
		if !errors.Is(m.quotesErr, boom) {
			t.Errorf("quotesErr = %v, want %v", m.quotesErr, boom)
		}
		if len(m.curve.Points) != len(tesoro.Tenors()) {
			t.Error("the treasury curve was dropped when another source failed")
		}
		if m.treasuryErr != nil {
			t.Errorf("treasuryErr = %v, want nil", m.treasuryErr)
		}
	})
}

func TestViewRendersTreasuryCurve(t *testing.T) {
	m := newTestModel(okQuotes, okRiesgo)
	updated, _ := m.Update(dataMsg{curve: okCurve(), fetchedAt: staleAt})
	m = updated.(Model)
	view := m.View()

	if !strings.Contains(view, "Curva del Tesoro") {
		t.Error("View() is missing the treasury section header")
	}
	if !strings.Contains(view, "21-09-2026") {
		t.Error("View() is missing the Treasury publication date")
	}
	for _, tenor := range tesoro.Tenors() {
		if !strings.Contains(view, tenor.Label) {
			t.Errorf("View() is missing tenor %q", tenor.Label)
		}
	}
	for _, want := range []string{"4,96", "4,76", "5,33", "4,45", "Spread 10Y-2Y: +20 pb"} {
		if !strings.Contains(view, want) {
			t.Errorf("View() is missing %q", want)
		}
	}
}

func TestViewRendersTreasuryLevelsInEsARFormat(t *testing.T) {
	// The level column reuses the dashboard's number formatting: comma decimals,
	// and never a dot decimal separator.
	m := newTestModel(okQuotes, okRiesgo)
	updated, _ := m.Update(dataMsg{curve: okCurve(), fetchedAt: staleAt})
	rows := updated.(Model).renderCurveRows()

	if strings.Contains(rows, "4.96") {
		t.Errorf("curve rows use a dot decimal separator:\n%s", rows)
	}
	if !strings.Contains(rows, "4,96") {
		t.Errorf("curve rows are missing the es-AR level:\n%s", rows)
	}
	// The tenor label follows the same rule as every number the dashboard
	// prints, even though Treasury spells the column "1.5 Month".
	if !strings.Contains(rows, "1,5M") {
		t.Errorf("curve rows are missing the es-AR tenor label:\n%s", rows)
	}
	if strings.Contains(rows, "1.5M") {
		t.Errorf("curve rows use a dot decimal separator in the tenor label:\n%s", rows)
	}
}

func TestViewRendersTreasuryDeltaDirectection(t *testing.T) {
	m := newTestModel(okQuotes, okRiesgo)
	updated, _ := m.Update(dataMsg{curve: okCurve(), fetchedAt: staleAt})
	rows := updated.(Model).renderCurveRows()

	for _, want := range []string{"▲  3", "▼  1", "▼  4"} {
		if !strings.Contains(rows, want) {
			t.Errorf("curve rows are missing %q:\n%s", want, rows)
		}
	}
	// A zero change is neither rising nor falling.
	if !strings.Contains(rows, "·  0") {
		t.Errorf("curve rows are missing the flat marker:\n%s", rows)
	}
}

func TestCurveDeltaColorFollowsTheYieldConvention(t *testing.T) {
	// Yields follow the market convention: a rising yield is red, a falling one
	// is green, and a flat one is muted rather than coloured. The assertions are
	// on the observable foreground colour, not on style identity: comparing
	// against upStyle would keep passing if upStyle itself were changed to blue,
	// which is exactly the regression this is here to catch.
	cases := []struct {
		name string
		bp   int
		want lipgloss.Color
	}{
		{"a rising yield is red", 3, lipgloss.Color("9")},
		{"a falling yield is green", -1, lipgloss.Color("10")},
		{"a flat yield is muted grey", 0, lipgloss.Color("241")},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if got := curveDeltaStyle(tt.bp).GetForeground(); got != lipgloss.TerminalColor(tt.want) {
				t.Errorf("curveDeltaStyle(%d) foreground = %v, want %v", tt.bp, got, tt.want)
			}
		})
	}

	// Whatever the palette is, the two directions must never share a colour.
	if curveDeltaStyle(1).GetForeground() == curveDeltaStyle(-1).GetForeground() {
		t.Error("a rising and a falling yield render in the same colour")
	}
}

func TestViewRendersMissingTenorAsDash(t *testing.T) {
	yields := map[string]float64{}
	for key, value := range curveYields {
		yields[key] = value
	}
	delete(yields, "10 Yr")
	delete(yields, "5 Yr")
	m := newTestModel(okQuotes, okRiesgo)
	updated, _ := m.Update(dataMsg{curve: testCurve(yields, curveDeltas), fetchedAt: staleAt})
	rows := updated.(Model).renderCurveRows()

	if !strings.Contains(rows, "10Y") {
		t.Errorf("a missing tenor must still occupy its slot:\n%s", rows)
	}
	for _, missing := range []string{"4,96", "4,83"} {
		if strings.Contains(rows, missing) {
			t.Errorf("an unpublished tenor rendered the level %q:\n%s", missing, rows)
		}
	}
	for _, want := range []string{"4,76", "4,90"} {
		if !strings.Contains(rows, want) {
			t.Errorf("curve rows are missing the neighbouring level %q:\n%s", want, rows)
		}
	}
}

func TestCurveColumnsAlignAcrossEveryRow(t *testing.T) {
	// The right column starts at one fixed display column on every row, whether
	// or not the tenor in it was published. The prefix carries SGR codes, which
	// Width ignores.
	m := newTestModel(okQuotes, okRiesgo)
	updated, _ := m.Update(dataMsg{curve: okCurve(), fetchedAt: staleAt})
	m = updated.(Model)

	tenors := tesoro.Tenors()
	rowsPerColumn := (len(tenors) + 1) / 2
	lines := strings.Split(m.renderCurveRows(), "\n")
	if len(lines) != rowsPerColumn {
		t.Fatalf("renderCurveRows() returned %d lines, want %d", len(lines), rowsPerColumn)
	}

	// The right column starts at one absolute display cell on every row: the
	// left cell's geometry (2 indent + 4 label + 2 + 5 yield + 2 + 4 delta = 19
	// display cells) plus the 3-cell gap between the columns. Taking row 0 as its
	// own reference would keep passing even if every row were shifted together,
	// which is how a wrong "cell 24" claim survived in the tracker.
	const wantRightColumnCell = 22
	if want := 2 + curveLabelWidth(tenors) + 2 + curveYieldWidth + 2 + curveDeltaWidth + len(curveColumnGap); want != wantRightColumnCell {
		t.Errorf("curve layout puts the right column at cell %d, want %d", want, wantRightColumnCell)
	}
	const want = wantRightColumnCell
	for i, line := range lines {
		label := tenors[i+rowsPerColumn].Label
		idx := strings.Index(line, label)
		if idx < 0 {
			t.Fatalf("row %d is missing the right column tenor %q: %q", i, label, line)
		}
		if start := lipgloss.Width(line[:idx]); start != want {
			t.Errorf("row %d starts its right column at cell %d, want %d: %q", i, start, want, line)
		}
	}

	// The dash for an unpublished tenor must not shift the column either.
	yields := map[string]float64{}
	for key, value := range curveYields {
		yields[key] = value
	}
	delete(yields, "3 Yr")
	m.curve = testCurve(yields, curveDeltas)
	for i, line := range strings.Split(m.renderCurveRows(), "\n") {
		label := tenors[i+rowsPerColumn].Label
		idx := strings.Index(line, label)
		if idx < 0 {
			t.Fatalf("with a missing tenor, row %d is missing the right column tenor %q: %q", i, label, line)
		}
		if start := lipgloss.Width(line[:idx]); start != want {
			t.Errorf("with a missing tenor, row %d starts its right column at cell %d, want %d: %q", i, start, want, line)
		}
	}
}

func TestCurveSectionFitsInTenLines(t *testing.T) {
	// Two columns of seven tenors keep the curve inside ten lines; a single
	// column would push the whole dashboard out of a terminal. The tenth line is
	// the Federal Reserve reference rate appended inside the section.
	m := newTestModel(okQuotes, okRiesgo)
	updated, _ := m.Update(dataMsg{curve: okCurve(), rate: okFed(), fetchedAt: staleAt})
	m = updated.(Model)

	lines := strings.Split(m.renderTreasury(), "\n")
	if len(lines) != 10 {
		t.Errorf("renderTreasury() returned %d lines, want 10 (header + 7 rows + spread + fed):\n%s", len(lines), m.renderTreasury())
	}
}

func TestViewFlagsAnInvertedCurve(t *testing.T) {
	yields := map[string]float64{}
	for key, value := range curveYields {
		yields[key] = value
	}
	yields["10 Yr"] = 4.31
	m := newTestModel(okQuotes, okRiesgo)
	updated, _ := m.Update(dataMsg{curve: testCurve(yields, curveDeltas), fetchedAt: staleAt})
	view := updated.(Model).View()

	if !strings.Contains(view, "Spread 10Y-2Y: -45 pb") {
		t.Errorf("View() is missing the negative spread:\n%s", view)
	}
	if !strings.Contains(view, "curva invertida") {
		t.Errorf("View() does not flag the inverted curve:\n%s", view)
	}
}

func TestViewRendersStaleTreasuryCurve(t *testing.T) {
	m := newTestModel(okQuotes, okRiesgo)
	updated, _ := m.Update(dataMsg{curve: okCurve(), fetchedAt: staleAt})
	m = updated.(Model)
	m.treasuryErr = errors.New("treasury timeout")

	view := m.View()
	for _, want := range []string{"4,96", "21-09-2026", "treasury timeout", "10:30:00", "desactualizado"} {
		if !strings.Contains(view, want) {
			t.Errorf("View() is missing %q for the stale curve", want)
		}
	}
	if !m.treasuryAt.Equal(staleAt) {
		t.Errorf("treasuryAt = %v, want the last success %v", m.treasuryAt, staleAt)
	}
}

func TestViewTreasuryUnavailableWithoutData(t *testing.T) {
	m := newTestModel(okQuotes, okRiesgo)
	m.loaded = true
	m.treasuryErr = errors.New("treasury timeout")
	quotes, _ := okQuotes(context.Background())
	m.quotes = quotes

	view := m.View()
	if !strings.Contains(view, "treasury timeout") {
		t.Errorf("View() is missing the treasury error:\n%s", view)
	}
	if strings.Contains(view, "desactualizado") {
		t.Error("View() claims stale curve data although none was ever fetched")
	}
	if !strings.Contains(view, "Oficial") {
		t.Error("a treasury failure blanked the dolar section")
	}
}

func TestViewShowsTreasuryLoadingBeforeFirstData(t *testing.T) {
	m := newTestModel(okQuotes, okRiesgo)
	if view := m.renderTreasury(); !strings.Contains(view, "cargando") {
		t.Errorf("renderTreasury() = %q, want a loading placeholder before the first fetch", view)
	}
	if view := m.View(); !strings.Contains(view, "Curva del Tesoro") {
		t.Error("View() must show the section header while the first fetch is in flight")
	}
}

func TestRefreshFetchesTheCurveOncePerBusinessDay(t *testing.T) {
	// The dashboard never de-duplicates by itself: it asks the injected fetcher
	// on every cycle and the publication cache decides whether that costs a
	// request. The clock moves across a real release boundary, so this fails if
	// the schedule degrades into a fixed TTL — a TTL longer than the window
	// would cost one request, and no schedule at all would cost four.
	//
	// 2026-09-21 is a Monday and its release is 16:00 EDT, which is 20:00 UTC.
	cycles := []struct {
		at       time.Time
		requests int
	}{
		{time.Date(2026, 9, 21, 18, 0, 0, 0, time.UTC), 1},  // 14:00 EDT, cold cache
		{time.Date(2026, 9, 21, 19, 59, 0, 0, time.UTC), 1}, // 15:59 EDT, before the release
		{time.Date(2026, 9, 21, 20, 1, 0, 0, time.UTC), 2},  // 16:01 EDT, past the release
		{time.Date(2026, 9, 21, 20, 30, 0, 0, time.UTC), 2}, // 16:30 EDT, served from the cache
	}

	clock := cycles[0].at
	requests := 0
	cache := tesoro.NewPublisherCache(func(context.Context) (tesoro.Curve, error) {
		requests++
		return okCurve(), nil
	})
	cache.Now = func() time.Time { return clock }

	m := newTestModel(okQuotes, okRiesgo)
	m.fetchTreasury = cache.Get

	for i, cycle := range cycles {
		clock = cycle.at
		msg, ok := m.refresh(false)().(dataMsg)
		if !ok {
			t.Fatal("refresh() command did not produce a dataMsg")
		}
		if msg.treasuryErr != nil {
			t.Fatalf("cycle %d at %s: treasuryErr = %v", i, cycle.at.Format(time.RFC3339), msg.treasuryErr)
		}
		if len(msg.curve.Points) != len(tesoro.Tenors()) {
			t.Fatalf("cycle %d at %s: curve points = %d, want %d", i, cycle.at.Format(time.RFC3339), len(msg.curve.Points), len(tesoro.Tenors()))
		}
		if requests != cycle.requests {
			t.Errorf("cycle %d at %s: source requests = %d, want %d", i, cycle.at.Format(time.RFC3339), requests, cycle.requests)
		}
	}
}

func TestRefreshReportsTreasuryTimeoutLikeEveryOtherSource(t *testing.T) {
	slow := func(ctx context.Context, _ bool) (tesoro.Curve, error) {
		<-time.After(50 * time.Millisecond)
		return okCurve(), nil
	}
	m := newTestModel(okQuotes, okRiesgo)
	m.fetchTreasury = slow
	m.timeout = 10 * time.Millisecond

	msg, ok := m.refresh(false)().(dataMsg)
	if !ok {
		t.Fatal("refresh() command did not produce a dataMsg")
	}
	if msg.treasuryErr == nil {
		t.Fatal("treasuryErr = nil, want an explicit timeout for the slow source")
	}
	if !errors.Is(msg.treasuryErr, context.DeadlineExceeded) {
		t.Errorf("treasuryErr = %v, want it to wrap context.DeadlineExceeded", msg.treasuryErr)
	}
	if msg.quotesErr != nil {
		t.Errorf("quotesErr = %v, want the fast source to survive", msg.quotesErr)
	}
}

func TestRefreshCommandFetchesFedRate(t *testing.T) {
	m := newTestModel(okQuotes, okRiesgo)
	var forced []bool
	m.fetchFed = func(_ context.Context, force bool) (fed.Rate, error) {
		forced = append(forced, force)
		return okFed(), nil
	}

	msg, ok := m.refresh(false)().(dataMsg)
	if !ok {
		t.Fatal("refresh() command did not produce a dataMsg")
	}
	if msg.fedErr != nil {
		t.Fatalf("fedErr = %v, want nil", msg.fedErr)
	}
	if msg.rate.Effective != 3.88 || msg.rate.TargetLow != 3.75 || msg.rate.TargetHigh != 4.00 {
		t.Errorf("rate = %+v, want the fetched reference rate", msg.rate)
	}
	if len(forced) != 1 || forced[0] {
		t.Errorf("force intents = %v, want one non-forced call", forced)
	}
}

func TestRefreshRecordsTheFedForceIntent(t *testing.T) {
	// Like the curve, the Fed cache invalidates on `r` only. The fetcher
	// signature carries the intent, so the model needs no extra flag for it.
	var forced []bool
	m := newTestModel(okQuotes, okRiesgo)
	m.fetchFed = func(_ context.Context, force bool) (fed.Rate, error) {
		forced = append(forced, force)
		return okFed(), nil
	}
	m.interval = time.Millisecond

	if _, tick := m.Update(tickMsg(time.Now())); tick != nil {
		runCmds(tick)
	}
	rKey := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}}
	if _, press := m.Update(rKey); press != nil {
		runCmds(press)
	}

	if len(forced) != 2 {
		t.Fatalf("fed fetcher called %d times, want 2", len(forced))
	}
	if forced[0] {
		t.Error("tick asked for a forced fed refetch, want the cached rate")
	}
	if !forced[1] {
		t.Error("the r key did not ask for a forced fed refetch")
	}
}

func TestRefreshReportsFedTimeoutLikeEveryOtherSource(t *testing.T) {
	slow := func(ctx context.Context, _ bool) (fed.Rate, error) {
		<-time.After(50 * time.Millisecond)
		return okFed(), nil
	}
	m := newTestModel(okQuotes, okRiesgo)
	m.fetchFed = slow
	m.timeout = 10 * time.Millisecond

	msg, ok := m.refresh(false)().(dataMsg)
	if !ok {
		t.Fatal("refresh() command did not produce a dataMsg")
	}
	if msg.fedErr == nil {
		t.Fatal("fedErr = nil, want an explicit timeout for the slow source")
	}
	if !errors.Is(msg.fedErr, context.DeadlineExceeded) {
		t.Errorf("fedErr = %v, want it to wrap context.DeadlineExceeded", msg.fedErr)
	}
	if msg.quotesErr != nil {
		t.Errorf("quotesErr = %v, want the fast source to survive", msg.quotesErr)
	}
}

func TestUpdateStoresFedRate(t *testing.T) {
	m := newTestModel(okQuotes, okRiesgo)
	m.fedErr = errors.New("previous failure")

	updated, _ := m.Update(dataMsg{curve: okCurve(), rate: okFed(), fetchedAt: staleAt})
	m = updated.(Model)

	if m.fedErr != nil {
		t.Errorf("fedErr = %v, want nil after a successful fetch", m.fedErr)
	}
	if m.rate.Effective != 3.88 {
		t.Errorf("rate.Effective = %v, want 3.88", m.rate.Effective)
	}
	if !m.fedAt.Equal(staleAt) {
		t.Errorf("fedAt = %v, want %v", m.fedAt, staleAt)
	}
}

func TestUpdateKeepsFedFailureIsolated(t *testing.T) {
	boom := errors.New("fed down")

	t.Run("fed fails, the curve and quotes survive", func(t *testing.T) {
		m := newTestModel(okQuotes, okRiesgo)
		quotes, _ := okQuotes(context.Background())
		updated, _ := m.Update(dataMsg{
			quotes:    quotes,
			curve:     okCurve(),
			fedErr:    boom,
			fetchedAt: staleAt,
		})
		m = updated.(Model)

		if !errors.Is(m.fedErr, boom) {
			t.Errorf("fedErr = %v, want %v", m.fedErr, boom)
		}
		if len(m.curve.Points) != len(tesoro.Tenors()) {
			t.Error("a fed failure blanked the treasury curve")
		}
		if len(m.quotes) != 4 {
			t.Error("a fed failure blanked the dollar section")
		}
	})

	t.Run("the curve and quotes fail, fed survives", func(t *testing.T) {
		m := newTestModel(okQuotes, okRiesgo)
		updated, _ := m.Update(dataMsg{
			rate:        okFed(),
			quotesErr:   boom,
			treasuryErr: boom,
			fetchedAt:   staleAt,
		})
		m = updated.(Model)

		if m.fedErr != nil {
			t.Errorf("fedErr = %v, want nil", m.fedErr)
		}
		if m.rate.Effective != 3.88 {
			t.Error("the fed rate was dropped when the other sources failed")
		}
		if !m.fedAt.Equal(staleAt) {
			t.Errorf("fedAt = %v, want %v", m.fedAt, staleAt)
		}
	})

	t.Run("the curve never loaded, fed still renders", func(t *testing.T) {
		// A curve that failed before its first success must not hide a rate
		// that did load: the two sources are independent.
		m := newTestModel(okQuotes, okRiesgo)
		updated, _ := m.Update(dataMsg{rate: okFed(), treasuryErr: errors.New("treasury down"), fetchedAt: staleAt})
		m = updated.(Model)

		view := m.View()
		for _, want := range []string{"no disponible", "Tasa FED", "3,75%", "3,88%"} {
			if !strings.Contains(view, want) {
				t.Errorf("View() missing %q although the fed rate loaded:\n%s", want, view)
			}
		}
	})
}

func TestViewRendersFedLine(t *testing.T) {
	m := newTestModel(okQuotes, okRiesgo)
	updated, _ := m.Update(dataMsg{curve: okCurve(), rate: okFed(), fetchedAt: staleAt})
	m = updated.(Model)
	view := m.View()

	for _, want := range []string{"Tasa FED", "3,75% – 4,00%", "efectiva", "3,88%", "▲ +25 pb", "21-09-2026"} {
		if !strings.Contains(view, want) {
			t.Errorf("View() is missing %q for the fed line:\n%s", want, view)
		}
	}
}

func TestViewRendersFedInEsARFormat(t *testing.T) {
	// The fed line reuses the dashboard's number formatting: comma decimals,
	// never a dot decimal separator.
	m := newTestModel(okQuotes, okRiesgo)
	updated, _ := m.Update(dataMsg{curve: okCurve(), rate: okFed(), fetchedAt: staleAt})
	line := updated.(Model).renderFed()

	if strings.Contains(line, "3.75") || strings.Contains(line, "4.00") || strings.Contains(line, "3.88") {
		t.Errorf("fed line uses a dot decimal separator: %q", line)
	}
	for _, want := range []string{"3,75%", "4,00%", "3,88%"} {
		if !strings.Contains(line, want) {
			t.Errorf("fed line is missing the es-AR value %q: %q", want, line)
		}
	}
}

func TestRenderFedDeltaDirections(t *testing.T) {
	cases := []struct {
		name    string
		delta   *float64
		want    string
		wantNot string
	}{
		{"rising rate", fedDelta(25), "▲ +25 pb", ""},
		{"falling rate", fedDelta(-25), "▼ -25 pb", ""},
		{"flat rate", fedDelta(0), "· 0 pb", ""},
		{"unknown change", nil, "—", "pb"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			m := newTestModel(okQuotes, okRiesgo)
			m.loaded = true
			m.fedAt = staleAt
			m.rate = okFed()
			m.rate.EffectiveDeltaBp = tt.delta

			line := m.renderFed()
			if !strings.Contains(line, tt.want) {
				t.Errorf("renderFed() = %q, want it to contain %q", line, tt.want)
			}
			if tt.wantNot != "" && strings.Contains(line, tt.wantNot) {
				t.Errorf("renderFed() = %q, want no %q for an unknown change", line, tt.wantNot)
			}
		})
	}
}

func TestRenderFedKeepsTheDeltaOnTheYieldConvention(t *testing.T) {
	// The EFFR change follows the same convention as every yield: a rising rate
	// is red and a falling one green. The assertion is on the observable
	// foreground colour, so a fed-specific style that drifted would fail it.
	if got := curveDeltaStyle(25).GetForeground(); got != lipgloss.TerminalColor(lipgloss.Color("9")) {
		t.Errorf("rising fed change foreground = %v, want red 9", got)
	}
	if got := curveDeltaStyle(-25).GetForeground(); got != lipgloss.TerminalColor(lipgloss.Color("10")) {
		t.Errorf("falling fed change foreground = %v, want green 10", got)
	}
}

func TestViewRendersStaleFedRate(t *testing.T) {
	m := newTestModel(okQuotes, okRiesgo)
	updated, _ := m.Update(dataMsg{curve: okCurve(), rate: okFed(), fetchedAt: staleAt})
	m = updated.(Model)
	m.fedErr = errors.New("fed timeout")

	view := m.View()
	for _, want := range []string{"3,75%", "3,88%", "21-09-2026", "fed timeout", "10:30:00", "desactualizado"} {
		if !strings.Contains(view, want) {
			t.Errorf("View() is missing %q for the stale fed rate:\n%s", want, view)
		}
	}
	// The curve is unaffected by the fed failure and stays on screen.
	if !strings.Contains(view, "Spread 10Y-2Y: +20 pb") {
		t.Errorf("View() lost the curve when the fed refresh failed:\n%s", view)
	}
	if lines := strings.Split(m.renderFed(), "\n"); len(lines) != 2 {
		t.Errorf("renderFed() returned %d lines for stale data, want value + note:\n%s", len(lines), m.renderFed())
	}
}

func TestRenderFedShowsLoadingAndEmptyStates(t *testing.T) {
	m := newTestModel(okQuotes, okRiesgo)
	if got := m.renderFed(); !strings.Contains(got, "cargando") {
		t.Errorf("renderFed() = %q, want a loading placeholder before the first fetch", got)
	}

	m.loaded = true
	if got := m.renderFed(); !strings.Contains(got, "sin datos") {
		t.Errorf("renderFed() = %q, want the empty state after a loaded cycle with no rate", got)
	}
}

func TestViewFedUnavailableWithoutData(t *testing.T) {
	m := newTestModel(okQuotes, okRiesgo)
	m.loaded = true
	m.fedErr = errors.New("fed timeout")
	m.curve = okCurve()
	m.treasuryAt = staleAt

	view := m.View()
	if !strings.Contains(view, "fed timeout") {
		t.Errorf("View() is missing the fed error:\n%s", view)
	}
	if strings.Contains(view, "desactualizado") && !strings.Contains(view, "últimos datos") {
		t.Error("View() renders a malformed stale note")
	}
	if !strings.Contains(view, "Spread 10Y-2Y") {
		t.Error("a fed failure blanked the curve")
	}
}
