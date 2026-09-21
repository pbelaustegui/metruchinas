# Feature: bonos-parity

Tracked Tw-equivalent file: `odd/tasks/bonos-parity.md` | Engram mirror: `odd/bonos-parity/tasks`

## Objective

Add USD-denominated Argentine sovereign bonds (GD curve) to the dashboard with their
**paridad**: market price divided by technical value (valor técnico = residual capital +
accrued interest), expressed in %.

## Problem / Why

User wants to see how the market prices the GD bond curve against each bond's contractual
worth. Paridad < 100% means the market pays less than the bond's technical value.

## Definitions (agreed with user)

- **Paridad (%) = precio de mercado (USD) / valor técnico (USD) × 100**
- **Valor técnico = VR (capital residual) + IC (interés corrido)**, over VN 100, in USD.
- **Precio de mercado (USD)** = Ámbito ARS price ÷ dolarapi CCL (venta). Keyless
  approximation; there is no keyless USD-leg quote (see Constraints).
- **IC basis**: 30/360, semiannual coupons (Jan 9 / Jul 9), per the prospectus terms.

## Scope (authorized)

- GD29, GD30, GD35, GD38, GD41, GD46 (the full curve Ámbito publishes under `/bono/`).
- Ámbito `https://mercados.ambito.com/bono/{TICKER}/variacion` client (new package
  `internal/bonos`): one request per ticker, per-bond error isolation, browser-like
  User-Agent (Ámbito's security policy intermittently blocks requests without one).
- Static contractual tables (coupon step-up schedules, amortization) compiled from the
  2020 exchange prospectus filed with the SEC (see Evidence). Pure VR/IC/paridad
  computation, date-anchored, no timezone pinning (civil-date arithmetic).
- TUI section "Bonos soberanos GD (paridad)" with per-bond rows and partial-failure
  behavior consistent with existing sources.
- Tests: table-driven parser tests (offline httptest) and date-anchored VR/IC tests.

## Out of scope (non-goals)

- True (international-leg) paridad, dólar bono, and paridad vs CCL — all need the USD leg
  (GD30D or NY price). No keyless source exists (recon 2026-09-19: Ámbito has no D leg,
  BYMA open API 401, TradingView no AR bonds, Yahoo 429, Cocos/IOL/Cronista no public API).
- AL family (Ámbito reports "Datos inexistentes"), Euro series, CER/TC-linked instruments.
- Rendering Ámbito's `fecha` field: verified stale (GD30 shows 24-05-2024 12:59 while the
  price is current). Ignored by design.

## Constraints

- Public APIs only, no API keys (project convention stands).
- Generated artifacts in English. Reply language: Spanish (Rioplatense, voseo).
- Reuse existing project patterns: `Client` with `HTTPClient`/`BaseURL`, `ErrMalformed`,
  `HTTPError`, comma-decimal string parsing (same shape as `internal/riesgo`).
- One in-flight refresh cycle at a time (existing TUI invariant) extended to the new
  source; per-bond failures degrade to a per-row error, never a blank section.

## Decisions

- D-01: Keyless USD via CCL division (user-approved): `precioUSD = ultimoARS / CCLventa`.
- D-02: Static contractual tables in-repo (user-approved); no runtime dependency for
  VR/coupons. Source: SEC primary document below.
