# Feature: us-treasury-curve

Tracked in repo: `odd/tasks/us-treasury-curve.md` | Engram mirror: `odd/us-treasury-curve/tasks`

## Objective

Add the US Treasury par yield curve to the dashboard: the 14 published tenors (1M, 1.5M, 2M, 3M, 4M, 6M, 1Y, 2Y, 3Y, 5Y, 7Y, 10Y, 20Y, 30Y) with each level, its daily change in basis points, the publication date, and the 10Y−2Y spread. Second data domain in the app after Argentine macro.

## Problem / Why

The dashboard covers Argentina only. US Treasury yields are the global risk-free benchmark and the 10Y−2Y spread is the standard recession signal. The source is official, keyless, and small.

## Source evidence (probed live 2026-09-22)

| Endpoint | Result |
| --- | --- |
| `home.treasury.gov/.../daily-treasury-rates.csv/{year}/all?type=daily_treasury_yield_curve&field_tdr_date_value={year}&page&_format=csv` | ✅ HTTP 200, 14.7 KB, 181 rows for 2026, newest row `09/21/2026`: 1Y 4.45 · 2Y 4.76 · 5Y 4.83 · 10Y 4.96 · 20Y 5.33 · 30Y 5.29 |
| `home.treasury.gov/.../pages/xml?data=daily_treasury_yield_curve&field_tdr_date_value={year}` | ⚠️ HTTP 200 but 280 KB (Atom feed) for the same data — rejected on payload size |
| `.../daily-treasury-rates.csv/2027/...` (year with no data) | ⚠️ HTTP 200 with an **empty body** — not an error. Year-boundary case |

Payload facts that constrain the design:

- Header is `Date,"1 Mo","1.5 Month","2 Mo","3 Mo","4 Mo","6 Mo","1 Yr","2 Yr","3 Yr","5 Yr","7 Yr","10 Yr","20 Yr","30 Yr"` — 15 fields.
- **`1.5 Month` is a new column.** Parsing by index would silently read crossed tenors on the next column change. The parser MUST resolve columns by header name, never by position.
- Rows arrive in **descending date order**, dates in `M/D/YYYY`. The newest row is row 0 and the previous business day is row 1.
- The payload carries **no variation field**: the daily change must be derived from the previous business-day row.
- Treasury publishes roughly once per business day at ~16:00 ET.

## Scope (authorized)

- New package `internal/tesoro`: CSV client (fetch + header-name-keyed parse + typed errors) and a business-day publication cache.
- All 14 tenors, ascending tenor order, per-tenor `—` when a column is absent (Treasury adds and retires columns over time).
- Daily change in basis points per tenor vs the previous business day (nil → `—` when unavailable).
- The 10Y−2Y spread in basis points.
- Dashboard section rendered in two columns of 7 rows (single column would add ~16 lines and push the whole dashboard out of an 80×24 terminal).
- Refresh through the existing per-source pipeline; one network request per business day; `r` forces a refetch.
- Tests: offline unit suite with `httptest`, plus a live integration check behind the `integration` build tag.
- README updated (indicator table, layout table, behavior notes).

## Constraints

- Go 1.27, stdlib HTTP/CSV only. No API keys, no new module dependencies.
- Generated artifacts in English. Reply language: Rioplatense Spanish.
- Preserve existing invariants: per-source error isolation (a failing source never blanks another section), stale-data rendering with the last good values, one in-flight fetch at a time, es-AR number formatting, no pinned display timezone.
- No commits unless the user asks.

## Decisions

