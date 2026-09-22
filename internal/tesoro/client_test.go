package tesoro

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// csvHeader is the exact header Treasury served when the endpoint was probed on
// 2026-09-22: 15 columns, including the "1.5 Month" column inserted after the
// original layout.
const csvHeader = `Date,"1 Mo","1.5 Month","2 Mo","3 Mo","4 Mo","6 Mo","1 Yr","2 Yr","3 Yr","5 Yr","7 Yr","10 Yr","20 Yr","30 Yr"`

// newestRowValues holds the tenors of the 2026-09-21 row. The levels the plan of
// record probed live (1Y 4.45, 2Y 4.76, 5Y 4.83, 10Y 4.96, 20Y 5.33, 30Y 5.29)
// are pinned; the short tenors are realistic fixtures, not observed data.
var newestRowValues = map[string]string{
	"1 Mo":      "4.05",
	"1.5 Month": "4.02",
	"2 Mo":      "4.03",
	"3 Mo":      "3.98",
	"4 Mo":      "3.95",
	"6 Mo":      "3.90",
	"1 Yr":      "4.45",
	"2 Yr":      "4.76",
	"3 Yr":      "4.64",
	"5 Yr":      "4.83",
	"7 Yr":      "4.90",
	"10 Yr":     "4.96",
	"20 Yr":     "5.33",
	"30 Yr":     "5.29",
}

// previousRowValues holds the 2026-09-18 row, the previous business day of the
// 2026-09-21 row.
var previousRowValues = map[string]string{
	"1 Mo":      "4.08",
	"1.5 Month": "4.05",
	"2 Mo":      "4.06",
	"3 Mo":      "4.01",
	"4 Mo":      "3.97",
	"6 Mo":      "3.91",
	"1 Yr":      "4.44",
	"2 Yr":      "4.77",
	"3 Yr":      "4.66",
	"5 Yr":      "4.80",
	"7 Yr":      "4.91",
	"10 Yr":     "4.93",
	"20 Yr":     "5.36",
	"30 Yr":     "5.33",
}

// yearResponse is the canned response for one year's CSV request.
type yearResponse struct {
	status int
	body   string
}

// newTestServer serves one canned response per year and records the years it was
// asked for, in order. It also asserts the request path and query Treasury's
// endpoint is documented to use.
func newTestServer(t *testing.T, responses map[int]yearResponse) (*httptest.Server, func() []int) {
	t.Helper()
	var mu sync.Mutex
	var requested []int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		year, ok := yearFromPath(r.URL.Path)
		if !ok {
			t.Errorf("request path = %q, want the CSV path for a year", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		wantQuery := fmt.Sprintf("type=daily_treasury_yield_curve&field_tdr_date_value=%d&page&_format=csv", year)
		if r.URL.RawQuery != wantQuery {
			t.Errorf("request query = %q, want %q", r.URL.RawQuery, wantQuery)
		}
		mu.Lock()
		requested = append(requested, year)
		mu.Unlock()

		resp, ok := responses[year]
		if !ok {
			t.Errorf("unexpected request for year %d", year)
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(resp.status)
		io.WriteString(w, resp.body)
	}))
	t.Cleanup(srv.Close)
	return srv, func() []int {
		mu.Lock()
		defer mu.Unlock()
		out := make([]int, len(requested))
		copy(out, requested)
		return out
	}
}

// yearFromPath extracts the {year} path element of the CSV endpoint.
func yearFromPath(path string) (int, bool) {
	const prefix = "/resource-center/data-chart-center/interest-rates/daily-treasury-rates.csv/"
	const suffix = "/all"
	if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, suffix) {
		return 0, false
	}
	year, err := time.Parse("2006", strings.TrimSuffix(strings.TrimPrefix(path, prefix), suffix))
	if err != nil {
		return 0, false
	}
	return year.Year(), true
}

// newTestClient points a Client at the test server and freezes its clock inside
// the given year, which is what selects the CSV file to request.
func newTestClient(srv *httptest.Server, year int) *Client {
	return &Client{
		HTTPClient: srv.Client(),
		BaseURL:    srv.URL,
		Now:        func() time.Time { return time.Date(year, time.September, 22, 12, 0, 0, 0, time.UTC) },
	}
}

// csvBody joins the live header with records, as Treasury publishes them.
func csvBody(records ...string) string {
	return strings.Join(append([]string{csvHeader}, records...), "\n") + "\n"
}

// csvRow builds one record in the live column order. A tenor absent from values
// renders as a blank cell, which is how Treasury retires a column.
func csvRow(date string, values map[string]string) string {
	cells := []string{date}
	for _, ten := range Tenors() {
		cells = append(cells, values[ten.Key])
	}
	return strings.Join(cells, ",")
}