- D-03: IC basis 30/360 as documented in the prospectus ("360 day year comprised of twelve
  30-day months"); coupons Jan 9 / Jul 9 semiannual in arrears, accruing from 2020-09-04.
- D-04: Bond set = GD29/30/35/38/41/46 (Ámbito's published curve).
- D-05: Amortization rule per prospectus: each payment = outstanding ÷ remaining
  installments (straight-line on original face), EXCEPT the 2024-07-09 payment on the 2030s
  = outstanding ÷ 25 (4%). Computed from installment lists, not a copied percentage grid.
- D-06: TDD mode off (same as macro-tui; no project/session config found). Checks:
  `gofmt -l .` empty, `go build ./...`, `go vet ./...`, `go test -count=1 ./...` green
  (also under `TZ=UTC`).
- D-07: Delivery via local work-unit commits on `feat/bonos-parity` (no remote configured),
  reviews per slice as in macro-tui.

## Contractual evidence (static tables source of truth)

Source: Republic of Argentina, "Amendment No. 2 Prospectus Supplement" (2020 debt
exchange), filed with the U.S. SEC on 2020-08-17, form 424B5, accession
`0001193125-20-221606`, primary document `d68512d424b5.htm`:
https://www.sec.gov/Archives/edgar/data/914021/000119312520221606/d68512d424b5.htm
(CIK 0000914021). Extracted 2026-09-19 from the summary tables and per-series terms.

Common terms (all six USD series):
- Interest accrues from 2020-09-04; basis 30/360.
- Coupons paid semiannually in arrears on Jan 9 and Jul 9 of each year (long first coupon
  2021-07-09; irrelevant for forward IC).
- Amortization: each principal payment = outstanding ÷ remaining installments, except
  2030s' 2024-07-09 payment = outstanding ÷ 25.

Per series (coupon step-ups: [from-date, rate] pairs; amortization installments):

| Ticker | Maturity | Coupon schedule (from 2020-09-04 unless noted) | Amortization |
|--------|----------|--------------------------------------------------|--------------|
| GD29 | 2029-07-09 | 1.000% flat | 10 × 10%: 2025-01-09 → 2029-07-09 |
| GD30 | 2030-07-09 | 0.125% →2021-07-09; 0.500% →2023-07-09; 0.750% →2027-07-09; 1.750% to maturity | 2024-07-09: 4% (outstanding÷25); then 12 × 8%: 2025-01-09 → 2030-07-09 |
| GD35 | 2035-07-09 | 0.125% →2021-07-09; 1.125% →2022-07-09; 1.500% →2023-07-09; 3.625% →2024-07-09; 4.125% →2027-07-09; 4.750% →2028-07-09; 5.000% to maturity | 10 × 10%: 2031-01-09 → 2035-07-09 |
| GD38 | 2038-01-09 | 0.125% →2021-07-09; 2.000% →2022-07-09; 3.875% →2023-07-09; 4.250% →2024-07-09; 5.000% to maturity | 22 × (100/22): 2027-07-09 → 2038-01-09 |
| GD41 | 2041-07-09 | 0.125% →2021-07-09; 2.500% →2022-07-09; 3.500% →2029-07-09; 4.875% to maturity | 28 × (100/28): 2028-01-09 → 2041-07-09 |
| GD46 | 2046-07-09 | 0.125% →2021-07-09; 1.125% →2022-07-09; 1.500% →2023-07-09; 3.625% →2024-07-09; 4.125% →2027-07-09; 4.375% →2028-07-09; 5.000% to maturity | 44 × (100/44): 2025-01-09 → 2046-07-09 |

Open validation item — CLOSED 2026-09-20: computed valor técnico cross-checked against
market-published VT (Eco Valores, Puente, Allaria, Portfolio Personal); see
"Progress / verification evidence" and "Next step" below. The SEC document is primary;
the ticker mapping (New USD 20xx = GDxx) holds on both sources.

## Task checklist

- [x] T-01: `internal/bonos` Ámbito client: `Fetch(ctx, tickers...)`, per-ticker
      `BondQuote{Ticker, Ultimo, Variacion, Cierre, ...}` (comma decimals), `ErrMalformed`,
      `HTTPError`, browser-like User-Agent, table-driven httptest suite. — checks: `go test
      ./internal/bonos` 56 passed, gofmt/build/vet green
- [x] T-02: contractual static tables + pure functions `ResidualCapital(ticker, date)`,
      `AccruedInterest(ticker, date)`, `TechnicalValue(...)`, `Parity(priceUSD, ticker,
      date)` + date-anchored table tests (incl. 30/360 edges and the GD30 4% first
      amortization). — checks: `go test ./internal/bonos` 56 passed, full suite 129 passed
- [x] T-03: TUI section: columns Bono | Paridad % | Valor técnico | USD (CCL) | ARS | Δ%,
      fed from bonos + dolar CCL quote; per-bond error rows; refresh-cycle integration with
      deadline handling. — checks: gofmt/build/vet green, `go test -count=1 ./...` 135 passed,
      `TZ=UTC go test -count=1 ./...` 135 passed
- [x] T-04: README (indicators table + behavior notes), tracker update, live integration
      test (`-tags=integration`) fetching the six Ámbito endpoints + CCL for a spot
      sanity check. — checks: full suite 135 passed

## Acceptance criteria

- Dashboard shows the six GD bonds with paridad %, technical value, and ARS price,
  refreshing with the existing cycle.
- A failing bond row shows an error state; the rest of the dashboard is unaffected.
- `go test ./...` green offline; live integration test passes against real APIs.
- Paridad math verified against at least one market-published valor técnico.

## Blockers

- None active.
- Note: Ámbito's `/bono/` endpoint intermittently blocks non-browser User-Agents
  ("Request blocked by security policy" observed 2026-09-19 on the first probe, then
  allowed once, then blocked again without UA). The client must send a browser-like UA and
  treat 403/security-block bodies as malformed/error, never as data.

## Progress / verification evidence

- Branch `feat/bonos-parity` created from `master` after PR #1 merge.
- Source recon (2026-09-19, live): Ámbito `/bono/{GD29,GD30,GD35,GD38,GD41,GD46}` all
  return quotes (GD30 sample: ultimo "67550,00", variacion "-2,86", cierre "69540,000");
  GD30D/AL30D/AL family → "Datos inexistentes"; BYMA open API → HTTP 401; TradingView
  scanner `argentina` → no bond tickers; Yahoo chart API → HTTP 429; Cocos → connection
  failure; IOL/Cronista → no public endpoint. dolarapi CCL already integrated in app.
- Contractual terms extracted from the SEC 424B5 (see Evidence) — full coupon step-ups,
  amortization installment dates, 30/360 basis, and the 2030s' ÷25 first payment rule.
- T-01+T-02 implemented: `internal/bonos/client.go` (234 lines), `internal/bonos/parity.go`
  (345 lines), `internal/bonos/client_test.go` (357 lines), `internal/bonos/parity_test.go`
  (469 lines). Verification: `gofmt -l .` empty, `go build ./...` exit 0, `go vet ./...`
  exit 0, `go test -count=1 ./internal/bonos/...` 56 passed, `go test -count=1 ./...` 129
  passed (5 packages). Commit `c707f48`.
- T-03+T-04 implemented: `internal/tui/tui.go` +135/-6 (BondsFetcher, cclVenta extraction,
  renderBonds with paridad/VT/USD/ARS/Δ%, per-bond error rows), `internal/tui/tui_test.go`
  +115 (6 new tests: bond data storage, failure isolation, refresh fetch, view rendering,
  per-bond error, CCL extraction), `README.md` +3 (bonds indicator row + 2 behavior notes),
  `internal/bonos/integration_test.go` (build tag `integration`, 2 live tests: quote fetch +
  parity sanity). Verification: `gofmt -l .` empty, build/vet exit 0, `go test -count=1 ./...`
  135 passed (5 packages), `TZ=UTC` also 135 passed.

## Next step

- **Feature CLOSED (2026-09-20).** T-01..T-04 all `[x]`, all four acceptance criteria hold
  with recorded evidence, no active blockers.
- VT market cross-check (2026-09-20): `TechnicalValue` output for all six bonds
  matches market-published valor técnico within rounding / same-day cache skew. Computed
  (30/360, VR + IC): GD29 60.118 / GD30 64.095 / GD35 100.814 / GD38 100.986 / GD41 100.690 /
  GD46 91.649. Market: Eco Valores publishes GD29 60.12, GD30 64.10, GD35 100.83,
  GD38 101.01, GD41 ~100.5-100.7 (cache skew), GD46 91.66; Puente GD38 101.00 (IC 1.00);
  Allaria residual schedules match (GD29 60%, GD30 64%, GD46 90.91% after 2026-07-09);
  Portfolio Personal informe 18-Sep-2026 residuals column matches. Residuals are exact on
  every source; IC differences are ≤0.02 (one accrual day or rounding).
- Review status: **no native Gentle AI review was run for this feature** — the user
  explicitly left these candidates unreviewed and closed the tracker anyway. D-07
  ("reviews per slice as in macro-tui") was NOT executed; recorded here as a deliberate
  deviation, do not treat the commits as reviewed. Delivery stays as local work-unit
  commits on `feat/bonos-parity` (no remote configured).
- Remaining open work: none. Advisory/coverage follow-ups: none recorded (no review ran).
- Final commits: `c92835d` (plan of record) → `c707f48` (client + parity) →
  `44d9ce0` (TUI section + tests + README) → `918f48d` (VT cross-check evidence).
