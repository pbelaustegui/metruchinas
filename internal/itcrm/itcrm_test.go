package itcrm

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// buildXLSX assembles a minimal workbook in memory whose first sheet holds
// sheetData (the inner XML of <sheetData>). Extra parts mimic the real file's
// neighbours so the parser must pick xl/worksheets/sheet1.xml by name.
func buildXLSX(t *testing.T, sheetData string) []byte {
	t.Helper()
	return buildXLSXWith(t, map[string]string{
		"xl/worksheets/sheet1.xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
			`<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>` +
			sheetData + `</sheetData></worksheet>`,
		"xl/worksheets/sheet2.xml": `<worksheet><sheetData><row r="1"><c r="A1"><v>1</v></c><c r="B1"><v>1</v></c></row></sheetData></worksheet>`,
	})
}

func buildXLSXWith(t *testing.T, parts map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range parts {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}

// dataRow renders a numeric A/B row like the real sheet does.
func dataRow(n int, serial, value string) string {
	r := strconv.Itoa(n)
	return `<row r="` + r + `"><c r="A` + r + `" s="9"><v>` + serial + `</v></c><c r="B` + r + `" s="4"><v>` + value + `</v></c><c r="C` + r + `"><v>1</v></c></row>`
}

const headerRows = `<row r="1"><c r="A1" t="s"><v>0</v></c></row>` +
	`<row r="2"><c r="A2" t="s"><v>1</v></c><c r="B2" t="s"><v>2</v></c></row>`

func TestParseIndicatorTakesLastTwoCompleteRows(t *testing.T) {
	// 46022 = 2025-12-31, 46023 = 2026-01-01 (days since 1899-12-30).
	sheet := headerRows +
		dataRow(3, "46021", "90") +
		dataRow(4, "46022", "100") +
		dataRow(5, "46023", "99.5") +
		`<row r="6"><c r="E6" s="6"/><c r="F6" s="5"/></row>` +
		`<row r="7"><c r="F7" s="5"/></row>`

	got, err := parseIndicator(buildXLSX(t, sheet))
	if err != nil {
		t.Fatalf("parseIndicator() error = %v", err)
	}
	if got.Value != 99.5 {
		t.Errorf("Value = %v, want 99.5", got.Value)
	}
	if want := -0.5; math.Abs(got.Variation-want) > 1e-9 {
		t.Errorf("Variation = %v, want %v", got.Variation, want)
	}
	wantDate := time.Date(2026, 1, 1, 0, 0, 0, 0, time.Local)
	if !got.Date.Equal(wantDate) {
		t.Errorf("Date = %v, want %v", got.Date, wantDate)
	}
}

func TestParseIndicatorIgnoresTrailingTextAndHalfFilledRows(t *testing.T) {
	// The real sheet ends with footnote strings and rows with only column A.
	sheet := headerRows +
		dataRow(3, "46022", "100") +
		dataRow(4, "46023", "101") +
		`<row r="5"><c r="A5" t="s"><v>19</v></c></row>` +
		`<row r="6"><c r="A6"><v>46024</v></c></row>`

	got, err := parseIndicator(buildXLSX(t, sheet))
	if err != nil {
		t.Fatalf("parseIndicator() error = %v", err)
	}
	if got.Value != 101 || math.Abs(got.Variation-1) > 1e-9 {
		t.Errorf("got %+v, want value 101 variation 1", got)
	}
}

func TestParseIndicatorMalformed(t *testing.T) {
	tests := map[string][]byte{
		"not a zip":     []byte("definitely not xlsx"),
		"missing sheet": buildXLSXWith(t, map[string]string{"xl/workbook.xml": "<workbook/>"}),
		"no data rows":  buildXLSX(t, headerRows),
		"one data row":  buildXLSX(t, headerRows+dataRow(3, "46023", "100")),
		"zero previous": buildXLSX(t, headerRows+dataRow(3, "46022", "0")+dataRow(4, "46023", "100")),
		"broken xml": buildXLSXWith(t, map[string]string{
			"xl/worksheets/sheet1.xml": `<worksheet><sheetData><row r="1"><c r="A1"><v>1</c>`,
		}),
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := parseIndicator(body)
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

func TestClientFetchRequestsWorkbookWithBrowserUserAgent(t *testing.T) {
	body := buildXLSX(t, headerRows+dataRow(3, "46022", "100")+dataRow(4, "46023", "101"))
	var gotPath, gotUA string
	c := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotUA = r.URL.Path, r.Header.Get("User-Agent")
		_, _ = w.Write(body)
	})

	got, err := c.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if got.Value != 101 {
		t.Errorf("Value = %v, want 101", got.Value)
	}
	if want := "/archivos/Pdfs/PublicacionesEstadisticas/ITCRMSerie.xlsx"; gotPath != want {
		t.Errorf("path = %q, want %q", gotPath, want)
	}
	if !strings.HasPrefix(gotUA, "Mozilla/5.0") {
		t.Errorf("User-Agent = %q, want a browser UA", gotUA)
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
