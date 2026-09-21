# Feature: bond-source-swap

Tracked in repo: `odd/tasks/bond-source-swap.md` | Engram mirror: `odd/bond-source-swap/tasks`

## Objective

Replace the dead Ámbito bond quote source with compararfondos.com.ar so the dashboard shows current USD prices for the six GD bonds.

## Problem / Why

`mercados.ambito.com/bono/{TICKER}/variacion` is frozen: live probe on 2026-09-21 returned GD30 `ultimo: 67550,00` with `fecha: 24-05-2024 12:59` — prices over two years stale. Alternatives probed live:

| Source | Result |
| --- | --- |
| compararfondos.com.ar `/api/bonos` | ✅ live, no key, 351 bonds, one request; GD in USD (`GDxxD` tickers) |
| data912 `/live/arg_bonds` | ❌ 502 Bad Gateway on every attempt |
| Rava API v5 | ⏸ requires registration + Bearer token; rejected for now |

## Scope (authorized)

- Source: `https://compararfondos.com.ar/api/bonos` (single GET, JSON, no auth).
- Tickers: GD29D, GD30D, GD35D, GD38D, GD41D, GD46D.
- Display: native USD price from source + parity % (existing math) + technical value + daily variation. The derived `ARS` column and the `ARS/CCL` USD derivation are removed.
- Keep: per-ticker error isolation, 30s refresh, timeout semantics (R3-timeout-broadcast-unchecked fix must keep working).

## Constraints

- Go 1.27, stdlib HTTP only for the client (bubbletea stays for TUI). No API keys.
- Artifacts in English. Reply language: Rioplatense Spanish.
- Attribution clause: mention compararfondos only if data is published publicly — a local TUI does not require it; record the URL in code comments regardless.

## Task checklist

- [x] T-01: Rewrite `internal/bonos` client for compararfondos (one request, parse 351-bond payload, select the six `GDxxD` tickers, per-ticker errors). Fields kept: `Ticker`, `Ultimo` (now USD price), `Variacion`. `Cierre` dropped (payload has no previous close; `pctChange` covers variation). Ticker resolution: input matched verbatim, falling back to the `D` (MEP, USD) variant — "GD30" resolves to "GD30D". Tests: 12 scenarios incl. happy path, missing ticker, null price, missing pctChange, verbatim-match preference, malformed payload, HTTP error, context cancellation, unexpected status. — checks: `go test ./internal/bonos` ✓ (12 PASS), `go vet ./...` ✓
- [x] T-02: TUI — renderBonds uses source USD price directly (CCL division and ARS column removed, `cclVenta` field and extraction removed); parity and VT unchanged. Tests updated (es-AR comma decimals in assertions). — checks: `gofmt -l .` empty, `go build ./...` ✓, `go vet ./...` ✓, `go test -count=1 ./...` ✓
- [x] T-03: README bond section + layout table updated; tracker closed; live integration check PASS: `go test -tags=integration` (7 quotes, riesgo 531) and ad-hoc live `bonos.Fetch` against the real API returning all six GD USD quotes (GD30 moved 57.57 → 57.20 within the session — proof the source is intraday-live). — checks: `go test -count=1 ./...` green

## Acceptance criteria

- Dashboard shows same-day USD prices for all six GD bonds.
- Parity % is computed from the source USD price and stays within market range.
- A failing source shows the error state; a missing ticker shows per-ticker error; the app does not crash.
- `go test -count=1 ./...` green.

## Blockers

- None. data912 is down (502) — noted only as a rejected alternative.

## Review closure

- **Native review CLOSED (approved on the first admitted event, authority burned).** Lineage `review-dc51c3d2ef069e51`, target `sha256:ba4d6ff1424731b6eed979a5fe7882f6e63df0ba7df0ee7bff18344ee2b891fb`, one lens (`review-reliability`, order 0), correction budget 200 — unused (no BLOCKER/CRITICAL). Flow: inspect (untracked selection for `odd/tasks/bond-source-swap.md`) → `gentle-ai sync` (managed assets outdated) → START → reviewer capture (`approved`) → acknowledgement `gentle-ai.review-acknowledged/v1`, `authority: burned`.
- Advisory findings (all informational, none blocks, do NOT re-review this candidate for them): `R3-request-count-data-race` (`client_test.go:94`, SUGGESTION), `R3-unbounded-body-read` (`client.go:195-197`, SUGGESTION), `R3-verbatim-ticker-preference` (`client.go:229-230`, WARNING).

## Next step

- Delivery: work-unit commits on request (never automatic).
- T-01 authorized 2026-09-21; T-02/T-03 executed in the same pass to keep the build green (TUI tests referenced the removed `Cierre` field).