// csvRecord builds one record for an arbitrary header order.
func csvRecord(header []string, date string, values map[string]string) string {
	cells := make([]string, 0, len(header))
	for _, name := range header {
		// Look the value up under the trimmed name but emit the cell exactly as
		// the header spells it, padding included.
		if trimmed := strings.TrimSpace(name); trimmed == dateColumn {
			cells = append(cells, date)
			continue
		} else {
			cells = append(cells, values[trimmed])
		}
	}
	return strings.Join(cells, ",")
}

// csvBodyWith builds a body whose header differs from the live one.
func csvBodyWith(header []string, records ...string) string {
	return strings.Join(append([]string{strings.Join(header, ",")}, records...), "\n") + "\n"
}

// pointOf returns the curve point for a tenor, failing when it is absent.
func pointOf(t *testing.T, c Curve, tenor string) Point {
	t.Helper()
	for _, p := range c.Points {
		if p.Tenor == tenor {
			return p
		}
	}
	t.Fatalf("tenor %q missing from the curve (points: %+v)", tenor, c.Points)
	return Point{}
}

// wantDelta returns the basis-point change for a tenor, failing the test cleanly
// when the delta is missing. Every delta assertion goes through it: a bare
// *DeltaBp dereference panics instead, which aborts the whole package and hides
// the rest of the suite behind a single mutation.
func wantDelta(t *testing.T, c Curve, tenor string) float64 {
	t.Helper()
	p := pointOf(t, c, tenor)
	if p.DeltaBp == nil {
		t.Fatalf("tenor %q has no delta, want a change against the previous business day", tenor)
	}
	return *p.DeltaBp
}

// wantClose asserts two floats match within float noise.
func wantClose(t *testing.T, name string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("%s = %v, want %v", name, got, want)
	}
}

func TestFetchParsesCurveWithHeaderAndDeltas(t *testing.T) {
	srv, requested := newTestServer(t, map[int]yearResponse{
		2026: {status: http.StatusOK, body: csvBody(
			csvRow("09/21/2026", newestRowValues),
			csvRow("09/18/2026", previousRowValues),
		)},
	})

	curve, err := newTestClient(srv, 2026).Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}

	wantDate := time.Date(2026, 9, 21, 0, 0, 0, 0, time.Local)
	if !curve.Date.Equal(wantDate) {
		t.Errorf("Date = %v, want %v", curve.Date, wantDate)
	}
	if curve.Date.Location() != time.Local {
		t.Errorf("Date location = %v, want %v (civil date, never converted)", curve.Date.Location(), time.Local)
	}

	want := Tenors()
	if len(curve.Points) != len(want) {
		t.Fatalf("len(Points) = %d, want %d", len(curve.Points), len(want))
	}
	for i, ten := range want {
		if curve.Points[i].Tenor != ten.Key {
			t.Errorf("Points[%d].Tenor = %q, want %q (ascending maturity order)", i, curve.Points[i].Tenor, ten.Key)
		}
		if curve.Points[i].Label != ten.Label {
			t.Errorf("Points[%d].Label = %q, want %q", i, curve.Points[i].Label, ten.Label)
		}
	}

	wantClose(t, "10 Yr yield", pointOf(t, curve, "10 Yr").Yield, 4.96)
	wantClose(t, "2 Yr yield", pointOf(t, curve, "2 Yr").Yield, 4.76)
	wantClose(t, "1.5 Month yield", pointOf(t, curve, "1.5 Month").Yield, 4.02)
	wantClose(t, "10 Yr delta", wantDelta(t, curve, "10 Yr"), 3)
	wantClose(t, "2 Yr delta", wantDelta(t, curve, "2 Yr"), -1)
	wantClose(t, "30 Yr delta", wantDelta(t, curve, "30 Yr"), -4)

	if got := requested(); len(got) != 1 || got[0] != 2026 {
		t.Errorf("requested years = %v, want [2026] only: a two-row year needs one request", got)
	}
}

func TestFetchResolvesColumnsByHeaderNameNotPosition(t *testing.T) {
	// Treasury inserted "1.5 Month" between "1 Mo" and "2 Mo". A parser that
	// reads by position silently reports the wrong tenor for every column after
	// the insertion, so the header order must not matter.
	header := []string{
		"Date", "30 Yr", "10 Yr", "1.5 Month", "2 Yr", "7 Yr", "1 Mo", "5 Yr",
		"3 Mo", "20 Yr", "1 Yr", "4 Mo", "6 Mo", "3 Yr", "2 Mo",
	}
	srv, _ := newTestServer(t, map[int]yearResponse{
		2026: {status: http.StatusOK, body: csvBodyWith(header,
			csvRecord(header, "09/21/2026", newestRowValues),
			csvRecord(header, "09/18/2026", previousRowValues),
		)},
	})

	curve, err := newTestClient(srv, 2026).Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	for _, ten := range Tenors() {
		p := pointOf(t, curve, ten.Key)
		wantClose(t, ten.Key+" yield", p.Yield, mustParse(t, newestRowValues[ten.Key]))
	}
}

