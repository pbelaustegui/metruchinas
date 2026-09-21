// Package tui renders the macroeconomic indicator dashboard: USD/ARS quotes
// and the Argentine country-risk index.
//
// The model is a plain Bubbletea model with injected fetchers, so state
// transitions can be tested by calling Update directly with messages.
package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"metruchinas/internal/bonos"
	"metruchinas/internal/dolar"
	"metruchinas/internal/riesgo"
)

// DefaultRefreshInterval is how often the dashboard refetches both sources.
const DefaultRefreshInterval = 30 * time.Second

// fetchTimeout bounds a single refresh cycle covering both sources.
const fetchTimeout = 25 * time.Second

// displayedHouses is the ordered list of dolarapi houses shown by the
// dashboard, from the most referenced to the least.
var displayedHouses = []string{"oficial", "blue", "bolsa", "contadoconliqui"}

// bondTickers is the list of sovereign bonds shown by the dashboard.
var bondTickers = []string{"GD29", "GD30", "GD35", "GD38", "GD41", "GD46"}

// QuotesFetcher retrieves the USD quotes. It matches dolar.Fetch.
type QuotesFetcher func(ctx context.Context) ([]dolar.Quote, error)

// RiesgoFetcher retrieves the country-risk indicator. It matches riesgo.Fetch.
type RiesgoFetcher func(ctx context.Context) (riesgo.Indicator, error)

// BondsFetcher retrieves the GD bond quotes. It matches a closure wrapping bonos.Client.Fetch.
type BondsFetcher func(ctx context.Context) ([]bonos.BondQuote, error)

// dataMsg carries the outcome of one refresh cycle. Each source reports its
// own error so a failing source never hides the other one's data.
type dataMsg struct {
	quotes    []dolar.Quote
	quotesErr error
	riesgo    riesgo.Indicator
	riesgoErr error
	bonds     []bonos.BondQuote
	bondsErr  error
	fetchedAt time.Time
}

// tickMsg triggers a periodic refresh.
type tickMsg time.Time

// Model is the dashboard state.
type Model struct {
	quotes      []dolar.Quote
	quotesErr   error
	riesgo      riesgo.Indicator
	riesgoErr   error
	bonds       []bonos.BondQuote
	bondsErr    error
	fetchedAt   time.Time
	refreshing  bool
	fetchQuotes QuotesFetcher
	fetchRiesgo RiesgoFetcher
	fetchBonds  BondsFetcher
	interval    time.Duration
	timeout     time.Duration
	loaded      bool
}

// New returns a Model wired to the real public APIs.
func New() Model {
	dc := dolar.NewClient()
	rc := riesgo.NewClient()
	bc := bonos.NewClient()
	return Model{
		fetchQuotes: dc.Fetch,
		fetchRiesgo: rc.Fetch,
		fetchBonds: func(ctx context.Context) ([]bonos.BondQuote, error) {
			return bc.Fetch(ctx, bondTickers...)
		},
		interval:   DefaultRefreshInterval,
		timeout:    fetchTimeout,
		refreshing: true,
	}
}

// Init starts the first fetch and the refresh heartbeat.
func (m Model) Init() tea.Cmd {
	return tea.Batch(m.refresh(), tickCmd(m.interval))
}

// Update handles keys, ticks and fetched data. It never blocks: the network
// work happens inside the returned command.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c", "esc":
			return m, tea.Quit
		case "r":
			if !m.refreshing {
				m.refreshing = true
				return m, m.refresh()
			}
		}
		return m, nil

	case tickMsg:
		// The heartbeat reschedules itself even while a fetch is in flight.
		cmds := []tea.Cmd{tickCmd(m.interval)}
		if !m.refreshing {
			m.refreshing = true
			cmds = append(cmds, m.refresh())
		}
		return m, tea.Batch(cmds...)

	case dataMsg:
		m.refreshing = false
		m.loaded = true
		m.fetchedAt = msg.fetchedAt
		if msg.quotesErr != nil {
			m.quotesErr = msg.quotesErr
		} else {
			m.quotes, m.quotesErr = msg.quotes, nil
		}
		if msg.riesgoErr != nil {
			m.riesgoErr = msg.riesgoErr
		} else {
			m.riesgo, m.riesgoErr = msg.riesgo, nil
		}
		if msg.bondsErr != nil {
			m.bondsErr = msg.bondsErr
		} else {
			m.bonds, m.bondsErr = msg.bonds, nil
		}
		return m, nil
	}
	return m, nil
}

