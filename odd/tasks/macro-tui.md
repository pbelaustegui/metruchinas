# Feature: macro-tui

Tracked Tw-equivalent file: `odd/tasks/macro-tui.md` | Engram mirror: `odd/macro-tui/tasks`

## Objective

Small Go terminal app that shows Argentine macroeconomic indicators: USD/ARS quotes (official, blue, MEP/CCL) and country risk (EMBI Argentina).

## Problem / Why

User wants a small Go TUI to consult public data without leaving the terminal.

## Scope (authorized)

- USD/ARS quotes from dolarapi.com `/v1/dolares`: oficial, blue, bolsa (MEP), contadoconliqui (CCL).
- Country risk from Ámbito `https://mercados.ambito.com/riesgopais/variacion`: value, date, variation.
- Bubbletea TUI with live refresh, quit key, and error state when a source fails.
- Tests for both API clients (parser/unit level). TUI state via direct Model.Update tests.
- README with run instructions.

## Constraints

- Go 1.27 (toolchain available). Public APIs only, no API keys.
- Generated artifacts in English. Reply language: Spanish (Rioplatense, voseo).
- No remote configured: delivery via local work-unit commits on `feat/macro-tui`.

## Decisions

- TUI library: Bubbletea (charmbracelet) — standard Go TUI, live updates, keyboard handling.
- TDD mode: **off** (no project/session config found). Use ordinary functional checks: `go build ./...`, `go vet ./...`, `go test ./...`.
- Delivery strategy: `ask-on-risk` default; no remote → local commits only. Forecast ~560 authored changed lines (clients ~110, TUI ~240, main ~40, tests ~150, README/odd ~60).

## Task checklist

- [x] T-01: Bootstrap Go module `metruchinas`, package layout. — checks: `go build ./...` · commit `c6f11d4` · tier medium (config) → deferido
- [x] T-02: `internal/dolar` client for dolarapi.com (fetch + parse oficial/blue/bolsa/contadoconliqui) + table-driven tests (parse + error). — checks: `go test ./internal/dolar` · commit `7821b22` · tier medium → cierre slice 1
- [x] T-03: `internal/riesgo` client for Ámbito (fetch + parse `"515"`, `"0,98%"`, date) + table-driven tests. — checks: `go test ./internal/riesgo` · commit `7821b22` · tier medium → cierre slice 1
- [x] T-04: Bubbletea TUI (refresh every 30s, `q`/Ctrl+C quit, error state per source) + `main.go` wiring + TUI state tests. — checks: `go build ./...`, `go vet ./...`, `go test ./...` · commit `721c55f` · tier medium (config: go.mod deps)
- [x] T-05: README (run/quit/indicators) + final full verification. — checks: `go test ./...`, `go build ./...` · commit `b87a115` · tier passive
- [x] T-06: Post-close fix — anchor `internal/riesgo` date to the system timezone. — checks: `gofmt -l .` empty, build/vet exit 0, `go test -count=1 ./...` green under `TZ=UTC` and `TZ=America/Argentina/Buenos_Aires` · commit `c3d5428` (fix, medium/executable) + `5cbe370` (README note, passive)
- [x] T-07: Close the top offline coverage gaps (client transport errors; `refresh()` success path and both failure-isolation directions; falling-index arrow). — checks: `gofmt -l .` empty, build/vet exit 0, `go test -count=1 ./...` green (also under `TZ=UTC`), coverage 87.9% → 90.2% · commit `332b293` · tier medium (`executable_change`, 163 líneas) → deferido al slice

## Acceptance criteria

- `go run .` renders current quotes (oficial, blue, MEP, CCL) and country risk, refreshing live.
- `q` or Ctrl+C quits cleanly.
- A failed source shows an error state and does not crash the app.
- `go test ./...` green.

## Blockers

- **Runtime (no Gentle AI)**: subagent dispatch fails session-wide with "OpenCode's free tier can only be used from within OpenCode". Blocks (a) the reviewer actor of the native review for slice 1 (lineage `review-99ba0faa3f7fb899`, lens `review-reliability`, slot still open and re-offered) and (b) further delegated writers. Bounded attempts: 2 reviewer dispatches + 1 trivial diagnostic — all failed. Not a product defect; no report filed.

## Progress / verification evidence