func TestFetchHandlesAddedAndRetiredTenorColumns(t *testing.T) {
	t.Run("retired column renders as an absent tenor", func(t *testing.T) {
		header := []string{"Date", "1 Mo", "2 Mo", "3 Mo", "6 Mo", "1 Yr", "2 Yr", "5 Yr", "10 Yr", "30 Yr"}
		newest := map[string]string{"1 Mo": "4.05", "2 Mo": "4.03", "3 Mo": "3.98", "6 Mo": "3.90", "1 Yr": "4.45", "2 Yr": "4.76", "5 Yr": "4.83", "10 Yr": "4.96", "30 Yr": "5.29"}
		previous := map[string]string{"1 Mo": "4.08", "2 Mo": "4.06", "3 Mo": "4.01", "6 Mo": "3.91", "1 Yr": "4.44", "2 Yr": "4.77", "5 Yr": "4.80", "10 Yr": "4.93", "30 Yr": "5.33"}
		srv, _ := newTestServer(t, map[int]yearResponse{
			2026: {status: http.StatusOK, body: csvBodyWith(header,
				csvRecord(header, "09/21/2026", newest),
				csvRecord(header, "09/18/2026", previous),
			)},
		})

		curve, err := newTestClient(srv, 2026).Fetch(context.Background())
		if err != nil {
			t.Fatalf("Fetch() error = %v", err)
		}
		if len(curve.Points) != len(header)-1 {
			t.Fatalf("len(Points) = %d, want %d: only the published tenors", len(curve.Points), len(header)-1)
		}
		for _, absent := range []string{"1.5 Month", "20 Yr"} {
			for _, p := range curve.Points {
				if p.Tenor == absent {
					t.Errorf("tenor %q is absent from the header but present in Points", absent)
				}
			}
		}
		wantClose(t, "10 Yr yield", pointOf(t, curve, "10 Yr").Yield, 4.96)
		wantClose(t, "30 Yr delta", wantDelta(t, curve, "30 Yr"), -4)
	})

	t.Run("unknown extra column is ignored", func(t *testing.T) {
		header := []string{"Date", "1 Mo", "1.5 Month", "2 Mo", "3 Mo", "4 Mo", "6 Mo", "1 Yr", "2 Yr", "3 Yr", "5 Yr", "7 Yr", "10 Yr", "20 Yr", "30 Yr", "40 Yr"}
		newest := map[string]string{"2 Yr": "4.76", "10 Yr": "4.96", "40 Yr": "5.40"}
		previous := map[string]string{"2 Yr": "4.77", "10 Yr": "4.93", "40 Yr": "5.42"}
		srv, _ := newTestServer(t, map[int]yearResponse{
			2026: {status: http.StatusOK, body: csvBodyWith(header,
				csvRecord(header, "09/21/2026", newest),
				csvRecord(header, "09/18/2026", previous),
			)},
		})

		curve, err := newTestClient(srv, 2026).Fetch(context.Background())
		if err != nil {
			t.Fatalf("Fetch() error = %v", err)
		}
		if len(curve.Points) != 2 {
			t.Fatalf("len(Points) = %d, want the 2 published tracked tenors", len(curve.Points))
		}
		for _, p := range curve.Points {
			if p.Tenor == "40 Yr" {
				t.Error("tracked tenors must be the dashboard set, not every column in the payload")
			}
		}
		wantClose(t, "10 Yr delta", wantDelta(t, curve, "10 Yr"), 3)
		wantClose(t, "2 Yr delta", wantDelta(t, curve, "2 Yr"), -1)
	})
}

