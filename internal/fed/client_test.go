package fed

import (
	"context"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// seriesResponse is the canned response for one series request.
type seriesResponse struct {
	status int
	body   string
}

// fedTestServer serves one canned response per series id and records every
// request it received, in order.
type fedTestServer struct {
	*httptest.Server

	mu     sync.Mutex
	ids    []string
	cosd   []string
	agents []string
}

func (s *fedTestServer) requestedIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.ids))
	copy(out, s.ids)
	return out
}

func (s *fedTestServer) cosineDates() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.cosd))
	copy(out, s.cosd)
	return out
}

func (s *fedTestServer) userAgents() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.agents))
	copy(out, s.agents)
	return out
}

// newTestServer serves one canned response per series and asserts the request
// path FRED's CSV endpoint is documented to use.
func newTestServer(t *testing.T, responses map[string]seriesResponse) *fedTestServer {
	t.Helper()
	srv := &fedTestServer{}
	srv.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != csvPath {
			t.Errorf("request path = %q, want %q", r.URL.Path, csvPath)
			http.NotFound(w, r)
			return
		}
		id := r.URL.Query().Get("id")
		srv.mu.Lock()
		srv.ids = append(srv.ids, id)
		srv.cosd = append(srv.cosd, r.URL.Query().Get("cosd"))
		srv.agents = append(srv.agents, r.Header.Get("User-Agent"))
		srv.mu.Unlock()

		resp, ok := responses[id]
		if !ok {
			t.Errorf("unexpected request for series %q", id)
			http.NotFound(w, r)
			return
		}
		status := resp.status
		if status == 0 {
			status = http.StatusOK
		}
		w.WriteHeader(status)
		io.WriteString(w, resp.body)
	}))
	t.Cleanup(srv.Server.Close)
	return srv
}

// newTestClient points a Client at the test server with a frozen clock.
func newTestClient(srv *fedTestServer, now time.Time) *Client {
	return &Client{
		HTTPClient:   srv.Client(),
		BaseURL:      srv.Server.URL,
		Now:          func() time.Time { return now },
		LookbackDays: 30,
	}
}

// seriesBody joins a series header with its records, as FRED publishes them.
func seriesBody(id string, rows ...string) string {
	return "observation_date," + id + "\n" + strings.Join(rows, "\n") + "\n"
}

// liveClock is the instant the plan of record probed the endpoint.
var liveClock = time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)

// okBodies is the healthy payload set: the DFF series lags the target range and
// ends in empty cells, exactly as the live payload did on 2026-09-23.
func okBodies() map[string]seriesResponse {
	return map[string]seriesResponse{
		targetLowSeries: {
			body: seriesBody(targetLowSeries,
				"2026-09-18,3.75",
				"2026-09-21,3.75",
				"2026-09-22,3.75",
				"2026-09-23,3.75",
			),
		},
		targetHighSeries: {
			body: seriesBody(targetHighSeries,
				"2026-09-18,4.00",
				"2026-09-21,4.00",
				"2026-09-22,4.00",
				"2026-09-23,4.00",
			),
		},
		effectiveSeries: {
			body: seriesBody(effectiveSeries,
				"2026-09-16,3.63",
				"2026-09-17,",
				"2026-09-21,3.88",
				"2026-09-22,",
				"2026-09-23,",
			),
		},
	}
}

func TestFetchReadsTheNewestNonEmptyRowPerSeries(t *testing.T) {
	srv := newTestServer(t, okBodies())
	c := newTestClient(srv, liveClock)

	rate, err := c.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if rate.TargetLow != 3.75 {
		t.Errorf("TargetLow = %v, want 3.75", rate.TargetLow)
	}
	if rate.TargetHigh != 4.00 {
		t.Errorf("TargetHigh = %v, want 4.00", rate.TargetHigh)
	}
	if rate.Effective != 3.88 {
		t.Errorf("Effective = %v, want 3.88", rate.Effective)
	}
	// The newest non-empty DFF row is 2026-09-21: the two trailing empty cells
	// are unpublished, not zero.
	wantDate := time.Date(2026, 9, 21, 0, 0, 0, 0, time.Local)
	if !rate.Date.Equal(wantDate) {
		t.Errorf("Date = %v, want %v", rate.Date, wantDate)
	}
	if rate.EffectiveDeltaBp == nil {
		t.Fatal("EffectiveDeltaBp = nil, want the change against the previous non-empty row")
	}
	// The previous non-empty row is 3.63, four calendar days back (the row in
	// between is empty): the delta skips missing rows instead of comparing
	// against an empty cell.
	if got := *rate.EffectiveDeltaBp; math.Abs(got-25) > 1e-9 {
		t.Errorf("EffectiveDeltaBp = %v, want 25", got)
	}
}

