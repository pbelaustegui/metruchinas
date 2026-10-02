package uscpi

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// csvSeries builds an ascending FRED CSV of n consecutive months ending at
// (year, month), whose level for the month k steps back is base-k. Extra raw
// rows are appended verbatim.
func csvSeries(id string, year int, month time.Month, n int, base float64, extra ...string) string {
	rows := make([]string, 0, n+len(extra))
	for k := n - 1; k >= 0; k-- {
		d := time.Date(year, month-time.Month(k), 1, 0, 0, 0, 0, time.UTC)
		rows = append(rows, fmt.Sprintf("%s,%v", d.Format("2006-01-02"), base-float64(k)))
	}
	rows = append(rows, extra...)
	return "observation_date," + id + "\n" + strings.Join(rows, "\n") + "\n"
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestParseIndicatorComputesVariationsFromLevels(t *testing.T) {
	// August 2026. SA: m=300, m-1=299. NSA: m=200, m-12=188, Dec 2025=192.
	sa := csvSeries("CPIAUCSL", 2026, time.August, 14, 300)
	nsa := csvSeries("CPIAUCNS", 2026, time.August, 14, 200)
	got, err := parseIndicator([]byte(sa), []byte(nsa))
	if err != nil {
		t.Fatalf("parseIndicator() error = %v", err)
	}
	if want := time.Date(2026, 8, 1, 0, 0, 0, 0, time.Local); !got.Month.Equal(want) {
		t.Errorf("Month = %v, want %v", got.Month, want)
	}
	if want := (300.0/299 - 1) * 100; !near(got.Monthly, want) {
		t.Errorf("Monthly = %v, want %v (from the SA series)", got.Monthly, want)
	}
	if want := (200.0/188 - 1) * 100; !near(got.Year12, want) {
		t.Errorf("Year12 = %v, want %v (from the NSA series)", got.Year12, want)
	}
	if want := (200.0/192 - 1) * 100; !near(got.YTD, want) {
		t.Errorf("YTD = %v, want %v (from the NSA series)", got.YTD, want)
	}
}

func TestParseIndicatorJanuaryUsesPreviousMonthAsYTDBase(t *testing.T) {
	sa := csvSeries("CPIAUCSL", 2026, time.January, 14, 300)
	nsa := csvSeries("CPIAUCNS", 2026, time.January, 14, 200)
	got, err := parseIndicator([]byte(sa), []byte(nsa))
	if err != nil {
		t.Fatalf("parseIndicator() error = %v", err)
	}
	if want := (200.0/199 - 1) * 100; !near(got.YTD, want) {
		t.Errorf("YTD = %v, want NSA m / NSA December = %v", got.YTD, want)
	}
}

func TestParseIndicatorDecemberYTDEqualsTwelveMonths(t *testing.T) {
	sa := csvSeries("CPIAUCSL", 2025, time.December, 14, 300)
	nsa := csvSeries("CPIAUCNS", 2025, time.December, 14, 200)
	got, err := parseIndicator([]byte(sa), []byte(nsa))
	if err != nil {
		t.Fatalf("parseIndicator() error = %v", err)
	}
	if !near(got.YTD, got.Year12) {
		t.Errorf("YTD = %v, want it equal to Year12 %v in December", got.YTD, got.Year12)
	}
}

func TestParseIndicatorSkipsEmptyTrailingCells(t *testing.T) {
	// FRED lists the next month with an empty value before it is published.
	sa := csvSeries("CPIAUCSL", 2026, time.August, 14, 300, "2026-09-01,")
	nsa := csvSeries("CPIAUCNS", 2026, time.August, 14, 200, "2026-09-01,")
	got, err := parseIndicator([]byte(sa), []byte(nsa))
	if err != nil {
		t.Fatalf("parseIndicator() error = %v", err)
	}
	if got.Month.Month() != time.August {
		t.Errorf("Month = %v, want August", got.Month)
	}
}

func TestParseIndicatorToleratesGapBetweenBases(t *testing.T) {
	// October 2025 was never published: the row is empty in both series, yet it
	// is neither a base for August 2026 nor the December base.
	sa := strings.Replace(csvSeries("CPIAUCSL", 2026, time.August, 14, 300), "2025-10-01,290", "2025-10-01,", 1)
	nsa := strings.Replace(csvSeries("CPIAUCNS", 2026, time.August, 14, 200), "2025-10-01,190", "2025-10-01,", 1)
	if !strings.Contains(sa, "2025-10-01,\n") || !strings.Contains(nsa, "2025-10-01,\n") {
		t.Fatal("fixture: October gap not applied")
	}
	got, err := parseIndicator([]byte(sa), []byte(nsa))
	if err != nil {
		t.Fatalf("parseIndicator() error = %v", err)
	}
	if want := (200.0/188 - 1) * 100; !near(got.Year12, want) {
		t.Errorf("Year12 = %v, want %v", got.Year12, want)
	}
}

func TestParseIndicatorUsesLatestMonthPresentInBoth(t *testing.T) {
	// NSA already has September; SA still ends in August.
	sa := csvSeries("CPIAUCSL", 2026, time.August, 14, 300)
	nsa := csvSeries("CPIAUCNS", 2026, time.September, 15, 201)
	got, err := parseIndicator([]byte(sa), []byte(nsa))
	if err != nil {
		t.Fatalf("parseIndicator() error = %v", err)
	}
	if got.Month.Month() != time.August {
		t.Fatalf("Month = %v, want August (latest in both)", got.Month)
	}
	// NSA August level is 200, August 2025 is 188.
	if want := (200.0/188 - 1) * 100; !near(got.Year12, want) {
		t.Errorf("Year12 = %v, want %v", got.Year12, want)
	}
}

func TestParseIndicatorMalformed(t *testing.T) {
	okSA := csvSeries("CPIAUCSL", 2026, time.August, 14, 300)
	okNSA := csvSeries("CPIAUCNS", 2026, time.August, 14, 200)
	drop := func(csv, row string) string {
		out := strings.Replace(csv, row+"\n", "", 1)
		if out == csv {
			t.Fatalf("fixture: row %q not found", row)
		}
		return out
	}
	tests := map[string]struct{ sa, nsa string }{
		"not csv html":       {okSA, "<html>blocked</html>"},
		"empty body":         {okSA, ""},
		"header only":        {okSA, "observation_date,CPIAUCNS\n"},
		"too few NSA months": {okSA, csvSeries("CPIAUCNS", 2026, time.August, 12, 200)},
		"too few SA months":  {csvSeries("CPIAUCSL", 2026, time.August, 1, 300), okNSA},
		"NSA lacks m-12":     {okSA, drop(okNSA, "2025-08-01,188")},
		"NSA lacks December": {okSA, drop(okNSA, "2025-12-01,192")},
		"SA lacks m-1":       {drop(okSA, "2026-07-01,299"), okNSA},
		"no common month":    {csvSeries("CPIAUCSL", 2024, time.August, 14, 300), okNSA},
		"bad date":           {okSA, strings.Replace(okNSA, "2026-08-01", "agosto", 1)},
		"bad level":          {okSA, strings.Replace(okNSA, "2026-08-01,200", "2026-08-01,abc", 1)},
		"zero level":         {okSA, strings.Replace(okNSA, "2026-07-01,199", "2026-07-01,0", 1)},
		"missing date col":   {okSA, "x,CPIAUCNS\n2026-08-01,200\n"},
	}
	for name, in := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := parseIndicator([]byte(in.sa), []byte(in.nsa))
			if !errors.Is(err, ErrMalformed) {
				t.Errorf("parseIndicator() error = %v, want ErrMalformed", err)
			}
		})
	}
}