func TestFetchHandlesBlankCellsPerTenor(t *testing.T) {
	t.Run("blank on the newest row drops only that tenor", func(t *testing.T) {
		newest := cloneValues(newestRowValues)
		newest["10 Yr"] = ""
		srv, _ := newTestServer(t, map[int]yearResponse{
			2026: {status: http.StatusOK, body: csvBody(
				csvRow("09/21/2026", newest),
				csvRow("09/18/2026", previousRowValues),
			)},
		})

		curve, err := newTestClient(srv, 2026).Fetch(context.Background())
		if err != nil {
			t.Fatalf("Fetch() error = %v", err)
		}
		for _, p := range curve.Points {
			if p.Tenor == "10 Yr" {
				t.Error("a blank cell must not become a zero yield")
			}
		}
		// The neighbours keep their own levels: a blank cell must not shift the
		// mapping to the next column.
		wantClose(t, "20 Yr yield", pointOf(t, curve, "20 Yr").Yield, 5.33)
		wantClose(t, "7 Yr yield", pointOf(t, curve, "7 Yr").Yield, 4.90)
		if len(curve.Points) != len(Tenors())-1 {
			t.Errorf("len(Points) = %d, want %d", len(curve.Points), len(Tenors())-1)
		}
	})

	t.Run("blank on the previous row leaves the yield without a delta", func(t *testing.T) {
		previous := cloneValues(previousRowValues)
		previous["5 Yr"] = ""
		srv, _ := newTestServer(t, map[int]yearResponse{
			2026: {status: http.StatusOK, body: csvBody(
				csvRow("09/21/2026", newestRowValues),
				csvRow("09/18/2026", previous),
			)},
		})

		curve, err := newTestClient(srv, 2026).Fetch(context.Background())
		if err != nil {
			t.Fatalf("Fetch() error = %v", err)
		}
		p := pointOf(t, curve, "5 Yr")
		wantClose(t, "5 Yr yield", p.Yield, 4.83)
		if p.DeltaBp != nil {
			t.Errorf("5 Yr DeltaBp = %v, want nil when the previous row published nothing", *p.DeltaBp)
		}
		wantClose(t, "10 Yr delta", wantDelta(t, curve, "10 Yr"), 3)
	})

	t.Run("single row leaves every delta unknown", func(t *testing.T) {
		srv, _ := newTestServer(t, map[int]yearResponse{
			2026: {status: http.StatusOK, body: csvBody(
				csvRow("09/21/2026", newestRowValues),
			)},
			2025: {status: http.StatusOK, body: ""},
		})

		curve, err := newTestClient(srv, 2026).Fetch(context.Background())
		if err != nil {
			t.Fatalf("Fetch() error = %v", err)
		}
		if len(curve.Points) != len(Tenors()) {
			t.Fatalf("len(Points) = %d, want %d", len(curve.Points), len(Tenors()))
		}
		for _, p := range curve.Points {
			if p.DeltaBp != nil {
				t.Errorf("%s DeltaBp = %v, want nil with no previous business day", p.Tenor, *p.DeltaBp)
			}
		}
	})
}

func TestFetchSortsRowsByDescendingDate(t *testing.T) {
	// Treasury publishes descending, but the newest row is defined by its date,
	// not by its position in the file.
	srv, _ := newTestServer(t, map[int]yearResponse{
		2026: {status: http.StatusOK, body: csvBody(
			csvRow("09/18/2026", previousRowValues),
			csvRow("09/21/2026", newestRowValues),
		)},
	})

	curve, err := newTestClient(srv, 2026).Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if want := time.Date(2026, 9, 21, 0, 0, 0, 0, time.Local); !curve.Date.Equal(want) {
		t.Errorf("Date = %v, want %v (the newest row, not the first row)", curve.Date, want)
	}
	wantClose(t, "10 Yr yield", pointOf(t, curve, "10 Yr").Yield, 4.96)
	wantClose(t, "10 Yr delta", wantDelta(t, curve, "10 Yr"), 3)
}

func TestFetchFallsBackToPreviousYearForTheDelta(t *testing.T) {
	// The first business day of a year: the current year's file holds one row,
	// so the previous business day lives in the previous year's file.
	newest := cloneValues(newestRowValues)
	newest["10 Yr"] = "4.35"
	december := cloneValues(previousRowValues)
	december["10 Yr"] = "4.28"

	srv, requested := newTestServer(t, map[int]yearResponse{
		2026: {status: http.StatusOK, body: csvBody(csvRow("01/02/2026", newest))},
		2025: {status: http.StatusOK, body: csvBody(
			csvRow("12/31/2025", december),
			csvRow("12/30/2025", cloneValues(december)),
		)},
	})

	curve, err := newTestClient(srv, 2026).Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if want := time.Date(2026, 1, 2, 0, 0, 0, 0, time.Local); !curve.Date.Equal(want) {
		t.Errorf("Date = %v, want %v", curve.Date, want)
	}
	wantClose(t, "10 Yr yield", pointOf(t, curve, "10 Yr").Yield, 4.35)
	wantClose(t, "10 Yr delta", wantDelta(t, curve, "10 Yr"), 7)
	if got := requested(); len(got) != 2 || got[0] != 2026 || got[1] != 2025 {
		t.Errorf("requested years = %v, want [2026 2025]", got)
	}
}

