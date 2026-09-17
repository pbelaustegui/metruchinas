# metruchinas

Argentine macroeconomic indicators in your terminal: USD/ARS quotes and the country-risk index, refreshed live from public APIs.

## What it shows

| Indicator | Detail | Source |
|-----------|--------|--------|
| Dólar oficial | Buy / sell rate | [dolarapi.com](https://dolarapi.com/docs/) |
| Dólar blue | Buy / sell rate | dolarapi.com |
| Dólar MEP (bolsa) | Buy / sell rate | dolarapi.com |
| Dólar CCL (contado con liquidación) | Buy / sell rate | dolarapi.com |
| Riesgo país | EMBI Argentina index + daily variation | [mercados.ambito.com](https://mercados.ambito.com) |

Both sources are public and need no API key.

## Quick path

```bash
go run .
```

Expected result: an alternate-screen dashboard with the four USD rates and the country-risk index. It refetches both sources every 30 seconds; `q` quits.

```bash
# on-demand checks
go build ./...
go vet ./...
go test ./...          # offline suite (httptest servers, no network)

# live end-to-end check against both real APIs
go test -tags=integration ./internal/tui -run TestLiveSourcesFetchRealData -v
```

## Keys

| Key | Action |
|-----|--------|
| `r` | Refresh both sources now |
| `q`, `Esc`, `Ctrl+C` | Quit |

## Behavior you can rely on

- **Partial failures never blank the dashboard.** Each source reports its own error; if Ámbito is down, the USD rates stay on screen (and vice versa) with a red message in the failing section.
- **Unpublished rates render as `—`.** The API may report `null` for a house; that is not shown as zero.
- **Numbers are formatted es-AR**: dot thousands, comma decimals (`1.485,00`).
- **Times use the system timezone.** No timezone is pinned in code: the footer clock and the country-risk date are rendered in the timezone of the machine running the app, so the same binary shows the wall clock of whatever host it runs on.
- **One in-flight fetch at a time.** A tick or `r` while a fetch is running is ignored instead of stacking requests.

## Project layout

| Path | Responsibility |
|------|----------------|
| `main.go` | Bubbletea program wiring |
| `internal/dolar` | dolarapi.com client (fetch + parse + typed errors) |
| `internal/riesgo` | Ámbito country-risk client (comma decimals, DD-MM-YYYY dates) |
| `internal/tui` | Dashboard model, view, and refresh pipeline |
| `odd/tasks/macro-tui.md` | Feature plan of record and verification evidence |

## Notes and limits

- Data comes from third-party public endpoints, not official BCRA feeds. Treat it as reference, not as financial advice.
- Requires Go 1.27+ and internet access. Wall-clock refresh is 30 s; source update frequency is set by each provider.
- The dashboard labels use the domain's Spanish names (`Riesgo país`, `Contado con liquidación`) because they mirror the API display names and the target audience.