// refresh fetches all sources and reports them in a single message, so one
// failing source never discards the other one's data.
func (m Model) refresh() tea.Cmd {
	return func() tea.Msg {
		timeout := m.timeout
		if timeout <= 0 {
			timeout = fetchTimeout
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()

		msg := dataMsg{fetchedAt: time.Now()}
		quotes, err := m.fetchQuotes(ctx)
		if err == nil {
			err = timeoutErr(ctx, "dolar", timeout)
		}
		if err != nil {
			msg.quotesErr = err
		} else {
			msg.quotes = quotes
		}
		indicator, err := m.fetchRiesgo(ctx)
		if err == nil {
			err = timeoutErr(ctx, "riesgo", timeout)
		}
		if err != nil {
			msg.riesgoErr = err
		} else {
			msg.riesgo = indicator
		}
		bondQuotes, err := m.fetchBonds(ctx)
		if err == nil {
			err = timeoutErr(ctx, "bonos", timeout)
		}
		if err != nil {
			msg.bondsErr = err
		} else {
			msg.bonds = bondQuotes
		}
		return msg
	}
}

// timeoutErr converts an expired refresh context into an explicit per-source
// failure. Both clients run their request under this context, but a response
// already buffered when the deadline passes can return without an error, so
// the cycle reports the expiry instead of silently keeping stale data.
func timeoutErr(ctx context.Context, source string, timeout time.Duration) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("tui: %s refresh did not complete within %s: %w", source, timeout, err)
	}
	return nil
}

// View renders the dashboard.
func (m Model) View() string {
	var b strings.Builder

	b.WriteString(titleStyle.Render("Metruchinas · indicadores macro"))
	b.WriteString("\n\n")
	b.WriteString(sectionStyle.Render("Dólar (ARS por USD)"))
	b.WriteString("\n")
	b.WriteString(m.renderQuotes())
	b.WriteString("\n\n")
	b.WriteString(sectionStyle.Render("Riesgo país (EMBI Argentina)"))
	b.WriteString("\n")
	b.WriteString(m.renderRiesgo())
	b.WriteString("\n\n")
	b.WriteString(m.renderBonds())
	b.WriteString("\n\n")
	b.WriteString(m.renderFooter())
	b.WriteString("\n")

	return b.String()
}

func (m Model) renderQuotes() string {
	if m.quotesErr != nil {
		return errorStyle.Render(fmt.Sprintf("  no disponible: %v", m.quotesErr))
	}
	rows := displayRows(m.quotes)
	if len(rows) == 0 {
		if !m.loaded {
			return mutedStyle.Render("  cargando…")
		}
		return mutedStyle.Render("  sin cotizaciones para mostrar")
	}

	var b strings.Builder
	for i, r := range rows {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString("  ")
		b.WriteString(labelStyle.Render(padRight(r.nombre, 26)))
		b.WriteString(rateStyle.Render(r.compra))
		b.WriteString(" / ")
		b.WriteString(rateStyle.Render(r.venta))
	}
	return b.String()
}

func (m Model) renderRiesgo() string {
	if m.riesgoErr != nil {
		return errorStyle.Render(fmt.Sprintf("  no disponible: %v", m.riesgoErr))
	}
	if m.riesgo.Date.IsZero() {
		if !m.loaded {
			return mutedStyle.Render("  cargando…")
		}
		return mutedStyle.Render("  sin datos")
	}
	arrow := "▲"
	style := upStyle
	if m.riesgo.Variation < 0 {
		arrow = "▼"
		style = downStyle
	}
	var b strings.Builder
	b.WriteString("  ")
	b.WriteString(valueStyle.Render(formatNumber(m.riesgo.Value, 0)))
	b.WriteString("  ")
	b.WriteString(style.Render(fmt.Sprintf("%s %s%%", arrow, formatNumber(m.riesgo.Variation, 2))))
	b.WriteString(mutedStyle.Render(fmt.Sprintf("   (%s)", m.riesgo.Date.Format("02-01-2006"))))
	return b.String()
}

