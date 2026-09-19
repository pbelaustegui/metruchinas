package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"metruchinas/internal/dolar"
	"metruchinas/internal/riesgo"
)

func rate(v float64) *float64 { return &v }

// newTestModel returns a Model with injected fetchers and no pending refresh,
// so tests exercise one message at a time.
func newTestModel(quotes QuotesFetcher, r RiesgoFetcher) Model {
	m := New()
	m.fetchQuotes = quotes
	m.fetchRiesgo = r
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
	if !m.fetchedAt.Equal(at) {
		t.Errorf("fetchedAt = %v, want %v", m.fetchedAt, at)
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

			msg, ok := m.refresh()().(dataMsg)
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

	msg, ok := m.refresh()().(dataMsg)
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

	msg, ok := m.refresh()().(dataMsg)
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

	msg, ok := m.refresh()().(dataMsg)
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
