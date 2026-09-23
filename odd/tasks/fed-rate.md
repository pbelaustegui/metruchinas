# Feature: fed-rate

Tracked in repo: `odd/tasks/fed-rate.md` | Engram mirror: `odd/fed-rate/tasks`

## Objective

Add the Federal Reserve's reference rate to the Treasury-curve section of the
dashboard: the FOMC **target range** (lower–upper bound) and the **effective
federal funds rate (EFFR)**, each with its observation date. This is the
"tasa de referencia de la FED" that Argentine financial news quotes (e.g.
"la Fed mantuvo la tasa en 3,75%–4,00%"), shown next to the yield curve it
anchors.

## Problem / Why

The curve shows market yields but not the policy rate that drives the short
end. The Fed funds target range and the EFFR are the missing monetary-policy
context, and both are available from a keyless official source (FRED) that
fits the repo's "public API, no key" rule.

## Source evidence (probed live 2026-09-23)

| Endpoint | Result |
| --- | --- |
| `fred.stlouisfed.org/graph/fredgraph.csv?id=DFEDTARU` | ✅ HTTP 200, daily rows, latest non-empty `2026-09-23 = 4.00` |
| `fred.stlouisfed.org/graph/fredgraph.csv?id=DFEDTARL` | ✅ HTTP 200, daily rows, latest non-empty `2026-09-23 = 3.75` |
| `fred.stlouisfed.org/graph/fredgraph.csv?id=DFF` | ✅ HTTP 200, daily rows, latest non-empty `2026-09-21 = 3.88` (lagged) |
| `...?id=DFEDTARL,DFEDTARU,DFF&cosd=2026-09-14` | ⚠️ `cosd` is **ignored** in multi-series mode (returned ~26k rows from 2021) |
| `...?id=DFF&cosd=2026-09-14` | ✅ `cosd` **works for a single series** (10 rows) |

Payload facts that constrain the design:

- Single-series CSV: header `observation_date,<SERIES>`, one row per day in
  **ascending** date order, dates `YYYY-MM-DD`, values `4.00` / `3.75` / `3.88`.
- Series are **forward-filled daily** (weekends and holidays repeat the last
  value), so "latest" is always within one day for the target range.
- **Empty cells exist**: `DFF` had empty cells for the last 1–2 rows (not yet
  published). An empty cell is *missing*, never zero. The parser must take the
  **last non-empty row** per series.
- `cosd` limits the lookback for one series but is ignored when several ids
  share one request, so the client fetches **three single-series requests with
  `cosd`** rather than one multi-series request (which would be ~800 KB and
  grow every year).

## Scope (authorized)

- New package `internal/fed`: FRED client (3 single-series CSV fetches with a
  bounded `cosd` lookback, last-non-empty parsing, typed errors) and a
  once-per-calendar-day cache.
- Display: one line appended inside the Treasury section (after the spread):
  the target range (`3,75% – 4,00%`), the EFFR (`3,88%`) with its daily change
  in basis points, and the EFFR observation date. Spanish labels matching the
  domain (`Tasa FED`, `efectiva`), es-AR number formatting.
- Refresh through the existing per-source pipeline; one network fetch per
  calendar day; `r` forces a refetch; failure keeps the last good rate and
  renders the stale note, exactly like every other section.
- Tests: offline unit suite with `httptest`, plus a structural live check
  behind the `integration` build tag.
- README updated (indicator table, project layout, behavior notes).

## Constraints

- Go 1.27, stdlib HTTP/CSV only. No API keys, no new module dependencies.
- Generated artifacts in English. Reply language: Rioplatense Spanish.
- Preserve invariants: per-source error isolation, stale-data rendering with
  the last good value, one in-flight fetch at a time, es-AR formatting, no
  pinned display timezone.
- No commits unless the user asks.

## Decisions