func TestFetchUsesPreviousYearWhenCurrentYearIsEmpty(t *testing.T) {
	srv, requested := newTestServer(t, map[int]yearResponse{
		2026: {status: http.StatusOK, body: ""},
		2025: {status: http.StatusOK, body: csvBody(
			csvRow("12/31/2025", previousRowValues),
			csvRow("12/30/2025", cloneValues(previousRowValues)),
		)},
	})

	curve, err := newTestClient(srv, 2026).Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if want := time.Date(2025, 12, 31, 0, 0, 0, 0, time.Local); !curve.Date.Equal(want) {
		t.Errorf("Date = %v, want %v", curve.Date, want)
	}
	if len(curve.Points) != len(Tenors()) {
		t.Errorf("len(Points) = %d, want %d", len(curve.Points), len(Tenors()))
	}
	if got := requested(); len(got) != 2 {
		t.Errorf("requested years = %v, want both years", got)
	}
}

func TestFetchReportsNoDataWhenNoYearHasRows(t *testing.T) {
	srv, _ := newTestServer(t, map[int]yearResponse{
		2026: {status: http.StatusOK, body: ""},
		2025: {status: http.StatusOK, body: ""},
	})

	_, err := newTestClient(srv, 2026).Fetch(context.Background())
	if !errors.Is(err, ErrNoData) {
		t.Fatalf("Fetch() error = %v, want ErrNoData", err)
	}
	if errors.Is(err, ErrMalformed) {
		t.Errorf("Fetch() error = %v, want ErrNoData, not ErrMalformed", err)
	}
}

func TestFetchReportsNoDataForHeaderWithoutRows(t *testing.T) {
	srv, _ := newTestServer(t, map[int]yearResponse{
		2026: {status: http.StatusOK, body: csvHeader + "\n"},
		2025: {status: http.StatusOK, body: csvHeader + "\n"},
	})

	_, err := newTestClient(srv, 2026).Fetch(context.Background())
	if !errors.Is(err, ErrNoData) {
		t.Fatalf("Fetch() error = %v, want ErrNoData", err)
	}
}

func TestFetchKeepsLevelsWhenTheFallbackYearFails(t *testing.T) {
	// One row is enough to publish levels; a broken fallback year must not
	// discard them. The tenors simply lose their delta.
	srv, _ := newTestServer(t, map[int]yearResponse{
		2026: {status: http.StatusOK, body: csvBody(csvRow("01/02/2026", newestRowValues))},
		2025: {status: http.StatusServiceUnavailable, body: "service unavailable"},
	})

	curve, err := newTestClient(srv, 2026).Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch() error = %v, want the published levels", err)
	}
	if len(curve.Points) != len(Tenors()) {
		t.Fatalf("len(Points) = %d, want %d", len(curve.Points), len(Tenors()))
	}
	for _, p := range curve.Points {
		if p.DeltaBp != nil {
			t.Errorf("%s DeltaBp = %v, want nil", p.Tenor, *p.DeltaBp)
		}
	}
}

func TestFetchSurfacesTheFallbackFailureWhenNothingIsPublished(t *testing.T) {
	srv, _ := newTestServer(t, map[int]yearResponse{
		2026: {status: http.StatusOK, body: ""},
		2025: {status: http.StatusServiceUnavailable, body: "service unavailable"},
	})

	_, err := newTestClient(srv, 2026).Fetch(context.Background())
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("Fetch() error = %v, want *HTTPError when the only data source failed", err)
	}
	if httpErr.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("HTTPError.StatusCode = %d, want %d", httpErr.StatusCode, http.StatusServiceUnavailable)
	}
}

func TestFetchFailsOnNon2xxStatus(t *testing.T) {
	srv, _ := newTestServer(t, map[int]yearResponse{
		2026: {status: http.StatusForbidden, body: "access denied"},
	})

	_, err := newTestClient(srv, 2026).Fetch(context.Background())
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("Fetch() error = %v, want *HTTPError", err)
	}
	if httpErr.StatusCode != http.StatusForbidden {
		t.Errorf("HTTPError.StatusCode = %d, want %d", httpErr.StatusCode, http.StatusForbidden)
	}
	if errors.Is(err, ErrMalformed) || errors.Is(err, ErrNoData) {
		t.Errorf("Fetch() error = %v, want a status error only", err)
	}
	// This string reaches the dashboard section verbatim.
	if !strings.Contains(err.Error(), "unexpected status 403 Forbidden") {
		t.Errorf("Fetch() error = %q, want the status line named", err)
	}
}