- **Cache lives in `internal/tesoro`, injected as a fetcher.** The TUI keeps its existing per-source staleness machinery untouched; the cache is independently unit-testable.
- **Display timezone stays the system's**, but the *publication schedule* is an external fact and uses `America/New_York`. `time.LoadLocation` is backed by an embedded `time/tzdata` import so the binary does not depend on the host having a tzdata package. This is a deliberate, documented exception to the "no pinned timezone" rule and applies only to the cache schedule, never to rendering.
- **Publication schedule is a pure function, not a scattered TTL.** `NextCheckAt(now, rowDate, loc)` returns the next instant the cache should re-check, and is table-tested against: before publication, after publication with today's row, after publication without today's row (late publish), weekend, and ET-midnight rollover.
- **Late-publish guard.** If the newest row we hold is older than today's ET date after 16:00 ET, the cache re-checks every 15 minutes instead of waiting a full day (Treasury does publish late), bounded by the next ET midnight so a holiday evening cannot turn into an all-night retry loop.
- **Year-boundary fallback.** Fetch the local year's CSV; if it has no rows, or fewer than two rows (so the previous business day lives in the prior year's file), fetch the previous year once as well.
- **Failure keeps the last good curve** and schedules a short retry; the error surfaces in the section, matching the other sources.
- Cache invalidation for `r`: the fetcher signature carries intent — `func(ctx, force bool) (Curve, error)` — so `refresh(force)` threads it without extra state on the model.

## Planned surface

```go
// internal/tesoro
type Point struct {
    Tenor   string   // canonical key = Treasury header, e.g. "10 Yr"
    Label   string   // display label, e.g. "10Y"
    Yield   float64  // par yield in percent, e.g. 4.96
    DeltaBp *float64 // change vs previous business day, basis points; nil when unknown
}

type Curve struct {
    Date   time.Time // Treasury business date of the newest row (civil date, zone-independent)
    Points []Point   // ascending tenor order
}

func (c Curve) SpreadBp(long, short string) (float64, bool) // 10Y − 2Y, basis points

type Client struct{ HTTPClient *http.Client; BaseURL string; Now func() time.Time }
func NewClient() *Client
func (c *Client) Fetch(ctx context.Context) (Curve, error)

func NewPublisherCache(fetch func(context.Context) (Curve, error)) *Cache
func (c *Cache) Get(ctx context.Context, force bool) (Curve, error)
func NextCheckAt(now, rowDate time.Time, loc *time.Location) time.Time

var ErrMalformed, ErrNoData error
type HTTPError struct{ StatusCode int; Status string }
```

Errors mirror `internal/riesgo`: `*HTTPError` for non-2xx, `ErrMalformed` for an unparseable payload, `ErrNoData` for a well-formed but row-less payload, wrapped network/context failures detectable with `errors.Is`.

Rendering target (two columns, one row per tenor, level + Δbp in pb with rising = red / falling = green, publication date in the section header, spread line under the columns).

## Task checklist

- [x] T-01: `internal/tesoro` client — CSV fetch, header-name-keyed column resolution, M/D/YYYY row dates, descending-order handling, previous-business-day delta, year-boundary fallback, typed errors. Tests (offline `httptest`): happy path, reordered header columns, added/removed tenor column, missing/blank cell for one tenor, single-row year falling back to the previous year, empty year body, malformed body, non-2xx, context cancellation. — checks: `go test -count=1 ./internal/tesoro` red-first evidence, then green
  - RED: package absent, so the suite did not build — `internal/tesoro/client_test.go:125:53: undefined: Client` plus `undefined: Tenors`, `Curve`, `Point` (12 errors, `FAIL ... [build failed]`).
  - GREEN: `go test -count=1 ./internal/tesoro` → `ok metruchinas/internal/tesoro 0.018s`; 32 top-level tests / 69 passing assertions; coverage `95.1% of statements`.
  - Uncovered lines are defensive only: nil clock/location defaults, `NewClient` field fallbacks in `get`, URL/build error branches, and the package-level `Fetch` wrapper (no injection point, same as `riesgo.Fetch`/`bonos.Fetch`).
  - **Correction (third pass, F1): a renamed header no longer degrades the section.** The parser resolved columns by name but never checked that *any* tracked column matched, so a payload whose tenor columns were renamed parsed successfully into a valid date and an empty curve, and the dashboard rendered a dated section of fourteen dashes with no error and no stale note — indistinguishable from a day Treasury published nothing. `checkTenorColumns` now rejects a header carrying none of the 14 tracked columns and one carrying a tracked column twice (previously silent last-wins: two `10 Yr` columns reported the second value, measured `10 Yr = 5.55`), `Fetch` rejects a curve whose newest row publishes no tenor at all, and `headerSummary` bounds the header it echoes (8 cells, 24 runes each, plus the column count). RED: `TestFetchFailsWhenNoTenorColumnMatches` → `Fetch() error = nil with 0 published tenors and date 2026-09-21 00:00:00 -0300 -03, want ErrMalformed`; `TestFetchFailsOnDuplicateTenorColumn` → `Fetch() error = nil with 10 Yr = 5.55, want ErrMalformed`; `TestFetchFailsWhenNoRowPublishesATenor` → `Fetch() error = nil with 0 points and date ..., want ErrMalformed`; `TestFetchBoundsTheHeaderInTheDiagnosis` → `Fetch() error = <nil>, want ErrMalformed`. GREEN: all four pass, plus `TestFetchToleratesWhitespaceAroundHeaderCells`. `ErrNoData` stays reserved for a body with no rows: every new case asserts `!errors.Is(err, ErrNoData)`. The user-visible result is `no disponible: tesoro: malformed response payload: none of the 14 tracked tenor columns appear in header "Date", "1Mo", "3Mo", "1YR", "10YR", "30YR"` instead of fourteen dashes (a later refresh keeps the last good curve and shows that text in the stale note).
  - One part of that finding does **not** reproduce: a header cell padded with spaces (`" 10 Yr "`) matches, because every header cell is `TrimSpace`d before the lookup. `TestFetchToleratesWhitespaceAroundHeaderCells` now pins that tolerance.
  - **Deviation (live-driven):** the endpoint throttles a non-browser `User-Agent`. `curl` as `Go-http-client/1.1` took 16.4 s and 20.3 s on two identical requests while the same URL as a browser took 0.38 s and 0.48 s, and Go's default agent exceeded the 10 s client timeout. The client now sends a browser `User-Agent` (as `internal/bonos` already does) and `TestFetchSendsABrowserUserAgent` pins it.
