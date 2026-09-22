// Package tesoro is a minimal client for the US Treasury par yield curve
// published by home.treasury.gov.
//
// The endpoint
//
//	GET https://home.treasury.gov/resource-center/data-chart-center/interest-rates/daily-treasury-rates.csv/{year}/all?type=daily_treasury_yield_curve&field_tdr_date_value={year}&page&_format=csv
//
// returns one calendar year of daily par yields as CSV: one row per business
// date in descending date order and one column per published tenor. No
// authentication is required and the payload is roughly 15 KB.
//
// Three properties of the payload shape this package:
//
//   - Columns are resolved by header name, never by position. Treasury inserted
//     the "1.5 Month" column into the middle of the header; an index-based
//     parser would silently report one tenor's yield as another's.
//   - The newest row is row 0 and the row before it is the previous business
//     day. The payload carries no variation field, so the daily change in basis
//     points is derived locally from those two rows.
//   - A year with no published data answers HTTP 200 with an empty body. That is
//     ErrNoData, not a malformed payload, and it is what makes the year-boundary
//     fallback necessary.
package tesoro

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"slices"
)

const (
	defaultBaseURL = "https://home.treasury.gov"
	// csvPath is the CSV endpoint path for one calendar year.
	csvPath = "/resource-center/data-chart-center/interest-rates/daily-treasury-rates.csv/%d/all"
	// csvQuery is the query Treasury serves the curve under. "page" is sent
	// bare, exactly as the probed endpoint URL spells it.
	csvQuery       = "type=daily_treasury_yield_curve&field_tdr_date_value=%d&page&_format=csv"
	defaultTimeout = 10 * time.Second

	// dateColumn is the name of the row date column.
	dateColumn = "Date"
	// csvDateLayout is the layout Treasury uses in the Date column: M/D/YYYY.
	csvDateLayout = "1/2/2006"
	// displayDateLayout is the layout the dashboard and error messages use for a
	// publication date: DD-MM-YYYY.
	displayDateLayout = "02-01-2006"
	// bpPerPercent converts a yield change in percentage points to basis points.
	bpPerPercent = 100
	// browserUA identifies the client as a browser. The endpoint throttles a
	// non-browser user agent: the same request sent as "Go-http-client/1.1"
	// takes 16 to 20 seconds and times out, while a browser user agent answers
	// in well under a second.
	browserUA = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/137.0.0.0 Safari/537.36"
	// byteOrderMark is stripped from the first header cell: a BOM is invisible
	// in a terminal but would hide the date column from the name lookup.
	byteOrderMark = "\ufeff"
)

// ErrMalformed reports a response body that is not the expected CSV payload or
// that carries an unparseable date or yield.
var ErrMalformed = errors.New("tesoro: malformed response payload")

// ErrNoData reports a well-formed response that publishes no rows. The endpoint
// answers HTTP 200 with an empty body for a year Treasury has not published.
var ErrNoData = errors.New("tesoro: no published data for the requested year")

// HTTPError reports a non-2xx response from the endpoint.
type HTTPError struct {
	// StatusCode is the numeric status code returned by the server.
	StatusCode int
	// Status is the full status line, e.g. "503 Service Unavailable".
	Status string
}

// Error implements the error interface.
func (e *HTTPError) Error() string {
	return fmt.Sprintf("tesoro: unexpected status %s", e.Status)
}

// Tenor identifies one published curve tenor: the Treasury CSV column header
// that keys it and the short label the dashboard displays.
type Tenor struct {
	// Key is the CSV column header, e.g. "10 Yr".
	Key string
	// Label is the display label, e.g. "10Y".
	Label string
}

// tenors is every tenor the curve tracks, in ascending maturity order, which is
// also the column order Treasury publishes. Key is the exact Treasury header, so
// a column is always resolved by the name the source publishes; Label is what
// the dashboard prints, and it follows the dashboard's es-AR rule of a comma
// decimal separator ("1.5 Month" is therefore displayed as "1,5M").
var tenors = []Tenor{
	{Key: "1 Mo", Label: "1M"},
	{Key: "1.5 Month", Label: "1,5M"},
	{Key: "2 Mo", Label: "2M"},
	{Key: "3 Mo", Label: "3M"},
	{Key: "4 Mo", Label: "4M"},
	{Key: "6 Mo", Label: "6M"},
	{Key: "1 Yr", Label: "1Y"},
	{Key: "2 Yr", Label: "2Y"},
	{Key: "3 Yr", Label: "3Y"},
	{Key: "5 Yr", Label: "5Y"},
	{Key: "7 Yr", Label: "7Y"},
	{Key: "10 Yr", Label: "10Y"},
	{Key: "20 Yr", Label: "20Y"},
	{Key: "30 Yr", Label: "30Y"},
}

