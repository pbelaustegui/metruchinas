// Package itcrm is a minimal client for the BCRA's Índice de Tipo de Cambio
// Real Multilateral (ITCRM, base 17-12-15 = 100).
//
// The BCRA publishes the whole daily series as an XLSX workbook,
// GET https://www.bcra.gob.ar/archivos/Pdfs/PublicacionesEstadisticas/ITCRMSerie.xlsx
// (about 3.6 MB, updated once a day, no authentication). Three properties of
// the file shape this package:
//
//   - Only the first sheet is read. Column A holds an Excel date serial (days
//     since 1899-12-30) and column B the ITCRM value. The first rows are
//     headers (shared strings) and the sheet ends with empty styled cells and
//     footnotes, so the parser keeps the last two rows that carry a numeric A
//     and a numeric B instead of trusting row positions.
//   - The sheet XML is about 9 MB, so it is streamed with encoding/xml rather
//     than loaded into a tree. The standard library is the only dependency.
//   - The file carries no variation field: the daily variation is derived
//     locally from the last two rows.
//
// The BCRA site rejects clients without a browser User-Agent, so one is sent.
package itcrm

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	defaultBaseURL = "https://www.bcra.gob.ar"
	workbookPath   = "/archivos/Pdfs/PublicacionesEstadisticas/ITCRMSerie.xlsx"
	// defaultTimeout covers dial and the whole ~3.6 MB download, which is far
	// larger than the JSON payloads of the other sources.
	defaultTimeout = 30 * time.Second

	browserUA = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/137.0.0.0 Safari/537.36"

	sheetName = "xl/worksheets/sheet1.xml"

	// maxWorkbookBytes and maxSheetBytes bound what is read from the network and
	// from the archive, so a hostile or broken response cannot exhaust memory.
	maxWorkbookBytes = 32 << 20
	maxSheetBytes    = 128 << 20
)

// excelEpoch is day zero of the Excel 1900 date system as used for any date
// after 1900-03-01 (the 1900 leap-year bug makes the epoch 1899-12-30).
var excelEpoch = [3]int{1899, 12, 30}

// ErrMalformed reports a response body that is not the expected workbook or
// that lacks two usable data rows.
var ErrMalformed = errors.New("itcrm: malformed response payload")

// HTTPError reports a non-2xx response from the server.
type HTTPError struct {
	// StatusCode is the numeric status code returned by the server.
	StatusCode int
	// Status is the full status line, e.g. "503 Service Unavailable".
	Status string
}

// Error implements the error interface.
func (e *HTTPError) Error() string {
	return fmt.Sprintf("itcrm: unexpected status %s", e.Status)
}

// Indicator is the latest ITCRM figure.
type Indicator struct {
	// Value is the latest index value, e.g. 85.04.
	Value float64
	// Variation is the percent change against the previous row of the series,
	// e.g. -0.13 for a 0.13% drop.
	Variation float64
	// Date is the date of the latest row, anchored to midnight in the system's
	// local timezone like the other sources, so it agrees with the clock the
	// dashboard displays.
	Date time.Time
}

// Client fetches the indicator from www.bcra.gob.ar.
//
// A zero-value Client is usable and resolves the same defaults as NewClient.
type Client struct {
	// HTTPClient performs the HTTP requests. It defaults to an http.Client with
	// a 30-second timeout covering connection and body download.
	HTTPClient *http.Client
	// BaseURL is the server base URL. It defaults to https://www.bcra.gob.ar.
	BaseURL string
}

// NewClient returns a Client with the default timeout and base URL.
func NewClient() *Client {
	return &Client{
		HTTPClient: &http.Client{Timeout: defaultTimeout},
		BaseURL:    defaultBaseURL,
	}
}

// Fetch downloads the workbook and returns the latest indicator.
//
// Error classes:
//   - *HTTPError for non-2xx responses, carrying the status code.
//   - an error wrapping ErrMalformed when the body is not a workbook with a
//     first sheet holding at least two complete date/value rows.
//   - a wrapped error for network, read, or context failures, detectable with
//     errors.Is against context.Canceled / context.DeadlineExceeded.
func (c *Client) Fetch(ctx context.Context) (Indicator, error) {
	body, err := c.get(ctx)
	if err != nil {
		return Indicator{}, err
	}
	return parseIndicator(body)
}

// Fetch returns the latest indicator using a default client. See Client.Fetch.
func Fetch(ctx context.Context) (Indicator, error) {
	return NewClient().Fetch(ctx)
}