- [x] T-02: `internal/tesoro` cache — `NextCheckAt` table tests (before/after publication, late publish, weekend, ET-midnight rollover, DST transition), `Get` behaviour with an injected clock (one fetch per business day, force, failure retry), and `-race` clean. — checks: `go test -count=1 -race ./internal/tesoro`
  - RED: `undefined: NextCheckAt`, `undefined: publicationTimezone`, `undefined: publicationHour`, `undefined: newCache` (11 errors, `FAIL ... [build failed]`).
  - GREEN: `go test -count=1 ./internal/tesoro` → `ok ... 0.018s`. `NextCheckAt` is table-tested on 18 cases (before/at/after the 16:00 release, late publish and its midnight bound, ET-midnight rollover, weekend, year rollover, nil location) plus both DST transitions asserted by zone offset (EDT -4h / EST -5h). `Get` is tested with an injected clock for one fetch per business day, forced refresh, the failure retry window, force bypassing that window, and `ErrNoData` handling.
  - **Correction (bounded, second pass): the release schedule must skip non-business days.** As first written, `NextCheckAt` computed the next release as "tomorrow 16:00" unconditionally and ran the late-publish retry on any day, so a Saturday scheduled a Saturday 16:00 release and then retried every 15 minutes until midnight. `nextReleaseAfter` now returns the next **Monday-to-Friday** release strictly after the clock (Friday evening schedules Monday), and the bounded late-publish retry applies only when the publication-zone civil day is Mon-Fri; a weekend day returns the next weekday release with no retry at all. RED: 8 schedule cases plus `TestNextCheckAtKeepsTheDSTOffsetCorrect` failed against the previous function (`NextCheckAt() = 2026-09-19 17:15:00 -0400 EDT, want 2026-09-21 16:00:00 -0400 EDT`), and `TestCacheMakesOneRequestAcrossAWeekendWindow` failed with `source requests from Friday 15:00 to Monday 09:00 = 67, want 1`. GREEN: `go test -count=1 ./internal/tesoro` → `ok`; the table now has 24 cases, including Saturday morning, Saturday evening, Sunday after midnight, Sunday evening, Friday evening after Friday's row, Friday after the release with only Thursday's row, and the Friday/Monday either side of each DST transition. The two weekend cases the plan named are gone as written ("weekend evening retries only until midnight" is now "Saturday evening has no release to retry", asserting Monday).
  - **Measured weekend cost.** The dashboard heartbeat was replayed at its real 30-second interval across Friday 15:00 → Monday 09:00 New York time with a warm cache, counting source requests: **the previous schedule made 67** (Friday 16:00, then every 15 minutes from 16:00 to 23:45 on both Saturday and Sunday plus the two midnight checks), **the corrected schedule makes 1** (Friday's 16:00 release). Both numbers come from the same harness — the previous figure was measured by temporarily restoring the old algorithm in `cache.go`, then restoring the file byte-identical.
  - Package figures after the third pass: 42 top-level tests / 86 passing assertions in `internal/tesoro`; `go test -count=1 -cover ./internal/tesoro` → `coverage: 96.0% of statements` (and `internal/tui`: 48 top-level tests / 76 assertions, `96.4%`).
  - **Correction (third pass, F2): the failure retry is capped.** A source that stayed down was re-asked every `failureRetryInterval` for as long as the dashboard ran. `failureBackoff` now doubles the interval per consecutive failure (5, 10, 20, then a 30-minute `failureRetryCeiling`) and the first success resets it. RED (stage 1: `undefined: failureBackoff`, `undefined: failureRetryCeiling`; stage 2: the same seam wired to the old fixed cadence) measured with the 30-second heartbeat harness: **289 requests in a 24-hour outage and 8641 in a 30-day outage**, first gaps `[5m 5m 5m 5m 5m]`; `TestFailureBackoffDoublesToACeiling` and `TestCacheBacksOffOnConsecutiveFailures` also failed. GREEN: **50 requests (24 h) and 1442 (30 days)**, first gaps `[5m 10m 20m 30m 30m]`, asserted by `TestCacheBoundsOutageRequests` against the ceilings implied by the design (≤52 and ≤1444), with `TestCacheResetsTheFailureBackoffOnSuccess` pinning that a success restarts the progression. README's "once per business day" claim is now qualified by both bounded exceptions (late publication and outage backoff). A second independent verification pass confirmed the ladder, the cap, the reset and both outage counts by driving the real cache, and found that the outage exception was still missing from the README while this tracker claimed it was documented; the README bullet now states the 5/10/20/30-minute progression, the two-requests-per-hour ceiling and the up-to-30-minute recovery latency, with `r` as the escape hatch.
  - **Holiday behaviour is preserved and documented.** There is still no holiday calendar: a weekday holiday (2027-01-01, a Friday, is pinned in the table) is treated as a publication day, so its release passes without a row and the bounded retry runs that evening until publication-zone midnight. That is the accepted limit recorded in the `NextCheckAt` doc comment.
  - **Not run: `go test -count=1 -race ./internal/tesoro`, and there is no race-detector evidence for this change.** This host has no C toolchain at all (`CGO_ENABLED=0`, and `gcc`, `cc` and `clang` are all absent), and `-race` requires cgo: `go test -race ...` → `go: -race requires cgo; enable cgo by setting CGO_ENABLED=1`, and with `CGO_ENABLED=1` → `cgo: C compiler "gcc" not found`. The gap is recorded rather than papered over:
    - Inspection: `Cache.mu` guards every mutable field (`curve`, `fetchOK`, `lastErr`, `failures`, `nextCheck`), each read and write of them happens inside the lock, and the network call is deliberately made outside it. `Now` and `loc` are not guarded because they are set once at construction and never written again; `Get` may read `Now` without the lock, which is safe only under that convention.
    - `TestCacheSurvivesConcurrentReaders` is **not** a substitute for the detector. It runs 8 goroutines x 5 reads and asserts no caller observes a partially built curve, which would catch a torn read but not a data race as such.
    - `Cache.Get` has **no single-flight guard**: two concurrent callers that both see a stale schedule will both call the source. The dashboard's one-in-flight gate (`m.refreshing`, tested) is the only thing upholding the documented single-caller contract. Any future caller outside the dashboard must serialize its own calls.
    - No machinery was added for this: adding a single-flight guard would be a behaviour change with its own review, not a fix for a missing detector.
- [x] T-03: TUI wiring — `TreasuryFetcher` injection, `refresh(force bool)`, per-source error/stale handling, two-column curve section, spread line, footer/`r` force path. Tests: success, isolated failure, stale note, missing tenor, spread sign, cache-hit path. — checks: `go test -count=1 ./internal/tui`
  - RED: `m.fetchTreasury undefined`, `undefined: TreasuryFetcher`, `too many arguments in call to m.refresh`, `msg.curve undefined` (build failure).
  - GREEN: `go test -count=1 ./internal/tui` → `ok metruchinas/internal/tui 0.159s`; 48 top-level tests / 73 passing assertions; coverage `96.4% of statements`.
  - **Correction (third pass, F4): every delta assertion is checked.** Nine unguarded `*DeltaBp` dereferences aborted the package on a missing delta. They now go through `wantDelta(t, curve, tenor)`. Mutation experiment (deleting the `point.DeltaBp = &delta` assignment, identical harness both ways): before — `panic: runtime error: invalid memory address or nil pointer dereference`, only **15 passed / 1 failed** of the 42 top-level tests reported, ~26 never ran; after — **37 passed / 5 failed**, every test reported, each failure reading `tenor "10 Yr" has no delta, want a change against the previous business day`. `client.go` and `client_test.go` were restored byte-identical after each stage, with the suite green.
  - **Correction (third pass, F5): the colour convention is now observed, not identity-checked.** `TestCurveDeltaColorFollowsTheYieldConvention` asserts the style's foreground colour (red `9`, green `10`, muted `241`) instead of `reflect.DeepEqual` against the style variable. Mutation experiment: with `upStyle` repainted `lipgloss.Color("12")`, the previous identity assertion still passed (`--- PASS: TestTmpOldIdentityAssertion`) while the new assertion failed with `curveDeltaStyle(3) foreground = 12, want 9`.
  - **Correction (third pass, F6): the TUI cache test now crosses a release boundary.** `TestRefreshFetchesTheCurveOncePerBusinessDay` (renamed from `...WithinTheSameBusinessDay`) drives four refresh cycles at 14:00, 15:59, 16:01 and 16:30 EDT on a Monday and requires request counts 1, 1, 2, 2. Mutation experiment: with the schedule replaced by a fixed 15-minute TTL it fails (`cycle 1 at 2026-09-21T19:59:00Z: source requests = 2, want 1`), and a longer TTL would fail the other way.
  - **Correction (third pass, F7): this tracker's own claims were re-measured.** The right column starts at display cell **22**, not 24, and the test now pins that absolute value. The dashboard is **27 visible lines** with the two-bond fixture (28 counting the empty element after the final newline) and **31** with six bonds, not the 29 previously recorded. Both were re-measured with a temporary probe that was then removed.
  - Cache-hit path is covered end to end: `TestRefreshServesTheCurveFromTheCacheWithinTheSameBusinessDay` wires the real `tesoro.Cache` behind an injected clock and proves three refresh cycles cost exactly one source request.
  - **Correction (bounded, second pass): the tenor label follows the dashboard's es-AR rule.** The `1.5 Month` tenor was labelled `1.5M`, which is the only dot decimal separator on a dashboard that prints `4,45`. The display `Label` is now `1,5M`; the canonical `Key` is unchanged (`1.5 Month`, the exact Treasury header, still the only thing the parser resolves columns by). RED: `Tenors()[1] = {Key:1.5 Month Label:1.5M}, want {Key:1.5 Month Label:1,5M}` and `curve rows use a dot decimal separator in the tenor label`. GREEN: both packages `ok`. The label is still four display cells wide, so the column geometry is unchanged and the alignment assertions were not loosened.
  - Layout evidence: the section renders in exactly 9 lines (`TestCurveSectionFitsInNineLines`) and the right column starts at display cell **22** on every row, with and without an unpublished tenor (`TestCurveColumnsAlignAcrossEveryRow`). The cell is the left cell's geometry (2 indent + 4 label + 2 + 5 yield + 2 + 4 delta = 19 cells) plus the 3-cell gap, and the test now pins that absolute value instead of taking row 0 as its own reference — the earlier "cell 24" claim in this tracker was wrong.
- [x] T-04: Live integration check against the real endpoint (extend `internal/tui/live_test.go`). — checks: `go test -tags=integration ./internal/tui -run TestLiveSourcesFetchRealData -v`
  - **This is a smoke test that now also pins structure**, not a value check: levels change every business day, so nothing in it asserts a yield. As first written it only checked `0 < yield <= 25` and never looked at the daily changes, so a curve with every delta nil, or one of the wrong length, would have passed. It now asserts that all 14 tracked tenors arrive in order with their display labels, that every delta is non-nil, that the publication date is non-zero and not in the future, and that the 10Y-2Y spread equals the 10Y minus the 2Y levels of that same curve. `-5.55`-style levels are never pinned.
  - Structural bite proven by mutation: with the `point.DeltaBp` assignment deleted, the live run fails with `1 Mo: no daily change against the previous business day` for all 14 tenors instead of passing.
  - `--- PASS: TestLiveSourcesFetchRealData`, `ok metruchinas/internal/tui`. Run four times during the work: 0.88 s, 0.94 s, 0.945 s and 1.03 s, with identical values every time (1.20 s and later runs after the structural assertions, identical values).
  - Live curve, dated `21-09-2026`, all 14 tenors: 1M 3.96 (-1 pb) · 1,5M 4.02 (+4) · 2M 4.10 (+0) · 3M 4.17 (+3) · 4M 4.26 (+2) · 6M 4.27 (+3) · 1Y 4.45 (+1) · 2Y 4.76 (+0) · 3Y 4.82 (-1) · 5Y 4.83 (-3) · 7Y 4.89 (-4) · 10Y 4.96 (-5) · 20Y 5.33 (-5) · 30Y 5.29 (-5); spread 10Y-2Y = **+20 pb**.
  - The six levels the plan of record pinned (1Y 4.45, 2Y 4.76, 5Y 4.83, 10Y 4.96, 20Y 5.33, 30Y 5.29) reproduce exactly, so the header-name mapping and the previous-business-day delta are confirmed against the real payload.
  - The same run also reports 7 quotes (4 displayed), riesgo 533 (1.72%), and 6 bonds: one refresh cycle now exercises four sources. The riesgo figure is a point-in-time capture and drifts with the live source: later runs reported `533 (0.00%, equal)`. The pinned curve levels and the `+20 pb` spread are the values that reproduce exactly.
- [x] T-05: README + this tracker closed. — checks: `gofmt -l .` empty, `go build ./...`, `go vet ./...`, `go test -count=1 ./...` green under `TZ=UTC`, `TZ=America/New_York`, and `TZ=America/Argentina/Buenos_Aires`
  - `gofmt -l .` → no output. `go build ./...` → clean. `go vet ./...` → clean.
  - `go test -count=1 ./...` → `ok` for `internal/bonos`, `internal/dolar`, `internal/riesgo`, `internal/tesoro`, `internal/tui` (no test files in the root package).
  - Same result under `TZ=UTC`, `TZ=America/New_York` and `TZ=America/Argentina/Buenos_Aires`; also spot-checked under `TZ=Asia/Kolkata` (+05:30) and `TZ=Pacific/Kiritimati` (+14).
  - README updated: indicator table, quick path, keys, behavior notes (publication cache, header-name mapping, basis-point changes and the inverted-curve flag, year-boundary fallback, the `America/New_York` exception), project layout, and the terminal-height note.
  - Re-run after the two bounded corrections (business-day schedule under T-02, `1,5M` label under T-03): identical results — `gofmt -l .` empty, `go build ./...` and `go vet ./...` clean, every package `ok` under `TZ=UTC`, `TZ=America/New_York` and `TZ=America/Argentina/Buenos_Aires`, `coverage: 95.2% of statements` for `internal/tesoro`, and `TestLiveSourcesFetchRealData` PASS in 1.51 s with the same live values.

## Implementation notes (recorded while running T-01..T-05)

Edit surfaces touched: `internal/tesoro/` (new package), `internal/tui/tui.go`, `internal/tui/tui_test.go`, `internal/tui/live_test.go`, `README.md`, this tracker. `go.mod`/`go.sum` unchanged: the implementation is stdlib plus the existing bubbletea/lipgloss.

Deviations from the plan:

1. **The client sends a browser `User-Agent`.** Live finding: the endpoint throttles a non-browser agent. The identical URL answered in 16.4 s and 20.3 s as `Go-http-client/1.1` and in 0.38 s and 0.48 s as a browser, so Go's default agent exceeded the 10 s client timeout and the live check failed. This mirrors what `internal/bonos` already does; `TestFetchSendsABrowserUserAgent` pins it.
2. **A published tenor is modelled by presence, not by a flag.** The planned `Point.Yield float64` has no presence marker, so `Curve.Points` holds only the tenors the payload published for that date: a blank cell, a retired column, or a not-yet-added column is absent, and the layout renders `—` in that slot. `DeltaBp *float64` stays explicit as planned. Two small helpers were added to the planned surface: `Tenors()` (the canonical order and labels, so the TUI lays out a fixed 14-slot grid instead of duplicating the tenor list) and the exported `Cache.Now` field (so the publication schedule can be injected from TUI-level tests, mirroring `Client.Now`).
3. **`go test -count=1 -race ./internal/tesoro` could not run.** This host has no C toolchain (`CGO_ENABLED=0`; `gcc`, `cc` and `clang` absent) and `-race` requires cgo. There is no race-detector evidence for this change, and `Cache.Get` has no single-flight guard: the dashboard's one-in-flight gate is the only thing upholding the single-caller contract, so any other caller must serialize its own calls. See the T-02 note for the full statement.
4. **Error strings:** sentinel wraps use `fmt.Errorf("%w: ...", ErrMalformed, ...)` instead of riesgo's `fmt.Errorf("riesgo: %w: ...")`, which prints the package prefix twice. The stage errors (`tesoro: request:`, `tesoro: read response:`) keep the house shape.

Risks the plan does not cover:

1. **The 80x24 premise does not hold.** The curve section itself is 9 lines as required, but the dashboard was already 21 lines with the six real bonds and is now **31** (measured: 27 visible lines with the two-bond test fixture — 28 when the empty element after the final newline is counted — and 31 visible lines, 32 with that element, with six bonds). The widest line is 60 display cells, so width is fine; height is not. Compressing the dolar (four rows) and bonds (six rows) sections into two columns would recover 5 lines, but that is a separate change with its own review.
2. **Holidays are still invisible to the schedule** (weekends no longer are: see the T-02 correction). A weekday holiday looks like a late release, so the bounded retry runs that evening until publication-zone midnight — up to ~32 requests, about 480 KB, on a handful of days a year. Closing that needs a holiday calendar, which the plan rules out; the retry is bounded, so the cost is one evening rather than a day of stale data.
3. **The 10 s client timeout is the only thing standing between the throttle and a stale section.** Under a browser `User-Agent` the endpoint answered in 0.38 s to 1.03 s across every live run, so the margin is large today, but the throttle behaviour is undocumented and can change without notice. A failure degrades to stale rendering, never to a blank section.
4. **Pre-existing inaccuracies observed and deliberately not touched** (out of scope for this feature): the README indicator table still lists the bonds source as `mercados.ambito.com/bono/{TICKER}/variacion` while `internal/bonos` and the project layout use `compararfondos.com.ar`; and `DefaultRefreshInterval`'s comment says "both sources" although the dashboard now has four.

## Decisions taken with the user (2026-09-22)

1. **The browser `User-Agent` stays.** Measured: the same URL answers in 0.38 s as a browser and in 16.4 s / 20.3 s as `Go-http-client/1.1` (exceeding the 10 s client timeout). Dropping it while raising the timeout to 30 s would let one slow source consume the 25 s cycle budget and degrade the whole dashboard instead of one section. The trade-off — identifying as a browser against a public, keyless endpoint — was put to the user and accepted; `internal/bonos` already does the same.
2. **The dashboard height is a follow-up, not part of this candidate.** The section meets its 9-line criterion; the total-height problem predates this feature.
3. **One native review for the whole feature** (measured ≈3,617 added lines, of which ≈2,334 are test-only), rather than two chained review slices.

## Follow-ups (explicitly out of this candidate)

1. **Dashboard height.** Compress the dólar (4 rows) and bonds (6 rows) sections into two columns to recover ~5 of the 31 lines. Needs its own candidate, layout tests, and review.
2. **The README bonds-source row is wrong today, independent of this feature.** The indicator table still lists `mercados.ambito.com/bono/{TICKER}/variacion` as the source for the GD bonds while `internal/bonos` and the README's own project layout use `compararfondos.com.ar`. Pre-existing; deliberately not touched here.
3. **`DefaultRefreshInterval`'s comment says "both sources"** while the dashboard now has four. Pre-existing; deliberately not touched here.
4. **Holiday calendar.** A weekday holiday still costs one evening of bounded retries (~32 requests). Closing it needs a holiday source or table.

## Acceptance criteria

- The dashboard shows all 14 tenors with level, Δbp, and the Treasury publication date; the curve section fits in ≤9 lines and the widest line stays at 60 display cells.
- The **whole dashboard** does not fit a 24-line terminal: it grew from 21 lines with the six real bonds to 31. This feature does not claim otherwise — compressing the dólar and bonds sections into two columns is tracked as a follow-up above instead of being folded in here.
- The 10Y−2Y spread renders in basis points and is visibly flagged when negative (inverted curve).
- A reordered or extended CSV header still maps every tenor correctly (header-name lookup, proven by test).
- On the first business day of a year, the Δbp comes from the previous year's newest row instead of rendering `—`.
- A session makes one request per business day; `r` forces one refetch; a failed fetch keeps the last good curve and retries.
- A failing Treasury source never blanks the other three sections, and vice versa.
- `gofmt -l .` empty, `go build ./...` and `go vet ./...` clean, `go test -count=1 ./...` green under the three timezones above, integration check passing against the live endpoint.

## Blockers

- None.

## Review closure

- **Native review CLOSED (approved on the last admitted event, authority burned).** Lineage `review-d0b74d959b8f2740`, target `sha256:51fbf7de35f2fc3bfa72e598612a8c3d156d128723b3ca567c74d395f7dcf23e`, tier medium, one lens (`review-reliability`, order 0), 9 changed paths, 3,668 changed lines, correction budget 200 — unused (no BLOCKER/CRITICAL finding). Flow: inspect → intended-untracked selection (5 paths) → `managed_assets_outdated` stop → `gentle-ai sync` → selection re-applied → START → reviewer capture (`approved`, store revision `sha256:f5f00b15c5bb0884de86e9d70947cd7980bbcd81f7bae132768225d032a6b825`) → acknowledgement `gentle-ai.review-acknowledged/v1`, `authority: burned`.
- Advisory findings (all informational, none blocks, do NOT re-review this candidate for them): `R3-001` (`internal/tesoro/cache.go:134-169`, WARNING), `R3-002` (`internal/tesoro/client.go:419-422`, WARNING), `R3-003` (`internal/tesoro/client.go:475-479`, SUGGESTION), `R3-004` (`internal/tui/live_test.go:60-76`, SUGGESTION). The native store keeps the state machine, not the finding prose, so the flagged line ranges are the durable evidence.
- **The review needed two attempts.** The first reviewer run was killed by the Pi host relay bound: `unachievable_lens_slot` / `relay_transport_bound_exceeded`, 1,029,467 ms elapsed against a 1,058,876 ms bound for a 185,103-byte materialized prompt. That bound is derived as a 15-minute floor plus 15 minutes per MiB, clamped to 2 hours, so the floor dominates and re-slicing the candidate would have bought roughly 30 seconds. Raising `GENTLE_PI_REVIEW_RELAY_PI_TIMEOUT_MS` to `3600000` for the Pi host process (a restart, since a tool call cannot set its own process environment) let the same slot complete on the first retry after `withdraw`.
- **Operational lesson (cost a failed withdraw).** `gentle-ai review capture-unachievable` failed with `operation_outcome_unknown` and the misleading cause "managed reviewer assets are outdated; run `gentle-ai sync`" while `sync` correctly reported everything up to date. Root cause: version skew — the `gentle-ai` on `PATH` is **3.4.0**, while this Pi package (gentle-pi 2.7.0) owns the lineage through its managed binary at `…/gentle-pi/.gentle-ai/v2.9.1/gentle-ai`. Native review maintenance must run with the **managed** binary that owns the lineage. A defect report was written to `.git/gentle-ai/defect-reports/` (native state, not repository content).

## Next step

- Delivery: work-unit commits on request (never automatic).
- Follow-ups, all out of this candidate's scope: the four advisory findings above; the dashboard-height compression; the stale README bonds-source row; and the `America/New_York` holiday calendar if the bounded holiday-evening retries ever matter.