// Tenors returns every tracked tenor in ascending maturity order. Treasury adds
// and retires columns over time, so a payload may publish only a subset of this
// list; a rendering layout iterates it and shows a dash for any tenor that is
// absent from Curve.Points.
func Tenors() []Tenor {
	out := make([]Tenor, len(tenors))
	copy(out, tenors)
	return out
}

// Point is one tenor of the curve.
type Point struct {
	// Tenor is the canonical key: the Treasury CSV column header, e.g. "10 Yr".
	Tenor string
	// Label is the display label, e.g. "10Y".
	Label string
	// Yield is the par yield in percent, e.g. 4.96.
	Yield float64
	// DeltaBp is the change against the previous business day in basis points,
	// e.g. 3 for a 0.03 percentage-point rise. It is nil when the previous
	// business day published no value for this tenor.
	DeltaBp *float64
}

// Curve is one Treasury business date of the par yield curve.
type Curve struct {
	// Date is the Treasury business date of the newest row. It carries a civil
	// date anchored to midnight in the system's local timezone and is never
	// converted, so the rendered date is the same whatever timezone the host
	// runs in.
	Date time.Time
	// Points holds the tenors published for Date, in ascending maturity order.
	// A tenor Treasury did not publish — a retired or newly added column, or a
	// blank cell — is absent instead of zero, so callers render a dash for it
	// and never a fake yield.
	Points []Point
}

// SpreadBp returns the spread between two tenors in basis points: the long
// yield minus the short yield, so the usual 10Y minus 2Y spread is
// SpreadBp("10Y", "2Y"). The ok result is false when either tenor is absent
// from the curve. Both the display label ("10Y") and the canonical column
// header ("10 Yr") are accepted.
func (c Curve) SpreadBp(long, short string) (float64, bool) {
	longPoint, ok := c.point(long)
	if !ok {
		return 0, false
	}
	shortPoint, ok := c.point(short)
	if !ok {
		return 0, false
	}
	return (longPoint.Yield - shortPoint.Yield) * bpPerPercent, true
}

// point resolves a tenor by its canonical key or by its display label.
func (c Curve) point(tenor string) (Point, bool) {
	for _, p := range c.Points {
		if p.Tenor == tenor || p.Label == tenor {
			return p, true
		}
	}
	return Point{}, false
}

// Client fetches the curve from home.treasury.gov.
//
// A zero-value Client is usable and resolves the same defaults as NewClient.
type Client struct {
	// HTTPClient performs the HTTP requests. It defaults to an http.Client
	// with a 10-second timeout that covers both connection (dial) and
	// response read.
	HTTPClient *http.Client
	// BaseURL is the endpoint base URL. It defaults to
	// https://home.treasury.gov.
	BaseURL string
	// Now returns the current time. It defaults to time.Now and is used only to
	// choose which year's CSV to request; it never affects parsing or display.
	Now func() time.Time
}

// NewClient returns a Client with the default timeout, base URL, and clock.
func NewClient() *Client {
	return &Client{
		HTTPClient: &http.Client{Timeout: defaultTimeout},
		BaseURL:    defaultBaseURL,
		Now:        time.Now,
	}
}

