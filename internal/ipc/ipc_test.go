package ipc

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

// body builds a desc-sorted API body of n consecutive months ending at
// (year, month), whose level for the month k steps back is base-k.
func body(year int, month time.Month, n int, base float64) string {
	rows := make([]string, 0, n)
	for k := 0; k < n; k++ {
		d := time.Date(year, month-time.Month(k), 1, 0, 0, 0, 0, time.UTC)
		rows = append(rows, fmt.Sprintf(`["%s", %v]`, d.Format("2006-01-02"), base-float64(k)))
	}
	return `{"data": [` + strings.Join(rows, ",") + `]}`
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestParseIndicatorComputesVariationsFromLevels(t *testing.T) {
	// August 2026: L[m]=200, L[m-1]=199, L[m-12]=188, L[Dec 2025]=192.
	got, err := parseIndicator([]byte(body(2026, time.August, 14, 200)))
	if err != nil {
		t.Fatalf("parseIndicator() error = %v", err)
	}
	if want := time.Date(2026, 8, 1, 0, 0, 0, 0, time.Local); !got.Month.Equal(want) {
		t.Errorf("Month = %v, want %v", got.Month, want)
	}
	if want := (200.0/199 - 1) * 100; !near(got.Monthly, want) {
		t.Errorf("Monthly = %v, want %v", got.Monthly, want)
	}
	if want := (200.0/188 - 1) * 100; !near(got.Year12, want) {
		t.Errorf("Year12 = %v, want %v", got.Year12, want)
	}
	if want := (200.0/192 - 1) * 100; !near(got.YTD, want) {
		t.Errorf("YTD = %v, want %v", got.YTD, want)
	}
}

func TestParseIndicatorJanuaryUsesPreviousMonthAsYTDBase(t *testing.T) {
	got, err := parseIndicator([]byte(body(2026, time.January, 14, 300)))
	if err != nil {
		t.Fatalf("parseIndicator() error = %v", err)
	}
	if !near(got.YTD, got.Monthly) {
		t.Errorf("YTD = %v, want it equal to Monthly %v in January", got.YTD, got.Monthly)
	}
}

func TestParseIndicatorDecemberYTDEqualsTwelveMonths(t *testing.T) {
	got, err := parseIndicator([]byte(body(2025, time.December, 14, 300)))
	if err != nil {
		t.Fatalf("parseIndicator() error = %v", err)
	}
	if !near(got.YTD, got.Year12) {
		t.Errorf("YTD = %v, want it equal to Year12 %v in December", got.YTD, got.Year12)
	}
}

func TestParseIndicatorMalformed(t *testing.T) {
	tests := map[string]string{
		"not json":      "<html>blocked</html>",
		"empty data":    `{"data": []}`,
		"too few rows":  body(2026, time.August, 12, 200),
		"gap":           `{"data": [["2026-08-01", 200], ["2026-06-01", 199], ["2026-05-01", 198], ["2026-04-01", 197], ["2026-03-01", 196], ["2026-02-01", 195], ["2026-01-01", 194], ["2025-12-01", 193], ["2025-11-01", 192], ["2025-10-01", 191], ["2025-09-01", 190], ["2025-08-01", 189], ["2025-07-01", 188], ["2025-06-01", 187]]}`,
		"ascending":     `{"data": [["2025-07-01", 188], ["2025-08-01", 189], ["2025-09-01", 190], ["2025-10-01", 191], ["2025-11-01", 192], ["2025-12-01", 193], ["2026-01-01", 194], ["2026-02-01", 195], ["2026-03-01", 196], ["2026-04-01", 197], ["2026-05-01", 198], ["2026-06-01", 199], ["2026-07-01", 200]]}`,
		"bad date":      strings.Replace(body(2026, time.August, 14, 200), "2026-08-01", "agosto", 1),
		"null level":    strings.Replace(body(2026, time.August, 14, 200), "200", "null", 1),
		"zero level":    strings.Replace(body(2026, time.August, 14, 200), "199", "0", 1),
		"short row":     `{"data": [["2026-08-01"]]}`,
		"string number": strings.Replace(body(2026, time.August, 14, 200), "200", `"200"`, 1),
	}
	for name, in := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := parseIndicator([]byte(in))
			if !errors.Is(err, ErrMalformed) {
				t.Errorf("parseIndicator() error = %v, want ErrMalformed", err)
			}
		})
	}
}

func newServer(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return &Client{HTTPClient: srv.Client(), BaseURL: srv.URL}
}

func TestClientFetchRequestsSeriesDescendingWithFourteenRows(t *testing.T) {
	var gotPath, gotQuery string
	c := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery = r.URL.Path, r.URL.RawQuery
		_, _ = w.Write([]byte(body(2026, time.August, 14, 200)))
	})
	got, err := c.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if got.Month.Month() != time.August {
		t.Errorf("Month = %v, want August", got.Month)
	}
	if gotPath != "/series/api/series/" {
		t.Errorf("path = %q", gotPath)
	}
	for _, want := range []string{"ids=148.3_INIVELNAL_DICI_M_26", "limit=14", "sort=desc"} {
		if !strings.Contains(gotQuery, want) {
			t.Errorf("query = %q, missing %q", gotQuery, want)
		}
	}
}

func TestClientFetchHTTPError(t *testing.T) {
	c := newServer(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusServiceUnavailable)
	})
	_, err := c.Fetch(context.Background())
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) || httpErr.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("Fetch() error = %v, want *HTTPError 503", err)
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