func TestFetchFailsOnMalformedBodies(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"no header", "treasury data is currently unavailable\n"},
		{"header without the date column", `"10 Yr","2 Yr"` + "\n" + "4.96,4.76\n"},
		{"impossible date", csvBody(csvRow("13/45/2026", newestRowValues))},
		{"month and day transposed", csvBody(csvRow("21/09/2026", newestRowValues))},
		{"unparseable date", csvBody(csvRow("2026-09-21", newestRowValues))},
		{"blank date", csvBody(csvRow("", newestRowValues))},
		{"non-numeric yield", `Date,"10 Yr"` + "\n" + "9/21/2026,n/a\n"},
		{"unterminated quote in the header", `Date,"10 Yr` + "\n" + "9/21/2026,4.96\n"},
		{"short record", `Date,"1 Mo","10 Yr"` + "\n" + "9/21/2026,4.05\n"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			srv, _ := newTestServer(t, map[int]yearResponse{
				2026: {status: http.StatusOK, body: tt.body},
			})
			_, err := newTestClient(srv, 2026).Fetch(context.Background())
			if !errors.Is(err, ErrMalformed) {
				t.Fatalf("Fetch() error = %v, want ErrMalformed", err)
			}
		})
	}
}

// errorRoundTripper fails every request, standing in for a dead connection
// without touching the network.
type errorRoundTripper struct{ err error }

func (rt errorRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, rt.err
}

func TestFetchFailsOnTransportError(t *testing.T) {
	boom := errors.New("dial tcp 127.0.0.1:1: connect: connection refused")
	c := &Client{
		BaseURL:    defaultBaseURL,
		HTTPClient: &http.Client{Transport: errorRoundTripper{err: boom}},
	}

	_, err := c.Fetch(context.Background())
	if err == nil {
		t.Fatal("Fetch() error = nil, want the transport failure surfaced")
	}
	if !errors.Is(err, boom) {
		t.Errorf("Fetch() error = %v, want it to wrap %v", err, boom)
	}
	if !strings.Contains(err.Error(), "tesoro: request:") {
		t.Errorf("Fetch() error = %v, want the request stage named", err)
	}
	if errors.Is(err, ErrMalformed) || errors.Is(err, ErrNoData) {
		t.Errorf("Fetch() error = %v, want a transport failure only", err)
	}
}

func TestFetchFailsOnCancelledContext(t *testing.T) {
	srv, _ := newTestServer(t, map[int]yearResponse{
		2026: {status: http.StatusOK, body: csvBody(csvRow("09/21/2026", newestRowValues))},
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := newTestClient(srv, 2026).Fetch(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Fetch() error = %v, want context.Canceled", err)
	}
	if errors.Is(err, ErrMalformed) || errors.Is(err, ErrNoData) {
		t.Errorf("Fetch() error = %v, want a context failure only", err)
	}
}

func TestFetchWrapsAnUnreadableBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "512")
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, "Date\n")
	}))
	t.Cleanup(srv.Close)

	// A body shorter than its declared Content-Length is a read failure, not a
	// parse failure.
	_, err := newTestClient(srv, 2026).Fetch(context.Background())
	if err == nil {
		t.Fatal("Fetch() error = nil, want the truncated read reported")
	}
	if errors.Is(err, ErrMalformed) {
		t.Errorf("Fetch() error = %v, want a read failure, not ErrMalformed", err)
	}
	if !strings.Contains(err.Error(), "tesoro: read response:") {
		t.Errorf("Fetch() error = %v, want the read stage named", err)
	}
}

func TestFetchToleratesAByteOrderMark(t *testing.T) {
	srv, _ := newTestServer(t, map[int]yearResponse{
		2026: {status: http.StatusOK, body: "\ufeff" + csvBody(
			csvRow("09/21/2026", newestRowValues),
			csvRow("09/18/2026", previousRowValues),
		)},
	})

	curve, err := newTestClient(srv, 2026).Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	wantClose(t, "10 Yr yield", pointOf(t, curve, "10 Yr").Yield, 4.96)
}

func TestFetchSendsABrowserUserAgent(t *testing.T) {
	// Live finding (2026-09-22): home.treasury.gov throttles a non-browser user
	// agent. The identical request sent as "Go-http-client/1.1" needs 16 to 20
	// seconds and blows past the client timeout, while the same request sent as
	// a browser answers in under a second. The client therefore identifies as a
	// browser, exactly like the bonds client does.
	var userAgent string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userAgent = r.Header.Get("User-Agent")
		io.WriteString(w, csvBody(csvRow("09/21/2026", newestRowValues)))
	}))
	t.Cleanup(srv.Close)

	if _, err := newTestClient(srv, 2026).Fetch(context.Background()); err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if !strings.Contains(userAgent, "Mozilla/5.0") {
		t.Errorf("request User-Agent = %q, want a browser user agent", userAgent)
	}
}

