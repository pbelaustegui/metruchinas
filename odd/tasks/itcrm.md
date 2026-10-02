# Feature: ITCRM indicator (multilateral real exchange rate)

## Objective
Show the BCRA's ITCRM (Índice de Tipo de Cambio Real Multilateral, base 17-12-15=100) in the dashboard, with its daily variation.

## Why
The user wants a real-exchange-rate indicator. The user attributed it to Banco Nación; verified it is published by the BCRA.

## Source (verified)
- `https://www.bcra.gob.ar/archivos/Pdfs/PublicacionesEstadisticas/ITCRMSerie.xlsx` (~3.6 MB, daily, last row 2026-10-01).
- Sheet 1 "ITCRM y bilaterales": col A = Excel date serial, col B = ITCRM. Rows 1-2 are headers; trailing rows are empty styled cells.
- Parse with stdlib only (`archive/zip` + streaming `encoding/xml`); no new dependency.

## Scope
- New package `internal/itcrm` (client + parser + tests), modeled on `internal/riesgo` / `internal/tesoro`.
- Wire into `internal/tui` and `cmd` entrypoint like the other fetchers; stale/error/loading states like other sections.
- Out of scope: bilateral series, historical charts, monthly sheet.

## Tasks
- [x] T1 itcrm package: fetch + parse latest and previous value (route: delegated writer)
- [x] T2 TUI wiring + rendering + tests (route: delegated writer)

## Acceptance
- `go test ./...` and `go vet ./...` pass; RED observed before GREEN where a runnable test exists.
- Dashboard shows `ITCRM: 85,04 (-0,13%)` style line, es-AR number format, existing coloring convention.

## Progress / evidence
- T1 done (uncommitted). RED: `go test ./internal/itcrm` failed to compile (Indicator/Cache undefined); GREEN after `itcrm.go` + `cache.go`. Real-file check (throwaway test, removed): value 85.035, variation -0.1308%, date 2026-10-01 -> renders `ITCRM: 85,04 (-0,13%)`.
- Cache: calendar-day cache (fed.Cache pattern), not tesoro's NY-publication schedule, because BCRA has no known release hour.
- T2 done (uncommitted). RED: `go vet ./internal/tui` failed (`fetchITCRM` undefined); GREEN after wiring `ITCRMFetcher`, `renderITCRM` (Dólar section, under quotes), `itcrmAt` in footer timestamp. Variation uses market colors (green >= 0, red < 0).
- Verification: `go vet ./...` clean; `go test -count=1 ./...` all ok; `gofmt -l .` empty.
- Next: user review, then commit (not committed by the writer).