- Branch `feat/macro-tui` created.
- Review slices (usuario eligió dos slices por unidad): Slice 1 = `master..7821b22` (clientes, 651 líneas, cierre ahora); Slice 2 = `7821b22..fin` (TUI + main + README, cierre de feature).
- T-02/T-03 report worker: `go build` exit 0, `go vet` exit 0, `go test` 31 passed. Spot check padre: 31 passed. Borde revisado actual: `master`.
- T-04 implemented inline (subagent dispatch unavailable, see Blockers). Checks: `gofmt -l .` empty, `go build ./...` exit 0, `go vet ./...` exit 0, `go test ./...` 63 passed. Live integration check `go test -tags=integration ./internal/tui -run TestLiveSourcesFetchRealData -v`: PASS — 7 quotes fetched (4 displayed), riesgo 515 (0.98%, up-red).
- T-05: README added (English, follows cognitive-doc-design shape). Final verification: `go build ./...` exit 0, `go vet ./...` exit 0, `go test ./...` 63 passed.
- Post-close fix (user report: "parece mostrar GMT y no el timezone local"): diagnosed, not an app-wide bug. At diagnosis time the machine's timezone was `Etc/UTC` (`/etc/localtime -> Etc/UTC`, `TZ` unset) — fixed since, see below — so Go's `Local` *was* UTC and the footer (`time.Now()`) was faithful. The real defect was scoped: `internal/riesgo` anchored the API's date-only `fecha` field to UTC, i.e. not system-timezone semantics. Fixed by re-anchoring to local midnight (`time.Date(y, m, d, 0, 0, 0, 0, time.Local)`) instead of converting the instant — converting would render the previous day west of Greenwich (verified: `t.In(Local)` shows 16-09-2026, re-anchoring shows 17-09-2026). Doc comment and `riesgo_test.go` location assertion updated to `time.Local`. Checks: `gofmt -l .` empty, `go build ./...` exit 0, `go vet ./...` exit 0, and fresh (`-count=1`) `go test ./...` green under both `TZ=UTC` and `TZ=America/Argentina/Buenos_Aires` (a plain rerun without `-count=1` returns `(cached)` and proves nothing). Commits: `c3d5428` (fix), `5cbe370` (README follow-up).
- Machine timezone (user-side follow-up of the T-06 diagnosis): fixed on the machine and verified by the agent — `/etc/localtime -> /usr/share/zoneinfo/America/Argentina/Buenos_Aires`, `/etc/timezone = America/Argentina/Buenos_Aires`, `date` shows `-03`, Go `Local` offset `-10800`. The old symlink pointed at `Argentina_Standard_Time`, a Windows/CLDR zone name absent from tzdata, so glibc fell back to UTC silently. Go resolves `time.Local` **once per process**: an already-running dashboard keeps the old offset until restarted.
- Offline coverage after T-07 (per package): `tui` 95.0%, `riesgo` 88.9%, `dolar` 80.6%, `main` 0.0% → 90.2% total, with `refresh()` fully covered. Measured with `go test -count=1 -covermode=count -coverprofile ./...`; a per-package profile reports `dolar.Fetch`/`riesgo.Fetch` as 0.0% although the integration test exercises them (they are wired in `tui.New()`), so never read dead code from a per-package profile alone.
- T-07 left these gaps open (available on request): `renderQuotes` error state (tui.go:169-170), "sin cotizaciones" (176), riesgo "sin datos" (201), footer "actualizando…" (221-222), `Init` (76-77), unknown-message fallthrough (120), `padRight` (294-295), `tickCmd` (300), `HTTPError.Error()` 0.0% in both clients (dolar.go:38, riesgo.go:46 — covered only by the network tag), and `main.main` 0.0% (accepted).
- Slice 2 (`7821b22..HEAD`) accumulates 1068 authored lines (11 files) — above the ~400-line review budget. With no remote there are no PRs to chain, so the only real lever is reviewing per work unit, which is what the per-commit tiers record.

## Next step

- Native review of slice 1 is blocked on the runtime (see Blockers); its frozen transaction stays open for capture from a runtime that can dispatch reviewer subagents. Slice 2 (TUI) review boundary is `7821b22`.
- Operational note for the review tooling: untracked now includes an IDE-generated `.idea/` directory (not created by the agent). The repository has no root `.gitignore`. Current eligible untracked inventory: `sha256:1564f50593a84f2e05b0bcb19b54110accc0cabb16c98679ef90c175af768490` (it had been `sha256:4425697ab74255760bb7c3b87dc13642263a59d4b65cc516abaa19765c94da71` for `.atl/` alone).