func TestFetchUsesTheSystemYearByDefault(t *testing.T) {
	// The year comes from the clock, not from a pinned constant.
	year := time.Now().Year()
	srv, requested := newTestServer(t, map[int]yearResponse{
		year: {status: http.StatusOK, body: csvBody(
			csvRow("09/21/2026", newestRowValues),
			csvRow("09/18/2026", previousRowValues),
		)},
	})

	c := &Client{HTTPClient: srv.Client(), BaseURL: srv.URL}
	if _, err := c.Fetch(context.Background()); err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if got := requested(); len(got) == 0 || got[0] != year {
		t.Errorf("requested years = %v, want the current year %d first", got, year)
	}
}

func TestCurveSpreadBp(t *testing.T) {
	curve := Curve{Points: []Point{
		{Tenor: "2 Yr", Label: "2Y", Yield: 4.76},
		{Tenor: "10 Yr", Label: "10Y", Yield: 4.96},
	}}

	t.Run("normal curve", func(t *testing.T) {
		spread, ok := curve.SpreadBp("10Y", "2Y")
		if !ok {
			t.Fatal("SpreadBp() ok = false, want true")
		}
		wantClose(t, "spread", spread, 20)
	})

	t.Run("inverted curve is negative", func(t *testing.T) {
		inverted := Curve{Points: []Point{
			{Tenor: "2 Yr", Label: "2Y", Yield: 4.76},
			{Tenor: "10 Yr", Label: "10Y", Yield: 4.31},
		}}
		spread, ok := inverted.SpreadBp("10Y", "2Y")
		if !ok {
			t.Fatal("SpreadBp() ok = false, want true")
		}
		wantClose(t, "spread", spread, -45)
	})

	t.Run("canonical header keys are accepted too", func(t *testing.T) {
		spread, ok := curve.SpreadBp("10 Yr", "2 Yr")
		if !ok {
			t.Fatal("SpreadBp() ok = false, want the canonical key accepted")
		}
		wantClose(t, "spread", spread, 20)
	})

	t.Run("a missing tenor is not a zero spread", func(t *testing.T) {
		if _, ok := curve.SpreadBp("30Y", "2Y"); ok {
			t.Error("SpreadBp() ok = true for an absent long tenor, want false")
		}
		if _, ok := curve.SpreadBp("10Y", "30Y"); ok {
			t.Error("SpreadBp() ok = true for an absent short tenor, want false")
		}
	})
}