// fixedNow is the clock injected into clients so the requested window is stable.
func fixedNow() time.Time { return time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC) }

// newServer serves both series from the same handler, keyed by the id query.
func newServer(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return &Client{HTTPClient: srv.Client(), BaseURL: srv.URL, Now: fixedNow}
}

func okHandler(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Query().Get("id") {
	case "CPIAUCSL":
		_, _ = w.Write([]byte(csvSeries("CPIAUCSL", 2026, time.August, 14, 300)))
	case "CPIAUCNS":
		_, _ = w.Write([]byte(csvSeries("CPIAUCNS", 2026, time.August, 14, 200)))
	default:
		http.NotFound(w, r)
	}
}

func TestClientFetchRequestsBothSeriesWithCosd(t *testing.T) {
	var paths, queries []string
	c := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		queries = append(queries, r.URL.RawQuery)
		okHandler(w, r)
	})
	got, err := c.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if got.Month.Month() != time.August {
		t.Errorf("Month = %v, want August", got.Month)
	}
	if len(queries) != 2 {
		t.Fatalf("requests = %d, want one per series", len(queries))
	}
	joined := strings.Join(queries, " ")
	for _, want := range []string{"id=CPIAUCSL", "id=CPIAUCNS"} {
		if !strings.Contains(joined, want) {
			t.Errorf("queries = %q, missing %q", joined, want)
		}
	}
	for i, q := range queries {
		if paths[i] != "/graph/fredgraph.csv" {
			t.Errorf("path = %q", paths[i])
		}
		// 14 months before 2026-10 is 2025-08-01.
		if !strings.Contains(q, "cosd=2025-08-01") {
			t.Errorf("query = %q, want cosd=2025-08-01", q)
		}
	}
}

func TestClientFetchFailsWhenEitherSeriesFails(t *testing.T) {
	for _, failing := range []string{"CPIAUCSL", "CPIAUCNS"} {
		t.Run(failing, func(t *testing.T) {
			c := newServer(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Get("id") == failing {
					http.Error(w, "boom", http.StatusServiceUnavailable)
					return
				}
				okHandler(w, r)
			})
			_, err := c.Fetch(context.Background())
			var httpErr *HTTPError
			if !errors.As(err, &httpErr) || httpErr.StatusCode != http.StatusServiceUnavailable {
				t.Fatalf("Fetch() error = %v, want *HTTPError 503", err)
			}
		})
	}
}

func TestClientFetchMalformedBody(t *testing.T) {
	c := newServer(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("<html>blocked</html>")) })
	if _, err := c.Fetch(context.Background()); !errors.Is(err, ErrMalformed) {
		t.Fatalf("Fetch() error = %v, want ErrMalformed", err)
	}
}

func TestClientFetchCanceledContext(t *testing.T) {
	c := newServer(t, func(http.ResponseWriter, *http.Request) {})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Fetch(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Fetch() error = %v, want context.Canceled", err)
	}
}

func TestZeroValueClientUsesDefaults(t *testing.T) {
	c := &Client{}
	if got := c.baseURL(); got != defaultBaseURL {
		t.Errorf("baseURL() = %q, want %q", got, defaultBaseURL)
	}
	if NewClient().BaseURL != defaultBaseURL {
		t.Error("NewClient().BaseURL is not the default")
	}
}
