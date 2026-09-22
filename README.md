# metruchinas

Market indicators in your terminal: USD/ARS quotes, the Argentine country-risk index, sovereign GD bond parities, and the US Treasury par yield curve, refreshed live from public APIs.

## What it shows

| Indicator | Detail | Source |
|-----------|--------|--------|
| Dólar oficial | Buy / sell rate | [dolarapi.com](https://dolarapi.com/docs/) |
| Dólar blue | Buy / sell rate | dolarapi.com |
| Dólar MEP (bolsa) | Buy / sell rate | dolarapi.com |
| Dólar CCL (contado con liquidación) | Buy / sell rate | dolarapi.com |
| Riesgo país | EMBI Argentina index + daily variation | [mercados.ambito.com](https://mercados.ambito.com) |
| Bonos soberanos GD (paridad) | GD29, GD30, GD35, GD38, GD41, GD46 with parity %, technical value, and USD price via CCL conversion | mercados.ambito.com/bono/{TICKER}/variacion |
| Curva del Tesoro EE. UU. | 14 tenors (1M to 30Y) with level, daily change in basis points, publication date, and the 10Y-2Y spread | [home.treasury.gov](https://home.treasury.gov/resource-center/data-chart-center/interest-rates/TextView?type=daily_treasury_yield_curve) |

All sources are public and need no API key.

## Quick path

```bash
go run .
```

Expected result: an alternate-screen dashboard with the four USD rates, the country-risk index, the GD bond table, and the US Treasury curve. It refetches every source every 30 seconds (the curve is served from a publication cache, see below); `q` quits.

```bash
# on-demand checks
go build ./...
go vet ./...
go test ./...          # offline suite (httptest servers, no network)

# live end-to-end check against all real APIs
go test -tags=integration ./internal/tui -run TestLiveSourcesFetchRealData -v
```

## Keys

| Key | Action |
|-----|--------|
| `r` | Refresh every source now, forcing the Treasury curve to refetch |
| `q`, `Esc`, `Ctrl+C` | Quit |

## Behavior you can rely on

- **Partial failures never blank the dashboard.** Each source reports its own error; if Ámbito is down, the USD rates stay on screen (and vice versa) with a red message in the failing section.
- **The Treasury curve is fetched once per business day.** Treasury releases roughly once per business day, Monday to Friday, at 16:00 New York time, so the dashboard asks the endpoint once per publication cycle and answers every other refresh from memory; `r` forces a refetch. The schedule follows the business week: a Friday evening, a Saturday and a Sunday all wait for Monday's release, so a session running from Friday 15:00 to Monday 09:00 makes one request. Two bounded exceptions qualify the once-per-day claim. If the newest row we hold is still older than the current business day after the release hour, the cache retries every 15 minutes until local midnight instead of waiting a whole day and instead of looping all night; holidays are not known to the schedule, so a weekday holiday looks like a late release and costs one evening of retries. If the source fails, the retry interval doubles on each consecutive failure (5, 10, 20, then a 30-minute ceiling) and the first success resets it, so a long outage costs at most two requests per hour instead of 288, and the section can stay on the last good curve for up to 30 minutes after the source recovers — `r` forces an immediate check.
- **Curve columns are resolved by header name, never by position.** Treasury inserts and retires tenor columns (`1.5 Month` was inserted between `1 Mo` and `2 Mo`), so a parser reading by position would silently report one tenor's yield as another's. A tenor that is missing, retired, or blank keeps its slot and renders as `—`; a header that no longer carries any tracked tenor column is reported as an error instead, so the section can never degrade into fourteen dashes behind a valid-looking date.
- **Curve changes are basis points against the previous business day.** The payload carries no variation field: the change is derived from the row above. Rising yields render red and falling yields green, and the 10Y-2Y spread is flagged as `curva invertida` when it turns negative.
- **The first business day of a year still shows a change.** When the current year's file cannot supply the previous business day, the dashboard reads the previous year's file once to get it.
- **The Treasury schedule follows Washington, not the host.** The release time is an external fact, so the curve cache schedules its checks in `America/New_York`, with the zone database embedded in the binary so no host tzdata package is needed. It never renders anything in that zone.
- **Unpublished rates render as `—`.** The API may report `null` for a house; that is not shown as zero.
- **Numbers are formatted es-AR**: dot thousands, comma decimals (`1.485,00`).
- **Displayed times use the system timezone.** The footer clock and the country-risk date are rendered in the timezone of the machine running the app, so the same binary shows the wall clock of whatever host it runs on. The Treasury publication schedule above is the one deliberate exception, and it affects only when the cache looks at the source.
- **One in-flight fetch at a time.** A tick or `r` while a fetch is running is ignored instead of stacking requests.
- **Paridad is market-price (USD) / technical-value × 100.** USD price is approximated as ARS-price ÷ CCL-venta (no keyless USD-leg source exists).
- **Technical value (valor técnico) = residual capital + accrued interest,** computed from the SEC 424B5 prospectus terms (30/360 day count).

## Project layout

| Path | Responsibility |
|------|----------------|
| `main.go` | Bubbletea program wiring |
| `internal/dolar` | dolarapi.com client (fetch + parse + typed errors) |
| `internal/riesgo` | Ámbito country-risk client (comma decimals, DD-MM-YYYY dates) |
| `internal/bonos` | compararfondos.com.ar bond client (single-request payload, USD quotes) + parity math |
| `internal/tesoro` | home.treasury.gov yield-curve client (header-name column mapping, previous-business-day changes) + publication cache |
| `internal/tui` | Dashboard model, view, and refresh pipeline |
| `odd/tasks/macro-tui.md` | macro-tui feature plan of record and verification evidence |
| `odd/tasks/bond-source-swap.md` | bond-source-swap feature plan of record |
| `odd/tasks/us-treasury-curve.md` | us-treasury-curve feature plan of record and verification evidence |

## Notes and limits

- Data comes from third-party public endpoints, not official BCRA feeds. Treat it as reference, not as financial advice.
- Requires Go 1.27+ and internet access. Wall-clock refresh is 30 s; source update frequency is set by each provider.
- The dashboard renders about 30 lines (four sections, six bonds, fourteen curve tenors). A terminal shorter than that scrolls.
- The dashboard labels use the domain's Spanish names (`Riesgo país`, `Contado con liquidación`) because they mirror the API display names and the target audience.