func TestTenorsListsEveryPublishedColumnAscending(t *testing.T) {
	got := Tenors()
	want := []Tenor{
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
	if len(got) != len(want) {
		t.Fatalf("len(Tenors()) = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Tenors()[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}

	// The returned slice must be a copy: mutating it cannot break the parser's
	// canonical column set.
	got[0].Key = "mutated"
	if Tenors()[0].Key != "1 Mo" {
		t.Error("Tenors() returned the package's own slice, want a copy")
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
	if c.Now == nil {
		t.Fatal("NewClient().Now is nil")
	}
}

// cloneValues copies a fixture map so a test can blank one cell.
func cloneValues(values map[string]string) map[string]string {
	out := make(map[string]string, len(values))
	for k, v := range values {
		out[k] = v
	}
	return out
}

// mustParse parses a fixture cell, failing the test on garbage.
func mustParse(t *testing.T, s string) float64 {
	t.Helper()
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		t.Fatalf("fixture cell %q is not a number: %v", s, err)
	}
	return v
}

func TestFetchFailsWhenNoTenorColumnMatches(t *testing.T) {
	// A header whose tenor columns were renamed must not degrade into a dated
	// section of dashes. The user cannot tell "Treasury published nothing" from
	// "our parser stopped matching", so the parser reports what it saw instead
	// of returning a valid date with an empty curve.
	header := []string{"Date", "1Mo", "3Mo", "6Mo", "1YR", "2YR", "5YR", "10YR", "30YR"}
	srv, _ := newTestServer(t, map[int]yearResponse{
		2026: {status: http.StatusOK, body: csvBodyWith(header,
			csvRecord(header, "09/21/2026", map[string]string{"1Mo": "4.05", "10YR": "4.96"}),
		)},
		2025: {status: http.StatusOK, body: ""},
	})

	curve, err := newTestClient(srv, 2026).Fetch(context.Background())
	if err == nil {
		t.Fatalf("Fetch() error = nil with %d published tenors and date %v, want ErrMalformed", len(curve.Points), curve.Date)
	}
	if !errors.Is(err, ErrMalformed) {
		t.Fatalf("Fetch() error = %v, want ErrMalformed", err)
	}
	if errors.Is(err, ErrNoData) {
		t.Errorf("Fetch() error = %v, want a header diagnosis, not ErrNoData", err)
	}
	if !curve.Date.IsZero() || len(curve.Points) != 0 {
		t.Errorf("Fetch() returned %v with %d points alongside the error, want no curve at all", curve.Date, len(curve.Points))
	}
	// The message names the cells the parser actually saw, so the mismatch is
	// diagnosable without the payload.
	for _, want := range []string{"10YR", "Date"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Fetch() error = %q, want it to name the header cell %q", err, want)
		}
	}
}

func TestFetchBoundsTheHeaderInTheDiagnosis(t *testing.T) {
	// A body that is not the curve at all can carry a very wide header; the
	// diagnosis must not grow with it.
	header := []string{"Date"}
	for i := 0; i < 60; i++ {
		header = append(header, fmt.Sprintf("column %d with a long name", i))
	}
	srv, _ := newTestServer(t, map[int]yearResponse{
		2026: {status: http.StatusOK, body: csvBodyWith(header, csvRecord(header, "09/21/2026", nil))},
		2025: {status: http.StatusOK, body: ""},
	})

	_, err := newTestClient(srv, 2026).Fetch(context.Background())
	if !errors.Is(err, ErrMalformed) {
		t.Fatalf("Fetch() error = %v, want ErrMalformed", err)
	}
	if len(err.Error()) > 400 {
		t.Errorf("Fetch() error is %d chars long, want a bounded diagnosis: %q", len(err.Error()), err)
	}
	if !strings.Contains(err.Error(), "61 columns") {
		t.Errorf("Fetch() error = %q, want the column count reported", err)
	}
}

func TestFetchFailsOnDuplicateTenorColumn(t *testing.T) {
	// Two "10 Yr" columns would otherwise resolve by position of the map write,
	// silently reporting one of the two values as the curve.
	header := []string{"Date", "10 Yr", "2 Yr", "10 Yr"}
	body := csvBodyWith(header, "9/21/2026,4.96,4.76,5.55", "9/18/2026,4.93,4.77,5.51")
	srv, _ := newTestServer(t, map[int]yearResponse{
		2026: {status: http.StatusOK, body: body},
	})

	curve, err := newTestClient(srv, 2026).Fetch(context.Background())
	if err == nil {
		t.Fatalf("Fetch() error = nil with 10 Yr = %v, want ErrMalformed for a duplicated tenor column",
			pointOf(t, curve, "10 Yr").Yield)
	}
	if !errors.Is(err, ErrMalformed) {
		t.Fatalf("Fetch() error = %v, want ErrMalformed", err)
	}
	if !strings.Contains(err.Error(), "10 Yr") {
		t.Errorf("Fetch() error = %q, want the duplicated column named", err)
	}
}

func TestFetchFailsWhenNoRowPublishesATenor(t *testing.T) {
	// A row carries a publication date but no tenor value: that is a payload the
	// parser does not understand, not a curve.
	body := csvBody(csvRow("09/21/2026", nil))
	srv, _ := newTestServer(t, map[int]yearResponse{
		2026: {status: http.StatusOK, body: body},
		2025: {status: http.StatusOK, body: ""},
	})

	curve, err := newTestClient(srv, 2026).Fetch(context.Background())
	if err == nil {
		t.Fatalf("Fetch() error = nil with %d points and date %v, want ErrMalformed", len(curve.Points), curve.Date)
	}
	if !errors.Is(err, ErrMalformed) {
		t.Fatalf("Fetch() error = %v, want ErrMalformed", err)
	}
	if errors.Is(err, ErrNoData) {
		t.Errorf("Fetch() error = %v, want a value diagnosis, not ErrNoData", err)
	}
}

func TestFetchToleratesWhitespaceAroundHeaderCells(t *testing.T) {
	// The finding claimed a leading space in a quoted header cell silently
	// degrades the section. Header cells are trimmed, so it does not: this pins
	// the tolerance so a future change cannot take it away.
	header := []string{"Date", " 1 Mo ", " 1.5 Month", "2 Mo", "3 Mo", "4 Mo", "6 Mo", "1 Yr", "2 Yr", "3 Yr", "5 Yr", "7 Yr", " 10 Yr ", "20 Yr", "30 Yr"}
	srv, _ := newTestServer(t, map[int]yearResponse{
		2026: {status: http.StatusOK, body: csvBodyWith(header,
			csvRecord(header, "09/21/2026", newestRowValues),
			csvRecord(header, "09/18/2026", previousRowValues),
		)},
	})

	curve, err := newTestClient(srv, 2026).Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch() error = %v, want whitespace-padded header cells accepted", err)
	}
	if len(curve.Points) != len(Tenors()) {
		t.Fatalf("len(Points) = %d, want %d", len(curve.Points), len(Tenors()))
	}
	wantClose(t, "10 Yr yield", pointOf(t, curve, "10 Yr").Yield, 4.96)
}
