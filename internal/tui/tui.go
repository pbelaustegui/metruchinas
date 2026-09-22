// Package tui renders the macroeconomic indicator dashboard: USD/ARS quotes,
// the Argentine country-risk index, the sovereign GD bond parity table, and the
// US Treasury par yield curve.
//
// The model is a plain Bubbletea model with injected fetchers, so state
// transitions can be tested by calling Update directly with messages.
package tui

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"metruchinas/internal/bonos"
	"metruchinas/internal/dolar"
	"metruchinas/internal/riesgo"
	"metruchinas/internal/tesoro"
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

// TreasuryFetcher retrieves the US Treasury par yield curve. It matches
// tesoro.Cache.Get: force asks for a refetch instead of the cached curve, which
// is how the `r` key reaches the publication cache. On every other cycle the
// cache decides whether the request is needed at all.
type TreasuryFetcher func(ctx context.Context, force bool) (tesoro.Curve, error)

// dataMsg carries the outcome of one refresh cycle. Each source reports its
// own error so a failing source never hides the other one's data.
type dataMsg struct {
	quotes      []dolar.Quote
	quotesErr   error
	riesgo      riesgo.Indicator
	riesgoErr   error
	bonds       []bonos.BondQuote
	bondsErr    error
	curve       tesoro.Curve
	treasuryErr error
	fetchedAt   time.Time
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
	curve       tesoro.Curve
	treasuryErr error
	// quotesAt, riesgoAt, bondsAt and treasuryAt are the wall-clock times of the
	// last successful fetch per source. They power stale rendering: a failing
	// refresh keeps the last good data on screen and says how old it is.
	quotesAt      time.Time
	riesgoAt      time.Time
	bondsAt       time.Time
	treasuryAt    time.Time
	refreshing    bool
	fetchQuotes   QuotesFetcher
	fetchRiesgo   RiesgoFetcher
	fetchBonds    BondsFetcher
	fetchTreasury TreasuryFetcher
	interval      time.Duration
	timeout       time.Duration
	loaded        bool
}

// New returns a Model wired to the real public APIs.
func New() Model {
	dc := dolar.NewClient()
	rc := riesgo.NewClient()
	bc := bonos.NewClient()
	// Treasury publishes once per business day, so the curve is wrapped in a
	// publication cache: without it the 30-second heartbeat would re-download
	// the same year of CSV all day long.
	curveCache := tesoro.NewPublisherCache(tesoro.NewClient().Fetch)
	return Model{
		fetchQuotes: dc.Fetch,
		fetchRiesgo: rc.Fetch,
		fetchBonds: func(ctx context.Context) ([]bonos.BondQuote, error) {
			return bc.Fetch(ctx, bondTickers...)
		},
		fetchTreasury: curveCache.Get,
		interval:      DefaultRefreshInterval,
		timeout:       fetchTimeout,
		refreshing:    true,
	}
}

// Init starts the first fetch and the refresh heartbeat.
func (m Model) Init() tea.Cmd {
	return tea.Batch(m.refresh(false), tickCmd(m.interval))
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
				// Only `r` invalidates the Treasury publication cache, so the
				// intent travels with the refresh command instead of living as
				// extra model state.
				return m, m.refresh(true)
			}
		}
		return m, nil

	case tickMsg:
		// The heartbeat reschedules itself even while a fetch is in flight.
		cmds := []tea.Cmd{tickCmd(m.interval)}
		if !m.refreshing {
			m.refreshing = true
			cmds = append(cmds, m.refresh(false))
		}
		return m, tea.Batch(cmds...)

	case dataMsg:
		m.refreshing = false
		m.loaded = true
		if msg.quotesErr != nil {
			m.quotesErr = msg.quotesErr
		} else {
			m.quotes, m.quotesErr = msg.quotes, nil
			m.quotesAt = msg.fetchedAt
		}
		if msg.riesgoErr != nil {
			m.riesgoErr = msg.riesgoErr
		} else {
			m.riesgo, m.riesgoErr = msg.riesgo, nil
			m.riesgoAt = msg.fetchedAt
		}
		if msg.bondsErr != nil {
			m.bondsErr = msg.bondsErr
		} else {
			m.bonds, m.bondsErr = msg.bonds, nil
			m.bondsAt = msg.fetchedAt
		}
		if msg.treasuryErr != nil {
			m.treasuryErr = msg.treasuryErr
		} else {
			m.curve, m.treasuryErr = msg.curve, nil
			m.treasuryAt = msg.fetchedAt
		}
		return m, nil
	}
	return m, nil
}

