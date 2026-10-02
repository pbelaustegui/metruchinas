# Feature: US CPI indicator

## Objective
Show the US CPI (all items, all urban consumers, BLS): last monthly variation, last-12-months variation and year-to-date accumulated variation.

## Source (verified, keyless)
- FRED CSV, same endpoint family as `internal/fed`: `https://fred.stlouisfed.org/graph/fredgraph.csv?id=<SERIES>&cosd=<YYYY-MM-DD>`; CSV header `observation_date,<SERIES>` (check the actual header in the existing fed client/tests), rows `2026-08-01,334.131`.
- `CPIAUCSL` (seasonally adjusted) for the monthly variation; `CPIAUCNS` (not seasonally adjusted) for the 12-month and year-to-date variations. This mirrors the BLS headline convention (monthly SA, annual NSA).
- Latest published month today: 2026-08. Series are index levels, so variations are computed:
  - monthly = SA[m] / SA[m-1] - 1
  - 12 months = NS[m] / NS[m-12] - 1
  - year-to-date = NS[m] / NS[December of previous year] - 1
- Request with `cosd` about 14 months back so 13 consecutive monthly rows are available. FRED can leave trailing cells empty or lag; require both series to share the same latest month (otherwise use the latest month present in both) and 13 consecutive months in the NSA series and 2 in the SA series; gaps are errors wrapping ErrMalformed.

## Scope
- New package `internal/uscpi` modeled on `internal/ipc` (derivations, cache, validation) and `internal/fed` (FRED CSV fetching/parsing, empty-cell handling).
- Wire into `internal/tui` like the IPC line, placed at the end of the US section (under the Fed line), moved there after review; loading/stale/error states like the other sections; neutral value style.
- README.md updated in the same feature (see the README-per-feature convention): overview, source table, expected result, behavior notes, project layout, `odd/tasks/us-cpi.md` row, dashboard line count (add the new line(s) to the estimate).
- Out of scope: core CPI, categories, charts.

## Tasks
- [x] T1 uscpi package: fetch both FRED series, parse, compute three variations (route: delegated writer)
- [x] T2 TUI wiring + rendering + tests (route: delegated writer)
- [x] T3 README update (route: delegated writer, same run)

## Acceptance
- `go vet ./...`, `go test -count=1 ./...`, `gofmt -l .` clean; RED observed before GREEN.
- Dashboard shows one line like `CPI EE. UU. (ago-2026): 0,4% mensual · 2,9% 12m · 1,8% acum. año`, es-AR number format.

## Progress / evidence
- RED: `go test ./internal/uscpi` and `go vet ./internal/tui` failed to compile (undefined Indicator / m.fetchUSCPI) before implementation; GREEN after.
- `go vet ./...` clean; `go test -count=1 ./...` all packages ok; `gofmt -l .` empty.
- Real-network spot check (throwaway test, deleted), 2026-10-02: Month 2026-08, monthly 0,396%, 12m 3,397%, YTD 3,372%. Raw FRED CSV: SA 334.131/332.813 = +0.396%; NSA 334.980/323.976 = +3.397% and 334.980/324.054 = +3.372%. The `2,9% / 1,8%` in Acceptance was only an example.
- Deviation from Source section: FRED has no October 2025 observation (empty cell in both series, BLS shutdown), so requiring 13 consecutive months failed against the real source (observed: `CPIAUCNS has no 2025-10`). Validation now requires only the base months (SA m and m-1; NSA m, m-12, previous December), looked up by month; a missing base is ErrMalformed, a gap between bases is tolerated.
- README: overview, source row, expected result, behavior bullets, layout rows, height 33 -> 34 lines.