func (c *Client) baseURL() string {
	if c.BaseURL == "" {
		return defaultBaseURL
	}
	return c.BaseURL
}

func (c *Client) get(ctx context.Context) ([]byte, error) {
	httpClient := c.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultTimeout}
	}
	endpoint, err := url.JoinPath(c.baseURL(), workbookPath)
	if err != nil {
		return nil, fmt.Errorf("itcrm: build URL: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("itcrm: build request: %w", err)
	}
	req.Header.Set("User-Agent", browserUA)
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("itcrm: request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, &HTTPError{StatusCode: resp.StatusCode, Status: resp.Status}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxWorkbookBytes))
	if err != nil {
		return nil, fmt.Errorf("itcrm: read response: %w", err)
	}
	return body, nil
}

// point is one complete row of the series.
type point struct {
	serial float64
	value  float64
}

// parseIndicator extracts the last two complete rows of the first sheet.
func parseIndicator(body []byte) (Indicator, error) {
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		return Indicator{}, fmt.Errorf("itcrm: %w: not a zip archive: %v", ErrMalformed, err)
	}
	var sheet *zip.File
	for _, f := range zr.File {
		if f.Name == sheetName {
			sheet = f
			break
		}
	}
	if sheet == nil {
		return Indicator{}, fmt.Errorf("itcrm: %w: %s not found", ErrMalformed, sheetName)
	}
	rc, err := sheet.Open()
	if err != nil {
		return Indicator{}, fmt.Errorf("itcrm: %w: open sheet: %v", ErrMalformed, err)
	}
	defer rc.Close()

	prev, last, n, err := lastTwoPoints(io.LimitReader(rc, maxSheetBytes))
	if err != nil {
		return Indicator{}, err
	}
	if n < 2 {
		return Indicator{}, fmt.Errorf("itcrm: %w: need two complete rows, found %d", ErrMalformed, n)
	}
	if prev.value == 0 || math.IsNaN(prev.value) || math.IsInf(prev.value, 0) ||
		math.IsNaN(last.value) || math.IsInf(last.value, 0) {
		return Indicator{}, fmt.Errorf("itcrm: %w: unusable values %v -> %v", ErrMalformed, prev.value, last.value)
	}
	days := int(math.Floor(last.serial))
	date := time.Date(excelEpoch[0], time.Month(excelEpoch[1]), excelEpoch[2]+days, 0, 0, 0, 0, time.Local)
	return Indicator{
		Value:     last.value,
		Variation: (last.value/prev.value - 1) * 100,
		Date:      date,
	}, nil
}

// lastTwoPoints streams the sheet XML and returns the last two rows holding a
// numeric value in both column A and column B, plus how many such rows exist
// (capped at 2). Cells typed as strings (headers, footnotes) are not numeric
// and so invalidate the row for that column.
func lastTwoPoints(r io.Reader) (prev, last point, n int, err error) {
	dec := xml.NewDecoder(r)

	var (
		haveA, haveB bool
		a, b         float64
		col          string
		numeric      bool
	)
	for {
		tok, tokErr := dec.Token()
		if tokErr == io.EOF {
			return prev, last, n, nil
		}
		if tokErr != nil {
			return point{}, point{}, 0, fmt.Errorf("itcrm: %w: sheet XML: %v", ErrMalformed, tokErr)
		}
		switch el := tok.(type) {
		case xml.StartElement:
			switch el.Name.Local {
			case "row":
				haveA, haveB = false, false
			case "c":
				col, numeric = "", true
				for _, attr := range el.Attr {
					switch attr.Name.Local {
					case "r":
						col = strings.TrimRight(attr.Value, "0123456789")
					case "t":
						// Only plain numbers: "s", "str", "b", "e"... are text.
						numeric = attr.Value == "n"
					}
				}
			case "v":
				var text string
				if err := dec.DecodeElement(&text, &el); err != nil {
					return point{}, point{}, 0, fmt.Errorf("itcrm: %w: sheet XML: %v", ErrMalformed, err)
				}
				if !numeric || (col != "A" && col != "B") {
					continue
				}
				v, parseErr := strconv.ParseFloat(strings.TrimSpace(text), 64)
				if parseErr != nil {
					continue
				}
				if col == "A" {
					a, haveA = v, true
				} else {
					b, haveB = v, true
				}
			}
		case xml.EndElement:
			if el.Name.Local == "row" && haveA && haveB {
				prev, last = last, point{serial: a, value: b}
				if n < 2 {
					n++
				}
			}
		}
	}
}