// refresh fetches all sources and reports them in a single message, so one
// failing source never discards the other one's data. force is the user intent
// behind `r` and reaches the sources that cache their answer.
func (m Model) refresh(force bool) tea.Cmd {
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
		curve, err := m.fetchTreasury(ctx, force)
		if err == nil {
			err = timeoutErr(ctx, "tesoro", timeout)
		}
		if err != nil {
			msg.treasuryErr = err
		} else {
			msg.curve = curve
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
	b.WriteString(m.renderTreasury())
	b.WriteString("\n\n")
	b.WriteString(m.renderBonds())
	b.WriteString("\n\n")
	b.WriteString(m.renderFooter())
	b.WriteString("\n")

	return b.String()
}

func (m Model) renderQuotes() string {
	rows := displayRows(m.quotes)
	if m.quotesErr != nil {
		// Keep showing the last good quotes, flagged as stale with the
		// error and the time they were successfully fetched.
		if len(rows) > 0 {
			return renderQuoteRows(rows) + "\n" + staleNote(m.quotesAt, m.quotesErr)
		}
		return errorStyle.Render(fmt.Sprintf("  no disponible: %v", m.quotesErr))
	}
	if len(rows) == 0 {
		if !m.loaded {
			return mutedStyle.Render("  cargando…")
		}
		return mutedStyle.Render("  sin cotizaciones para mostrar")
	}
	return renderQuoteRows(rows)
}

// renderQuoteRows formats one quote per line: house name and buy/sell rates.
func renderQuoteRows(rows []row) string {
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

// staleNote renders the stale-data warning shown under a section whose last
// refresh failed: the error plus the time the displayed data was fetched.
func staleNote(at time.Time, err error) string {
	return errorStyle.Render(fmt.Sprintf("  ⚠ desactualizado · últimos datos %s · %v", at.Format("15:04:05"), err))
}

func (m Model) renderRiesgo() string {
	if m.riesgoErr != nil {
		// Keep showing the last good indicator, flagged as stale. The gate is
		// the success stamp, not riesgo.Date: the source may omit the date.
		if !m.riesgoAt.IsZero() {
			return m.renderRiesgoValue() + "\n" + staleNote(m.riesgoAt, m.riesgoErr)
		}
		return errorStyle.Render(fmt.Sprintf("  no disponible: %v", m.riesgoErr))
	}
	// The gate is the success stamp, not riesgo.Date: the source may omit
	// the publication date.
	if m.riesgoAt.IsZero() {
		if !m.loaded {
			return mutedStyle.Render("  cargando…")
		}
		return mutedStyle.Render("  sin datos")
	}
	return m.renderRiesgoValue()
}

// renderRiesgoValue formats the indicator value, daily variation and date.
func (m Model) renderRiesgoValue() string {
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
	if !m.riesgo.Date.IsZero() {
		b.WriteString(mutedStyle.Render(fmt.Sprintf("   (%s)", m.riesgo.Date.Format("02-01-2006"))))
	}
	return b.String()
}

// treasurySectionTitle is the curve section header.
const treasurySectionTitle = "Curva del Tesoro EE. UU."

const (
	// curveYieldWidth and curveDeltaWidth are the fixed display widths of the
	// two value columns. They are sized for a double-digit level and a
	// double-digit change so a wide value never shifts the columns.
	curveYieldWidth = 5
	curveDeltaWidth = 4
	// curveColumnGap separates the two curve columns.
	curveColumnGap = "   "
)

// renderTreasury renders the US Treasury par yield curve: the publication date
// in the section header, two columns of seven tenors carrying each level and
// its daily change in basis points, and the 10Y-2Y spread underneath.
//
// Two columns keep the section inside nine lines. A single column of fourteen
// rows would add eight more lines to a dashboard that already runs past an
// 80x24 terminal.
func (m Model) renderTreasury() string {
	var b strings.Builder
	b.WriteString(sectionStyle.Render(treasurySectionTitle))
	if !m.curve.Date.IsZero() {
		b.WriteString(mutedStyle.Render(fmt.Sprintf(" · %s · Δ diario (pb)", m.curve.Date.Format("02-01-2006"))))
	}
	b.WriteByte('\n')

	if m.treasuryErr != nil && m.treasuryAt.IsZero() {
		b.WriteString(errorStyle.Render("  no disponible: " + m.treasuryErr.Error()))
		return b.String()
	}
	if m.treasuryAt.IsZero() {
		if !m.loaded {
			b.WriteString(mutedStyle.Render("  cargando…"))
			return b.String()
		}
		b.WriteString(mutedStyle.Render("  sin datos"))
		return b.String()
	}

	b.WriteString(m.renderCurveRows())
	b.WriteByte('\n')
	b.WriteString(m.renderSpread())
	if m.treasuryErr != nil {
		// Keep showing the last good curve, flagged as stale, like every other
		// section does.
		b.WriteByte('\n')
		b.WriteString(staleNote(m.treasuryAt, m.treasuryErr))
	}
	return b.String()
}

// renderCurveRows renders the curve as two columns of seven rows in ascending
// maturity order. A tenor Treasury did not publish keeps its slot and renders
// as a dash, so a retired or brand-new column can never shift its neighbours.
func (m Model) renderCurveRows() string {
	tenors := tesoro.Tenors()
	rowsPerColumn := (len(tenors) + 1) / 2
	labelWidth := curveLabelWidth(tenors)

	published := make(map[string]tesoro.Point, len(m.curve.Points))
	for _, point := range m.curve.Points {
		published[point.Tenor] = point
	}

	var b strings.Builder
	for i := 0; i < rowsPerColumn; i++ {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString("  ")
		left := tenors[i]
		leftPoint, leftOK := published[left.Key]
		b.WriteString(curveCell(left.Label, leftPoint, leftOK, labelWidth))

		rightIndex := i + rowsPerColumn
		if rightIndex >= len(tenors) {
			break
		}
		b.WriteString(curveColumnGap)
		right := tenors[rightIndex]
		rightPoint, rightOK := published[right.Key]
		b.WriteString(curveCell(right.Label, rightPoint, rightOK, labelWidth))
	}
	return b.String()
}

// curveCell renders one curve cell: display label, level, and daily change. An
// unpublished tenor renders a dash in both value columns.
func curveCell(label string, point tesoro.Point, published bool, labelWidth int) string {
	var b strings.Builder
	b.WriteString(labelStyle.Render(padRight(label, labelWidth)))
	b.WriteString("  ")
	if !published {
		b.WriteString(mutedStyle.Render(padRight("—", curveYieldWidth)))
		b.WriteString("  ")
		b.WriteString(mutedStyle.Render(padRight("—", curveDeltaWidth)))
		return b.String()
	}
	b.WriteString(rateStyle.Render(padRight(formatNumber(point.Yield, 2), curveYieldWidth)))
	b.WriteString("  ")
	b.WriteString(curveDeltaCell(point.DeltaBp))
	return b.String()
}

// curveDeltaCell renders a daily change in basis points. The arrow carries the
// direction and the colour follows the yield convention (rising yields red,
// falling green); an unknown change is a dash and a flat one a muted dot.
//
// The change is rounded to whole basis points because it is derived from two
// yields the source publishes with two decimals: the difference is exact to the
// basis point, and rounding also keeps float noise from rendering as -0.
func curveDeltaCell(deltaBp *float64) string {
	if deltaBp == nil {
		return mutedStyle.Render(padRight("—", curveDeltaWidth))
	}
	bp := int(math.Round(*deltaBp))
	marker := "·"
	switch {
	case bp > 0:
		marker = "▲"
	case bp < 0:
		marker = "▼"
	}
	cell := fmt.Sprintf("%s %2s", marker, formatNumber(math.Abs(float64(bp)), 0))
	return curveDeltaStyle(bp).Render(padRight(cell, curveDeltaWidth))
}

// curveDeltaStyle returns the style for a daily yield change in basis points.
// Yields follow the market convention: a rise is red, a fall is green, and no
// change is muted rather than coloured.
func curveDeltaStyle(bp int) lipgloss.Style {
	switch {
	case bp > 0:
		return upStyle
	case bp < 0:
		return downStyle
	default:
		return mutedStyle
	}
}

// curveLabelWidth is the widest display label, so both curve columns start
// their value columns on the same display cell.
func curveLabelWidth(tenors []tesoro.Tenor) int {
	width := 0
	for _, tenor := range tenors {
		if w := lipgloss.Width(tenor.Label); w > width {
			width = w
		}
	}
	return width
}

// renderSpread renders the 10Y-2Y spread in basis points. A negative spread is
// the inverted-curve signal, so it is flagged explicitly instead of being
// coloured by the yield convention: here the sign is the news, not a market
// direction.
func (m Model) renderSpread() string {
	spread, ok := m.curve.SpreadBp("10Y", "2Y")
	if !ok {
		return mutedStyle.Render("  Spread 10Y-2Y: —")
	}
	bp := int(math.Round(spread))
	text := fmt.Sprintf("  Spread 10Y-2Y: %s pb", formatBp(bp))
	if bp < 0 {
		return lossStyle.Render(text + " · curva invertida")
	}
	return rateStyle.Render(text)
}

// formatBp renders a basis-point change with an explicit sign: +20, -45, 0.
// The digits themselves still come from formatNumber, so the dashboard keeps a
// single es-AR number rule.
func formatBp(bp int) string {
	sign := ""
	if bp > 0 {
		sign = "+"
	}
	return sign + formatNumber(float64(bp), 0)
}

// renderBonds renders the sovereign bonds section with parity %.
func (m Model) renderBonds() string {
	var b strings.Builder
	b.WriteString(sectionStyle.Render("Bonos soberanos GD (paridad)"))
	b.WriteByte('\n')

	if m.bondsErr != nil {
		// Keep showing the last good bond rows, flagged as stale. Separate
		// the note from the rows explicitly instead of relying on
		// renderBondRows ending with a newline.
		if len(m.bonds) > 0 {
			b.WriteString(strings.TrimSuffix(m.renderBondRows(), "\n"))
			b.WriteByte('\n')
			b.WriteString(staleNote(m.bondsAt, m.bondsErr))
			b.WriteByte('\n')
			return b.String()
		}
		b.WriteString(errorStyle.Render("  " + m.bondsErr.Error()))
		b.WriteByte('\n')
		return b.String()
	}
	if len(m.bonds) == 0 {
		return b.String()
	}

	b.WriteString(m.renderBondRows())
	return b.String()
}

// renderBondRows formats one row per bond: parity, technical value, USD price
// and daily variation.
func (m Model) renderBondRows() string {
	var b strings.Builder
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

		// Variation arrow. Bonds follow the market convention: green when the
		// price rises, red when it falls. (Country risk is the opposite: a
		// falling EMBI is good news, so riesgo swaps these styles.)
		var varStr string
		if bq.Variacion > 0 {
			varStr = variationStyle(bq.Variacion).Render(fmt.Sprintf("▲ %.2f%%", bq.Variacion))
		} else if bq.Variacion < 0 {
			varStr = variationStyle(bq.Variacion).Render(fmt.Sprintf("▼ %.2f%%", bq.Variacion))
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

// lastSuccessAt returns the most recent per-source success time, which is
// what "actualizado" in the footer should reflect: a fully failed cycle did
// not update anything.
func (m Model) lastSuccessAt() time.Time {
	latest := m.quotesAt
	if m.riesgoAt.After(latest) {
		latest = m.riesgoAt
	}
	if m.bondsAt.After(latest) {
		latest = m.bondsAt
	}
	if m.treasuryAt.After(latest) {
		latest = m.treasuryAt
	}
	return latest
}

// variationStyle returns the style for a bond price variation following the
// market convention: green when positive, red when negative.
func variationStyle(v float64) lipgloss.Style {
	if v < 0 {
		return lossStyle
	}
	return gainStyle
}

func (m Model) renderFooter() string {
	status := "actualizado " + m.lastSuccessAt().Format("15:04:05")
	if m.refreshing {
		status = "actualizando…"
	} else if m.lastSuccessAt().IsZero() {
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
	gainStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	lossStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
)
