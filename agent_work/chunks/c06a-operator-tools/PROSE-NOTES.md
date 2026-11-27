# Chunk 6a — Operator tools: prose notes

Prose pass for `c06a-operator-tools` on branch `work/c06a-operator-tools` (pre-prose commit `3eccc58`), written with the Clarity skill (`SKILL.md`, `references/edit.md`, `references/medium.md`, `references/longform.md`) and Nick's ruling of 2026-11-26 (describe the software only; no making-of prose).

## What was written

- **Go comments (comments only; AST unchanged against `3eccc58`):**
  - Package doc comments, doc comments on every exported identifier, and targeted inline invariant comments in:
    - `cmd/loadgen/main.go`
    - `internal/loadgen/{plan,tracker,report,metrics}.go`
    - `internal/loadgen/live/live.go`
    - `internal/loadgen/loadgentest/fake.go`
    - `cmd/mgctl/main.go`
    - `internal/mgctl/{commands,contract,password,session,tls}.go`
  - Brief per-test property comments in all chunk 6a test files:
    - `cmd/loadgen/main_test.go`, `cmd/mgctl/main_test.go`
    - `internal/loadgen/{plan,tracker}_test.go`, `internal/loadgen/live/live_test.go`
    - `internal/mgctl/{imports,mgctl}_test.go`
    - `internal/obs/simrun/loadgen_test.go`
    - `scripts/c06a_{chaos_down,report_scripts,site_links,site_suffix}_test.go`
    - `test/analysis/report/report_test.go`
    - `test/e2e/c06a_{mgctl,tierr}_test.go`
  - Package/file summary comments on the 10 promoted test files (`internal/sim/adv_c01_promoted_test.go`, `internal/sim/simnet/adv_c01_promoted_test.go`, `internal/obs/adv_c01_promoted_test.go`, `internal/sim/world/adv_c02_promoted_test.go`, `internal/obs/adv_c03_promoted_test.go`, `internal/obs/jsonstats/adv_c03_promoted_test.go`, `internal/obs/promtext/adv_c03_promoted_test.go`, `internal/obs/csvflush/adv_c03_promoted_test.go`, `internal/obs/simrun/adv_c03_promoted_test.go`, `internal/pgwire/adv_c04_promoted_test.go`).
- **Book chapter and front matter:**
  - Wrote `book/chapters/06a-operator-tools.md` ("Watching it run: a load tool, a small client, and reports that say what a run proved").
  - Updated `book/manifest.json`: set chapter `6a` status from `planned` to `live` (no renumbering).
  - Updated `book/index.md` to reflect Chapters 1 through 6a being live.
- **Reader-facing docs:**
  - Updated `README.md`: added Chunk 6a and Chapter 6a links and a concise "Running the local stack and operator tools" section (`make run-up`, `make load`, `make run-down`, `make report`, `mgctl`, `loadgen`).
  - Checked `docs/knowledge/operator-tools.md`: every sentence matches the final code and limits accurately, so no edit was needed.

## Claims narrowed, scoped, or left out for lack of evidence

- **`mgctl tenants` and `config show` gap stated plainly:** No engine component exports a `tenant` label yet (per-tenant numbers wait for fairness in Chapter 9), and the admin surface has no effective-settings endpoint. The chapter states this directly in both the `mgctl` walkthrough (with real simulated output) and Stated limits #1, noting the `-config <file>` local comparison fallback.
- **No live `make run-up` run executed during this prose pass:** Per the brief, `make run-up` needs Docker and is slow on this shared 4-vCPU host; tier R smoke numbers (200 completed requests matching Prometheus `mg_engine_requests_completed_total`, ~150 statements/s single-host dev-stack saturation, loopback-only bindings) are taken strictly from the verified evidence in `NOTES.md` and `REVIEW-2.md` and stated at that exact scope.
- **3-seed vs 300-seed report scale:** Stated explicitly as using synthetic copies of recorded runs, and noted that a full 4,000-run sweep report has not been timed.
- **`mgctl query` end-to-end row coverage:** Distinguished unit coverage over all messaged rows of `pgwire.ErrorRows` from end-to-end `mgctl` coverage (rows, `pass-through`, `unsupported-feature`, `auth-failed`, `tls-required`, `unknown-database`), noting that overload and database-loss rows are produced end to end by the proxy's scenario suite.
- **`late` and intended-time latency caveats:** Stated that `Late` includes host scheduling jitter (25 ms default threshold), that `latency` includes client-side pipelining wait (`latency_from_send` is the control), and that `refused` requires a `mini-gateway:` message prefix.
- **Stdin password cap:** Stated the 1,024-byte limit on `-password -` (refused, never truncated) vs no limit on direct flag values.
- **Planned chapters not linked:** References to Chapters 7, 9, 14, and 15 appear as plain text only; every relative link targets a `git ls-files`-tracked file.
- **Zero making-of prose:** Per Nick's ruling of 2026-11-26, no review rounds, repair rounds, adversarial passes, or seats appear in the book, README, or Go comments.

## Commands run

- Real outputs quoted in Chapter 6a:
  - `go run ./cmd/loadgen -plan 6 -seed 7 -profile errors` (digest `8d16c9b3f53e6bfa`)
  - `go run ./cmd/mgctl -help`
  - `go run ./cmd/mgctl verifier -password example-pw`
  - `go run ./cmd/mgctl config check internal/config/testdata/example.json` (exit 2)
  - `MGCTL_TRANSCRIPT=/tmp/c06a-prose-transcript.txt go test -count=1 -v -run '^TestMgctlSeededModelAgainstTheSimulatedGateway$' ./test/e2e` (PASS, 3.206s; transcript excerpts quoted)
  - `RUN_DIR=/tmp/c06a-prose-sweep scripts/sweep.sh -seeds 3 -run '^TestScenarioE2E$/^(e2e-results|e2e-queue-full|e2e-reject-list|e2e-reset-error)$'` (12 seed runs passed)
  - `scripts/report.sh /tmp/c06a-prose-sweep` (`VERDICT: PASS (30 checks over 12 runs)`, exit 0)
  - `scripts/report.sh test/analysis/report/testdata/load-errors` (loadgen tables 4.7–4.9 quoted)
- Formatting, build, vet, site, Clarity lint, and tests:
  - `gofmt -l .` (empty)
  - `go build ./... && go vet ./...` (clean)
  - `scripts/site.sh /tmp/c06a-prose-site` (`built 25 pages`, confirmed `book/06a-operator-tools.html` built and listed in `book/index.html` and `pages.txt`)
  - `python3 /root/.agents/skills/clarity/scripts/strip_markdown.py` + `prose_stats.py` on `book/chapters/06a-operator-tools.md` (one self-review edit applied)
  - `MG_REQUIRE_DUCKDB=1 go test -count=1 ./cmd/loadgen ./cmd/mgctl ./internal/loadgen/... ./internal/mgctl ./scripts ./test/analysis/report ./agent_work/adversarial/c06a-operator-tools` (all 7 packages PASS)
  - `go test -count=1 -run 'Mgctl' ./test/e2e` (PASS)
  - `go test -short -count=1 ./internal/sim ./internal/sim/simnet ./internal/obs ./internal/sim/world ./internal/obs/jsonstats ./internal/obs/promtext ./internal/obs/csvflush ./internal/obs/simrun ./internal/pgwire`
  - `GATE_STAGE=final GATE_PREPROSE=3eccc58 GATE_ONLY=G0,G1,G2,G9 GATE_OUT=/tmp/c06a-prose-final scripts/gate.sh c06a-operator-tools` (run after commit to verify `astdiff`, `prose-paths.txt`, lints, and site)