// renderBonds renders the sovereign bonds section with parity %.
func (m Model) renderBonds() string {
	var b strings.Builder
	b.WriteString(sectionStyle.Render("Bonos soberanos GD (paridad)"))
	b.WriteByte('\n')

	if m.bondsErr != nil {
		b.WriteString(errorStyle.Render("  " + m.bondsErr.Error()))
		b.WriteByte('\n')
		return b.String()
	}
	if len(m.bonds) == 0 {
		return b.String()
	}

	now := time.Now()
	for _, bq := range m.bonds {
		if bq.Err != nil {
			b.WriteString(fmt.Sprintf("  %s  %s\n",
				labelStyle.Render(padRight(bq.Ticker, 6)),
				errorStyle.Render(bq.Err.Error())))
			continue
		}

		// Price in USD as published by the source.
		priceUSD := bq.Ultimo
		usdStr := formatNumber(priceUSD, 2)

		// Compute parity.
		var parityStr string
		if priceUSD > 0 {
			parity, err := bonos.Parity(priceUSD, bq.Ticker, now)
			if err == nil {
				parityStr = fmt.Sprintf("%.1f%%", parity)
			} else {
				parityStr = "—"
			}
		} else {
			parityStr = "—"
		}

		// Compute technical value.
		var tvStr string
		tv, err := bonos.TechnicalValue(bq.Ticker, now)
		if err == nil {
			tvStr = formatNumber(tv, 2)
		} else {
			tvStr = "—"
		}

		// Variation arrow.
		var varStr string
		if bq.Variacion > 0 {
			varStr = upStyle.Render(fmt.Sprintf("▲ %.2f%%", bq.Variacion))
		} else if bq.Variacion < 0 {
			varStr = downStyle.Render(fmt.Sprintf("▼ %.2f%%", bq.Variacion))
		} else {
			varStr = mutedStyle.Render(fmt.Sprintf("  %.2f%%", bq.Variacion))
		}

		b.WriteString(fmt.Sprintf("  %s  %s  %s  %s  %s\n",
			labelStyle.Render(padRight(bq.Ticker, 6)),
			rateStyle.Render(padRight("P:"+parityStr, 12)),
			mutedStyle.Render(padRight("VT:"+tvStr, 12)),
			rateStyle.Render(padRight("USD "+usdStr, 12)),
			varStr))
	}
	return b.String()
}

func (m Model) renderFooter() string {
	status := "actualizado " + m.fetchedAt.Format("15:04:05")
	if m.refreshing {
		status = "actualizando…"
	} else if m.fetchedAt.IsZero() {
		status = "sin datos"
	}
	return mutedStyle.Render(fmt.Sprintf("%s · r refrescar · q salir", status))
}

// row is one rendered dolar quote.
type row struct {
	nombre string
	compra string
	venta  string
}

// displayRows filters the API quotes down to the displayed houses, in
// displayedHouses order, and formats their rates. Houses the API does not
// publish are skipped; unknown houses are ignored.
func displayRows(quotes []dolar.Quote) []row {
	byCasa := make(map[string]dolar.Quote, len(quotes))
	for _, q := range quotes {
		byCasa[q.Casa] = q
	}
	rows := make([]row, 0, len(displayedHouses))
	for _, casa := range displayedHouses {
		q, ok := byCasa[casa]
		if !ok {
			continue
		}
		rows = append(rows, row{
			nombre: q.Nombre,
			compra: formatRate(q.Compra),
			venta:  formatRate(q.Venta),
		})
	}
	return rows
}

// formatRate renders a published rate, or a dash when the API reports null.
func formatRate(v *float64) string {
	if v == nil {
		return "—"
	}
	return formatNumber(*v, 2)
}

// formatNumber renders v with a comma decimal separator and dot thousands
// separators (es-AR convention), e.g. 1485.5 with 2 decimals is "1.485,50".
func formatNumber(v float64, decimals int) string {
	s := fmt.Sprintf("%.*f", decimals, v)
	sign := ""
	if strings.HasPrefix(s, "-") {
		sign, s = "-", s[1:]
	}
	intPart, fracPart, _ := strings.Cut(s, ".")

	var b strings.Builder
	for i, digit := range intPart {
		if i > 0 && (len(intPart)-i)%3 == 0 {
			b.WriteByte('.')
		}
		b.WriteRune(digit)
	}
	out := sign + b.String()
	if decimals > 0 {
		out += "," + fracPart
	}
	return out
}

// padRight pads s with spaces to at least width display cells. It measures
// display width, not bytes, so accented labels ("liquidación") line up with
// plain ones; a byte count would over-pad them by one cell per multibyte rune.
func padRight(s string, width int) string {
	if w := lipgloss.Width(s); w < width {
		return s + strings.Repeat(" ", width-w)
	}
	return s
}

func tickCmd(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(t time.Time) tea.Msg { return tickMsg(t) })
}

var (
	titleStyle   = lipgloss.NewStyle().Bold(true)
	sectionStyle = lipgloss.NewStyle().Bold(true).Underline(true)
	labelStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	rateStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("15"))
	valueStyle   = lipgloss.NewStyle().Bold(true)
	mutedStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	errorStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	upStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	downStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
)