- **Three single-series requests with `cosd`, not one multi-series request.**
  `cosd` is ignored in multi-series mode (probed), so the multi-series CSV is
  the full history (~800 KB and growing). Three `cosd`-bounded requests are
  ~2 KB total. `cosd` = `Now().AddDate(0, 0, -30)`, which is always longer than
  any plausible EFFR publication gap.
- **A rate is modelled by the last non-empty row per series.** `Rate.Date` is
  the observation date of the newest non-empty `DFF` row (the market rate's
  "as of", which can lag the policy rate by a day or two). `TargetLow` /
  `TargetHigh` are the newest non-empty `DFEDTARL` / `DFEDTARU` values (current
  policy). `EffectiveDeltaBp` is the EFFR change against its previous
  non-empty row (nil when there are fewer than two).
- **No FOMC/meeting calendar.** The cache refreshes at most once per calendar
  day (next local midnight), not on an FOMC schedule. This is the accepted
  limit, documented in the cache doc comment: a weekend session makes one
  request and sees the same values until a change is published.
- **Cache lives in `internal/fed`, injected as a fetcher** (mirrors `tesoro`):
  `FedFetcher func(ctx context.Context, force bool) (fed.Rate, error)`, so `r`
  travels with the refresh command and the model needs no extra flag.
- **Failure backoff reuses the tesoro ladder** (5m → 10m → 20m → 30m ceiling,
  reset on success), so an outage costs at most two requests per hour.
- **The fed line lives inside `renderTreasury`** (it is "the curve section",
  per the ask), rendered by a small `renderFed()` that owns its loading /
  stale / error states against `fedAt` / `fedErr`, so a Fed failure never
  blanks the curve and vice versa. It adds one line: `renderTreasury` grows
  from 9 to 10 lines, and `TestCurveSectionFitsInNineLines` is updated to 10.

## Planned surface

```go
// internal/fed
type Rate struct {
    Date             time.Time // newest non-empty DFF observation date (civil date)
    TargetLow        float64   // DFEDTARL newest non-empty value, percent
    TargetHigh       float64   // DFEDTARU newest non-empty value, percent
    Effective        float64   // DFF newest non-empty value, percent
    EffectiveDeltaBp *float64  // EFFR change vs previous non-empty row, bp; nil when unknown
}

type Client struct {
    HTTPClient   *http.Client
    BaseURL      string
    Now          func() time.Time
    LookbackDays int
}
func NewClient() *Client
func (c *Client) Fetch(ctx context.Context) (Rate, error)

func NewDailyCache(fetch func(context.Context) (Rate, error)) *Cache
func (c *Cache) Get(ctx context.Context, force bool) (Rate, error)

var ErrMalformed, ErrNoData error
type HTTPError struct{ StatusCode int; Status string }
```

Errors mirror `internal/tesoro`: `*HTTPError` for non-2xx, `ErrMalformed` for
an unparseable payload (bad header, missing `observation_date`, bad date/value),
`ErrNoData` for a well-formed but empty body or a window where a required
series has no non-empty row, wrapped network/context failures detectable with
`errors.Is`. The client sends a browser `User-Agent` (as `tesoro`/`bonos` do).

Rendering target (one line after the spread, when data is present):

```
  Tasa FED 3,75% – 4,00% · efectiva 3,88% ▲ +25 pb · 21-09-2026
```

Target range in rate style; EFFR in rate style; the delta arrow/colour follow
the yield convention (rising red `9`, falling green `10`, flat muted `241`)
reusing `curveDeltaStyle`; the date muted. An unknown delta renders `—`.

## Task checklist

- [x] T-01: `internal/fed` client — three `cosd`-bounded single-series fetches,
  ascending-order + last-non-empty parsing, empty-cell tolerance, EFFR delta vs
  previous non-empty row, typed errors. Tests (offline `httptest`): happy path
  with a lagged EFFR, empty cells skipped, all-empty required series →
  `ErrNoData`, header without rows, malformed CSV, missing date column,
  unparseable value, non-2xx, context cancellation, `cosd` present in the URL,
  browser UA. — checks: `go test -count=1 ./internal/fed` (red-first, then
  green).
  - Evidence: `internal/fed/client.go` + `client_test.go`. Green:
    `go test -count=1 ./internal/fed` = ok. Deviation: the endpoint rejects a
    browser User-Agent from a Go client (see Implementation notes); the test
    now pins a non-browser agent instead.
