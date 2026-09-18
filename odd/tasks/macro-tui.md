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
- [x] T-08: Add the root `.gitignore` (build output, `*.test`, `*.out`, `.atl/`, `.idea/`). — checks: `git check-ignore -v` on every pattern, `git status --porcelain` empty, `gentle-ai review assess` succeeds without an untracked-inventory declaration · commit `de4217c` · tier medium (`executable_change`)

## Acceptance criteria

- `go run .` renders current quotes (oficial, blue, MEP, CCL) and country risk, refreshing live.
- `q` or Ctrl+C quits cleanly.
- A failed source shows an error state and does not crash the app.
- `go test ./...` green.

## Blockers

- None active.
- **Runtime subagent dispatch — RESOLVED**: dispatch used to fail session-wide with "OpenCode's free tier can only be used from within OpenCode" (bounded attempts: 2 reviewer dispatches + 1 trivial diagnostic). Resolved by pinning `opencode/claude-haiku-4-5` on the 22 subagents. This was a local runtime configuration change, not a Gentle AI defect; no report filed.

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
- Slice 2 (`7821b22..HEAD`) accumulates 1157 authored lines (12 files) — above the ~400-line review budget. With no remote there are no PRs to chain, so the only real lever is reviewing per work unit, which is what the per-commit tiers record.
- **Native review of slice 1 — CLOSED (approved, authority burned)**. The predecessor lineage `review-99ba0faa3f7fb899`, frozen while dispatch was broken, was carried forward through an explicit maintainer-authorized `scope_changed` recovery into successor lineage `review-slice1-recovery` (base-ref `f93e3a1a1525fb5b91020da86e44810c87a2d7bc`, `--committed-only`): the workspace had advanced to 13 paths (whole feature) past the frozen two-client target. Flow: `review-reliability` reviewer (admitted) → `review-refuter` (one CRITICAL corroborated, `R3-timeout-broadcast-unchecked`) → correction plan forecast 70 lines → bounded correction → targeted validator (approved) → acknowledgement `review-acknowledged/v1`, `authority: burned`.
- Correction commit `4d57ba5` (`fix: fail the refresh cycle when its deadline expires`): `internal/tui/tui.go` +24/-1 (injectable `Model.timeout`; `timeoutErr()` helper turns an expired refresh context into an explicit per-source error wrapping `context.DeadlineExceeded` so buffered data from a cycle that blew its deadline is never accepted silently) and `internal/tui/tui_test.go` +50 (two tests: a slow source times out while the fast source still delivers; buffered data from an expired cycle is rejected). Actual 74 changed lines against the 200-line correction budget. Checks: `gofmt -l .` empty, build/vet exit 0, `go test -count=1 ./...` 71 passed (also under `TZ=UTC`); parent spot check re-ran `go test -count=1 ./...` → 71 passed.
- Non-blocking advisory findings from the approved slice-1 review (informational; none opened a correction; do NOT re-review this candidate for them): `R3-date-parse-no-validation` (`riesgo.go:209-214`, WARNING), `R3-http-client-nil-fallback` (`dolar.go:114-116`, WARNING), `R3-nil-pointer-dereference-rate-format` (`tui.go:259-264`, SUGGESTION), `R3-riesgo-variation-negative-zero` (`riesgo.go:178-182`, SUGGESTION), `R3-test-isolation-httptest-cleanup` (`dolar_test.go:19-29`, SUGGESTION), `R3-tick-heartbeat-determinism` (`tui.go:302-303`, SUGGESTION).

## Next step

- Slice 1 review is closed and burned. Slice 2 (TUI + main + README + tracker, `7821b22..HEAD`) is the remaining review candidate: `gentle-ai review assess --cwd . --base-ref 7821b22 --committed-only --json` reports `medium`, 12 paths, 1157 changed lines. RDD is `on` (decided by global).
- Remaining open work, both explicitly non-blocking and unstarted: the T-07 coverage gaps (listed above) and the six advisory findings from the approved slice-1 review.
- Operational note for the review tooling: the IDE-generated `.idea/` directory (not created by the agent) and the machine-local `.atl/` are now ignored by the root `.gitignore` added in `de4217c` (`chore: ignore local build output and editor state`), together with `/metruchinas`, `*.test`, and `*.out`. Verified effect: `git status --porcelain` is empty after that commit and `gentle-ai review assess --cwd . --base-ref <prev> --committed-only --json` succeeds **without** the untracked-inventory declaration that earlier runs required (the hashes involved were `sha256:1564f505...` with `.idea/` present and `sha256:4425697...` for `.atl/` alone).
- Review-execution note: the OpenCode reviewer transport is `opencode_provider_injected`, so `--materialize` is unavailable; reviewer/refuter/validator captures happen by launching the host `task` tool with the exact provider-issued binding prompt. A frozen transaction whose workspace has advanced requires the maintainer-authorized `review recover --disposition=scope_changed` path before capture, and `gentle-ai review recover` needs the predecessor `--base-ref` (the earlier default did not match).
