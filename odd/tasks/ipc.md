# Feature: IPC Argentina indicator

## Objective
Show Argentina's national CPI (IPC, INDEC): last monthly variation, last-12-months variation and year-to-date accumulated variation.

## Source (verified)
- `https://apis.datos.gob.ar/series/api/series/?ids=148.3_INIVELNAL_DICI_M_26&limit=14&sort=desc&format=json`
- Series "IPC. Nivel General Nacional. Base dic 2016. Mensual" (INDEC). Response: `{"data": [["2026-08-01", 12276.766], ...]}`, newest first. Latest published month today: 2026-08.
- The series is an index level, so variations are computed:
  - monthly = L[m] / L[m-1] - 1
  - 12 months = L[m] / L[m-12] - 1
  - year-to-date = L[m] / L[December of previous year] - 1
- With limit=14 and sort=desc, rows cover at least 13 months back (needed for 12m). Handle January (YTD base is previous December = 1 month back) and missing months (gaps) as errors.
- Expected for 2026-08 (from the live data): ~1,7 % m/m, ~33,5 % 12m, ~21,3 % YTD.

## Scope
- New package `internal/ipc` (client + parser + variation math + tests), modeled on `internal/itcrm` and `internal/riesgo`.
- Wire into `internal/tui` like the ITCRM line; loading/stale/error states like other sections.
- Out of scope: regional or category breakdowns, charts, core CPI.

## Tasks
- [x] T1 ipc package: fetch + parse + compute three variations (route: delegated writer)
- [x] T2 TUI wiring + rendering + tests (route: delegated writer)

## Acceptance
- `go vet ./...`, `go test ./...`, `gofmt -l .` clean; RED observed before GREEN.
- Dashboard shows one line like `IPC (ago-2026): 1,7% mensual · 33,5% 12m · 21,3% acum. año`, es-AR number format.

## Progress / evidence
- T1 (inline-verified, delegated writer): `internal/ipc` (ipc.go, cache.go + tests). RED: `go test ./ipc` failed to build (undefined Client/Indicator); GREEN after implementation. Live spot check 2026-08: monthly 1.659, 12m 33.541, YTD 21.295 (matches expected ~1,7 / ~33,5 / ~21,3). Throwaway live test deleted.
- T2: `internal/tui` wiring (IPCFetcher, renderIPC, footer timestamp) + 9 tests. RED: `go vet` failed (fetchIPC undefined); GREEN after implementation. IPC line sits directly under the ITCRM line (+1 dashboard line), neutral value style.
- Checks: `go vet ./...` clean, `go test -count=1 ./...` all ok, `gofmt -l .` empty.
- Commits: none yet (not committed by the writer).