- [x] T-02: `internal/fed` cache — next-local-midnight schedule, `Get` with an
  injected clock (one fetch per calendar day, `force`, failure backoff + reset,
  `ErrNoData` handling). — checks: `go test -count=1 ./internal/fed`.
  - Evidence: `internal/fed/cache.go` + `cache_test.go`. Green, including the
    5→10→20→30-minute ceiling ladder and concurrent readers.
- [x] T-03: TUI wiring — `FedFetcher` injection, `dataMsg.rate`/`fedErr`,
  `Model.rate`/`fedErr`/`fedAt`, `refresh` fetch + `r` force, `renderFed()` line
  inside `renderTreasury`, per-source isolation and stale note, update
  `TestCurveSectionFitsInNineLines` to 10. — checks: `go test -count=1 ./internal/tui`.
  - Evidence: `internal/tui/tui.go` + `tui_test.go` (renamed
    `TestCurveSectionFitsInTenLines`). Green; package coverage 96.8%.
- [x] T-04: live integration check (extend `internal/tui/live_test.go`) +
  README + close this tracker. — checks: `gofmt -l .`, `go build ./...`,
  `go vet ./...`, `go test -count=1 ./...` under `TZ=UTC`,
  `TZ=America/New_York`, `TZ=America/Argentina/Buenos_Aires`, and
  `go test -tags=integration ./internal/tui -run TestLiveSourcesFetchRealData -v`.
  - Evidence: live run observed `fed 3.75-4.00% effective 3.88%` (PASS);
    `go test -count=1 ./...` green under all three timezones; coverage 90.4%
    for `internal/fed`. README updated.

## Acceptance criteria

- The dashboard shows, inside the Treasury section, the FOMC target range and
  the EFFR with its observation date, in es-AR format (`3,75% – 4,00%`,
  `3,88%`, `21-09-2026`).
- The Treasury section fits in ≤10 lines (was 9; +1 for the fed line).
- A Fed failure never blanks the curve or any other section, and vice versa;
  a failed refresh keeps the last good rate and shows the stale note.
- One fetch per calendar day; `r` forces one refetch; an outage backs off to at
  most two requests per hour.
- Empty FRED cells are never rendered as zero; the "latest" value is the last
  non-empty row per series.
- `gofmt -l .` empty, `go build ./...` and `go vet ./...` clean,
  `go test -count=1 ./...` green under the three timezones above, integration
  check passing against the live endpoint.

## Blockers

- None.

## Implementation notes

- **No browser User-Agent for FRED (deviation from the planned surface).** The
  endpoint was re-probed on 2026-09-23 before writing the client. FRED's edge
  answers a browser `User-Agent` from a Go client with an HTTP/2
  `stream error ... INTERNAL_ERROR; received from peer`, while curl and
  non-browser agents are served the CSV. Adding `Accept`/`Accept-Language` did
  not help from Go, so `internal/fed` leaves the transport's default user agent
  in place. The test asserting a browser UA was inverted to pin that.
- **`renderFed()` is rendered whatever the curve's own state is.** The
  early returns for a curve that never loaded are gone: the header, the curve
  state and the Fed line are independent, so a curve failure never hides the
  rate and the rate failure never hides the curve. Healthy state is still 10
  lines.
- **`lastSuccessAt` now includes `fedAt`**, so the footer's "actualizado"
  reflects a Fed-only refresh like it does for every other source.
- **Test renamed** `TestCurveSectionFitsInNineLines` →
  `TestCurveSectionFitsInTenLines` (asserts 10).

## Review closure

- Pending (native review is the user's switch to flip).

## Next step

- Await the user's decision on commits / branch / native review.