// Fetch returns the most recent published curve.
//
// It requests the current year's CSV and, when that file cannot carry a daily
// change on its own — it has no rows at all, or a single row that leaves the
// previous business day in the previous year's file — one extra request to the
// previous year. A healthy year therefore costs exactly one request.
//
// Error classes:
//   - *HTTPError for non-2xx responses, carrying the status code.
//   - an error wrapping ErrMalformed when the body is not the expected CSV or
//     carries an unparseable date or yield.
//   - an error wrapping ErrNoData when no requested year publishes rows.
//   - a wrapped error for network, read, or context failures, detectable with
//     errors.Is against context.Canceled / context.DeadlineExceeded.
func (c *Client) Fetch(ctx context.Context) (Curve, error) {
	year := c.now().Year()

	current, err := c.fetchYear(ctx, year)
	if err != nil && !errors.Is(err, ErrNoData) {
		return Curve{}, err
	}

	// The previous business day can live in the previous year's file: on the
	// first business day of a year the current file holds a single row, and the
	// first business day of January needs December's row to compute a delta.
	var previous []rawRow
	if len(current) < 2 {
		fallback, fallbackErr := c.fetchYear(ctx, year-1)
		switch {
		case fallbackErr == nil:
			previous = fallback
		case errors.Is(fallbackErr, ErrNoData):
			// The previous year published nothing either.
		case len(current) == 0:
			// Nothing can be rendered without the fallback year, so surface it
			// instead of reporting an empty year as ErrNoData.
			return Curve{}, fallbackErr
		default:
			// A broken fallback year must not hide the levels the current year
			// already published: the tenors lose their delta instead.
		}
	}

	rows := make([]rawRow, 0, len(current)+len(previous))
	rows = append(rows, current...)
	rows = append(rows, previous...)
	if len(rows) == 0 {
		return Curve{}, fmt.Errorf("%w: no rows in the current or previous year", ErrNoData)
	}
	// The newest row is defined by its date, not by its position in the file.
	slices.SortStableFunc(rows, func(a, b rawRow) int { return b.date.Compare(a.date) })

	curve := buildCurve(rows)
	if len(curve.Points) == 0 {
		// A date with no published tenor is not a curve. Returning it would
		// render a dated section of dashes with no error at all, which the user
		// cannot tell apart from a day Treasury published nothing.
		return Curve{}, fmt.Errorf("%w: the newest row (%s) publishes none of the %d tracked tenors",
			ErrMalformed, curve.Date.Format(displayDateLayout), len(tenors))
	}
	return curve, nil
}

// checkTenorColumns rejects the two header shapes that would otherwise degrade
// silently. A header carrying none of the tracked tenor columns means the
// source renamed them, and every row would parse as empty; a header carrying one
// of them twice means the value reported would depend on column order. Both are
// payloads the parser does not understand, so both are ErrMalformed — distinct
// from ErrNoData, which stays reserved for a body with no rows at all.
func checkTenorColumns(header []string, counts map[string]int) error {
	var matched int
	for _, tenor := range tenors {
		switch count := counts[tenor.Key]; {
		case count == 0:
		case count > 1:
			return fmt.Errorf("%w: column %q appears %d times in header %s",
				ErrMalformed, tenor.Key, count, headerSummary(header))
		default:
			matched++
		}
	}
	if matched == 0 {
		return fmt.Errorf("%w: none of the %d tracked tenor columns appear in header %s",
			ErrMalformed, len(tenors), headerSummary(header))
	}
	return nil
}

// headerSummary renders a bounded summary of a CSV header for error messages.
// The parsed body may be anything at all, so a wide or hostile header must not
// produce an unbounded diagnosis.
func headerSummary(header []string) string {
	const (
		maxCells    = 8
		maxCellRune = 24
	)
	shown := header
	if len(shown) > maxCells {
		shown = shown[:maxCells]
	}
	cells := make([]string, 0, len(shown))
	for _, cell := range shown {
		cells = append(cells, strconv.Quote(clipRunes(cell, maxCellRune)))
	}
	summary := strings.Join(cells, ", ")
	if len(header) > maxCells {
		summary += fmt.Sprintf(", … (%d columns)", len(header))
	}
	return summary
}

// clipRunes truncates s to at most max runes, marking the cut.
func clipRunes(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "…"
}

// Fetch returns the most recent published curve using a default client.
// See Client.Fetch.
func Fetch(ctx context.Context) (Curve, error) {
	return NewClient().Fetch(ctx)
}

// rawRow is one parsed CSV record: its business date plus the published yield
// per Treasury column header.
type rawRow struct {
	date   time.Time
	values map[string]float64
}

// fetchYear requests one calendar year's CSV and returns its rows in descending
// date order. A year Treasury has not published returns ErrNoData.
func (c *Client) fetchYear(ctx context.Context, year int) ([]rawRow, error) {
	body, err := c.get(ctx, year)
	if err != nil {
		return nil, err
	}
	return parseCSV(body)
}

func (c *Client) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *Client) get(ctx context.Context, year int) ([]byte, error) {
	httpClient := c.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultTimeout}
	}
	base := c.BaseURL
	if base == "" {
		base = defaultBaseURL
	}
	endpoint, err := url.JoinPath(base, fmt.Sprintf(csvPath, year))
	if err != nil {
		return nil, fmt.Errorf("tesoro: build URL: %w", err)
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("tesoro: build URL: %w", err)
	}
	parsed.RawQuery = fmt.Sprintf(csvQuery, year)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("tesoro: build request: %w", err)
	}
	req.Header.Set("User-Agent", browserUA)
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("tesoro: request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, &HTTPError{StatusCode: resp.StatusCode, Status: resp.Status}
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("tesoro: read response: %w", err)
	}
	return body, nil
}