func TestFetchReportsNilDeltaWithASingleObservation(t *testing.T) {
	bodies := okBodies()
	bodies[effectiveSeries] = seriesResponse{body: seriesBody(effectiveSeries, "2026-09-21,3.88", "2026-09-22,")}
	srv := newTestServer(t, bodies)
	c := newTestClient(srv, liveClock)

	rate, err := c.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if rate.EffectiveDeltaBp != nil {
		t.Errorf("EffectiveDeltaBp = %v, want nil with fewer than two non-empty rows", *rate.EffectiveDeltaBp)
	}
}

func TestFetchRequestsEachSeriesOnceWithCosineStart(t *testing.T) {
	srv := newTestServer(t, okBodies())
	c := newTestClient(srv, liveClock)

	if _, err := c.Fetch(context.Background()); err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}

	// Three single-series requests, in a fixed order. A multi-series request
	// would leave cosd ignored and download the full history.
	wantIDs := []string{targetLowSeries, targetHighSeries, effectiveSeries}
	ids := srv.requestedIDs()
	if len(ids) != len(wantIDs) {
		t.Fatalf("fetch made %d requests (%v), want %d single-series requests", len(ids), ids, len(wantIDs))
	}
	for i, want := range wantIDs {
		if ids[i] != want {
			t.Errorf("request %d asked for id %q, want %q", i, ids[i], want)
		}
	}

	wantCosd := liveClock.AddDate(0, 0, -30).Format(csvDateLayout)
	for i, got := range srv.cosineDates() {
		if got != wantCosd {
			t.Errorf("request %d cosd = %q, want %q", i, got, wantCosd)
		}
	}
	// The live endpoint rejects a browser user agent from a Go client with an
	// HTTP/2 INTERNAL_ERROR, so this pins the opposite of tesoro's rule: the
	// request must not look like a browser.
	for i, ua := range srv.userAgents() {
		if strings.Contains(ua, "Mozilla") {
			t.Errorf("request %d User-Agent = %q, want a non-browser agent", i, ua)
		}
	}
}

func TestFetchUsesTheDefaultLookback(t *testing.T) {
	srv := newTestServer(t, okBodies())
	c := newTestClient(srv, liveClock)
	c.LookbackDays = 0 // resolve the default

	if _, err := c.Fetch(context.Background()); err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	want := liveClock.AddDate(0, 0, -defaultLookbackDays).Format(csvDateLayout)
	for i, got := range srv.cosineDates() {
		if got != want {
			t.Errorf("request %d cosd = %q, want the default lookback %q", i, got, want)
		}
	}
}

func TestFetchReportsNoDataWhenARequiredSeriesHasOnlyEmptyCells(t *testing.T) {
	bodies := okBodies()
	bodies[targetLowSeries] = seriesResponse{body: seriesBody(targetLowSeries, "2026-09-23,")}
	srv := newTestServer(t, bodies)
	c := newTestClient(srv, liveClock)

	_, err := c.Fetch(context.Background())
	if !errors.Is(err, ErrNoData) {
		t.Fatalf("Fetch() error = %v, want ErrNoData", err)
	}
}

func TestFetchReportsNoDataForAHeaderWithoutRows(t *testing.T) {
	bodies := okBodies()
	bodies[effectiveSeries] = seriesResponse{body: seriesBody(effectiveSeries)}
	srv := newTestServer(t, bodies)
	c := newTestClient(srv, liveClock)

	_, err := c.Fetch(context.Background())
	if !errors.Is(err, ErrNoData) {
		t.Fatalf("Fetch() error = %v, want ErrNoData", err)
	}
}

func TestFetchReportsNoDataForAnEmptyBody(t *testing.T) {
	bodies := okBodies()
	bodies[effectiveSeries] = seriesResponse{body: ""}
	srv := newTestServer(t, bodies)
	c := newTestClient(srv, liveClock)

	_, err := c.Fetch(context.Background())
	if !errors.Is(err, ErrNoData) {
		t.Fatalf("Fetch() error = %v, want ErrNoData", err)
	}
}

func TestFetchFailsOnMalformedBodies(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"wrong field count", "observation_date,DFEDTARL\n2026-09-23,3.75,extra\n"},
		{"unterminated quote", "observation_date,DFEDTARL\n\"unterminated\n"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			bodies := okBodies()
			bodies[targetLowSeries] = seriesResponse{body: tt.body}
			srv := newTestServer(t, bodies)
			c := newTestClient(srv, liveClock)

			_, err := c.Fetch(context.Background())
			if !errors.Is(err, ErrMalformed) {
				t.Fatalf("Fetch() error = %v, want ErrMalformed", err)
			}
		})
	}
}