// parseCSV parses one year of Treasury CSV into rows sorted by descending date.
//
// The column set is resolved by header name. The header is the only thing that
// identifies a tenor, so a reordered, extended, or trimmed header keeps every
// tenor mapped to its own column.
func parseCSV(body []byte) ([]rawRow, error) {
	reader := csv.NewReader(strings.NewReader(string(body)))
	// Treasury publishes the same number of cells on every row of a file, so a
	// record that disagrees with the header is a corrupt payload rather than a
	// row to trim.
	header, err := reader.Read()
	if err == io.EOF {
		// A year with no published data answers with an empty body.
		return nil, fmt.Errorf("%w: empty response body", ErrNoData)
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformed, err)
	}

	columns := make(map[string]int, len(header))
	counts := make(map[string]int, len(header))
	for i, name := range header {
		name = strings.TrimSpace(name)
		if i == 0 {
			name = strings.TrimPrefix(name, byteOrderMark)
		}
		columns[name] = i
		counts[name]++
	}
	dateIdx, ok := columns[dateColumn]
	if !ok {
		return nil, fmt.Errorf("%w: no %q column in header %s", ErrMalformed, dateColumn, headerSummary(header))
	}
	if err := checkTenorColumns(header, counts); err != nil {
		return nil, err
	}

	var rows []rawRow
	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrMalformed, err)
		}
		row, err := parseRecord(record, dateIdx, columns)
		if err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("%w: header without data rows", ErrNoData)
	}
	slices.SortStableFunc(rows, func(a, b rawRow) int { return b.date.Compare(a.date) })
	return rows, nil
}

// parseRecord converts one CSV record into a raw row. Tenors that are absent
// from the header, absent from this record, or blank in this record are left
// out of the row: Treasury publishes a blank cell for a tenor it did not quote
// that day, and a blank cell is not a zero yield. A cell that carries a
// non-numeric value instead is a payload the parser does not understand.
func parseRecord(record []string, dateIdx int, columns map[string]int) (rawRow, error) {
	if dateIdx >= len(record) {
		return rawRow{}, fmt.Errorf("%w: record %q has no date cell", ErrMalformed, strings.Join(record, ","))
	}
	date, err := time.Parse(csvDateLayout, strings.TrimSpace(record[dateIdx]))
	if err != nil {
		return rawRow{}, fmt.Errorf("%w: invalid date %q", ErrMalformed, record[dateIdx])
	}

	row := rawRow{date: date, values: make(map[string]float64, len(tenors))}
	for _, tenor := range tenors {
		idx, ok := columns[tenor.Key]
		if !ok || idx >= len(record) {
			continue
		}
		cell := strings.TrimSpace(record[idx])
		if cell == "" {
			continue
		}
		value, err := strconv.ParseFloat(cell, 64)
		if err != nil {
			return rawRow{}, fmt.Errorf("%w: invalid %s value %q", ErrMalformed, tenor.Key, cell)
		}
		row.values[tenor.Key] = value
	}
	return row, nil
}

// buildCurve assembles the curve from rows sorted newest-first: rows[0] is the
// published date and rows[1] is the previous business day the deltas compare
// against.
func buildCurve(rows []rawRow) Curve {
	newest := rows[0]
	curve := Curve{Date: localCivilDate(newest.date), Points: make([]Point, 0, len(newest.values))}

	var previous map[string]float64
	if len(rows) > 1 {
		previous = rows[1].values
	}

	for _, tenor := range tenors {
		yield, ok := newest.values[tenor.Key]
		if !ok {
			continue
		}
		point := Point{Tenor: tenor.Key, Label: tenor.Label, Yield: yield}
		if previousYield, ok := previous[tenor.Key]; ok {
			delta := (yield - previousYield) * bpPerPercent
			point.DeltaBp = &delta
		}
		curve.Points = append(curve.Points, point)
	}
	return curve
}

// localCivilDate re-anchors a parsed CSV date to midnight in the system's local
// timezone. The payload carries a calendar date with no time and no zone;
// converting the instant instead of re-anchoring would move a UTC-midnight
// value to the previous day west of Greenwich.
func localCivilDate(t time.Time) time.Time {
	year, month, day := t.Date()
	return time.Date(year, month, day, 0, 0, 0, 0, time.Local)
}