func TestFetchFailsWhenTheDateColumnIsMissing(t *testing.T) {
	bodies := okBodies()
	bodies[targetLowSeries] = seriesResponse{body: "date,DFEDTARL\n2026-09-23,3.75\n"}
	srv := newTestServer(t, bodies)
	c := newTestClient(srv, liveClock)

	_, err := c.Fetch(context.Background())
	if !errors.Is(err, ErrMalformed) {
		t.Fatalf("Fetch() error = %v, want ErrMalformed", err)
	}
}

func TestFetchFailsOnAnUnparseableDate(t *testing.T) {
	bodies := okBodies()
	bodies[targetLowSeries] = seriesResponse{body: seriesBody(targetLowSeries, "23-09-2026,3.75")}
	srv := newTestServer(t, bodies)
	c := newTestClient(srv, liveClock)

	_, err := c.Fetch(context.Background())
	if !errors.Is(err, ErrMalformed) {
		t.Fatalf("Fetch() error = %v, want ErrMalformed", err)
	}
}

func TestFetchFailsOnAnUnparseableValue(t *testing.T) {
	bodies := okBodies()
	bodies[targetLowSeries] = seriesResponse{body: seriesBody(targetLowSeries, "2026-09-23,not-a-number")}
	srv := newTestServer(t, bodies)
	c := newTestClient(srv, liveClock)

	_, err := c.Fetch(context.Background())
	if !errors.Is(err, ErrMalformed) {
		t.Fatalf("Fetch() error = %v, want ErrMalformed", err)
	}
}

func TestFetchFailsOnNon2xxStatus(t *testing.T) {
	bodies := okBodies()
	bodies[targetLowSeries] = seriesResponse{status: http.StatusServiceUnavailable, body: "upstream error"}
	srv := newTestServer(t, bodies)
	c := newTestClient(srv, liveClock)

	_, err := c.Fetch(context.Background())
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("Fetch() error = %v, want *HTTPError", err)
	}
	if httpErr.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("StatusCode = %d, want %d", httpErr.StatusCode, http.StatusServiceUnavailable)
	}
}

func TestFetchFailsOnATransportError(t *testing.T) {
	// 127.0.0.1:1 refuses the connection, which is a transport failure rather
	// than a payload the parser could diagnose.
	c := &Client{
		HTTPClient:   &http.Client{Timeout: time.Second},
		BaseURL:      "http://127.0.0.1:1",
		Now:          func() time.Time { return liveClock },
		LookbackDays: 30,
	}

	_, err := c.Fetch(context.Background())
	if err == nil {
		t.Fatal("Fetch() error = nil, want a transport failure")
	}
	if errors.Is(err, ErrMalformed) || errors.Is(err, ErrNoData) {
		t.Errorf("Fetch() error = %v, want a wrapped network failure", err)
	}
}

func TestFetchFailsOnACancelledContext(t *testing.T) {
	srv := newTestServer(t, okBodies())
	c := newTestClient(srv, liveClock)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := c.Fetch(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Fetch() error = %v, want it to wrap context.Canceled", err)
	}
}

func TestNewClientAppliesDefaults(t *testing.T) {
	c := NewClient()
	if c.HTTPClient == nil {
		t.Error("NewClient().HTTPClient is nil, want a default client")
	}
	if c.BaseURL != defaultBaseURL {
		t.Errorf("NewClient().BaseURL = %q, want %q", c.BaseURL, defaultBaseURL)
	}
	if c.Now == nil {
		t.Error("NewClient().Now is nil, want a clock default")
	}
	if c.LookbackDays != defaultLookbackDays {
		t.Errorf("NewClient().LookbackDays = %d, want %d", c.LookbackDays, defaultLookbackDays)
	}
}

func TestZeroValueClientResolvesDefaults(t *testing.T) {
	c := Client{}
	if got := c.lookbackDays(); got != defaultLookbackDays {
		t.Errorf("zero Client lookbackDays() = %d, want %d", got, defaultLookbackDays)
	}
	if c.now().IsZero() {
		t.Error("zero Client now() is zero, want the wall clock")
	}
}

func TestFetchToleratesAByteOrderMark(t *testing.T) {
	bodies := okBodies()
	bodies[targetLowSeries] = seriesResponse{body: "\ufeff" + seriesBody(targetLowSeries, "2026-09-23,3.75")}
	srv := newTestServer(t, bodies)
	c := newTestClient(srv, liveClock)

	rate, err := c.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if rate.TargetLow != 3.75 {
		t.Errorf("TargetLow = %v, want 3.75", rate.TargetLow)
	}
}
