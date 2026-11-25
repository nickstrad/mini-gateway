# Chunk 3 — Observability spine: prose pass notes

Written by Neon on 2026-11-25 on branch `work/c03-obs-spine` (worktree `/root/Software/mini-gateway-work/c03`) from `/root/briefs/zgateway-p2-c03-prose.md`.

## What was written

- **Go comments (comments only, zero AST changes verified by `astdiff`):**
  - Added doc comments on every exported type, constant, variable, function, and method, plus concise invariant/"why" inline comments where helpful, across all 20 non-test Go files added by Chunk 3:
    - `internal/obs/{metric,instance,collector,self,merge,health,detail}.go` (existing Chunk 1 `histogram.go` already holds the `Package obs` doc comment and was not touched)
    - `internal/obs/catalog/catalog.go` (with `Package catalog` doc)
    - `internal/obs/csvflush/{schema,writer,reader}.go` (with `Package csvflush` doc on `schema.go`)
    - `internal/obs/jsonstats/jsonstats.go` (with `Package jsonstats` doc)
    - `internal/obs/logline/logline.go` (with `Package logline` doc)
    - `internal/obs/obstest/{conformance,golden}.go` (existing Chunk 1 `obstest.go` already holds the `Package obstest` doc comment and was not touched)
    - `internal/obs/promtext/{promtext,check}.go` (with `Package promtext` doc on `promtext.go`)
    - `internal/obs/simrun/simrun.go` (with `Package simrun` doc)
    - `internal/obs/simsrc/simsrc.go` (with `Package simsrc` doc)
  - Added a brief property comment to every test and benchmark function (and doc comments on `csvflush/export_test.go`) across all 21 Chunk 3 test files (`internal/obs/{collector,collector_repair,cost,merge,mergecheck,synthetic}_test.go`, `catalog/catalog_test.go`, `csvflush/{canary,csvflush,export,repair,roundtrip}_test.go`, `jsonstats/jsonstats_test.go`, `logline/logline_test.go`, `obstest/{conformance,headline}_test.go`, `promtext/promtext_test.go`, `simrun/{csvassert,imports,registry,simrun}_test.go`, `simsrc/simsrc_test.go`).
  - Untouched: all Chunk 0/1/2 Go files, `tools/`, `agent_work/adversarial/`, `testdata/`, `docs/csv-schema.md`, `docs/c4/`, `PLAN.md`, `STATE.md`.
- **Book chapter and manifest:**
  - Wrote `book/chapters/03-observability-spine.md` ("Numbers without a framework: snapshots, a collector, exporters"), including real `run.csv`/`metrics.csv`/`histograms.csv` excerpts, real `scripts/csv-assert.sh` output, two real DuckDB `1.5.5` queries with their exact outputs (baseline summary across `runs JOIN final` and coordinated-omission p50/p99 over `final_hist`), cost-model measurements, conformance/canary/gate coverage, review and adversarial history, narrowed claims, carried nits, and stated limits.
  - Marked Chapter 3 `"status": "live"` in `book/manifest.json` and updated `book/index.md` to list Chapters 1, 2, and 3 as live.
- **Reader-facing pages:**
  - Updated `README.md` with Chunk 3 status, Chapter 3 link, generated `docs/csv-schema.md` link, DuckDB `1.5.x` requirement, `scripts/csv-assert.sh` / `make csv-assert`, and `scripts/gen-docs.sh --check`.
  - Updated `docs/index.md` with Chunk 3 status and Chapter 3 links.

## Narrowed claims and evidence discipline

- **Claim 2 (determinism):** Written strictly as narrowed—every adapter's output is a deterministic function of its configuration and the full sequence of snapshots it has exported so far, explicitly distinguishing stateless adapters (`jsonstats`, `logline`) from stateful adapters (`promtext` eviction, `csvflush` row persistence).
- **Claim 12 (declared and documented):** Written strictly as narrowed—no metric can be exported without being declared (`Emitter`, `Merge`), while documentation in `docs/csv-schema.md` is enforced for every source listed in `internal/obs/catalog.Registry()` (with the Chunk 6 follow-up noted for checking every source registered by the real binary).
- **Adversarial counts:** Reported exact numbers from `RESULTS.md` (**HELD: 22, BROKEN: 14** across 36 named attacks on the initial build) and `ADVERSARIAL.md` / `REVIEW-2.md` (**32 of 36 named attacks pass**, plus the `TestReplayWorker` helper = 33 passing tests, **0 fail**, **4 settled** with `t.Skip`—2 fixed another way at registration and 2 settled by narrowing Claims 2 and 12).
- **Gate result file:** `agent_work/chunks/c03-obs-spine/gate/result.json` was not committed in the worktree (per builder rules), so build-stage gate figures were taken directly from the existing `agent_work/chunks/c03-obs-spine/gate/G*.log` logs and `NOTES.md`, without claiming a committed `final`-stage `result.json`.
- **Carried Review 2 nits and limits:** All three Review 2 minor items carried to Chunk 6 (`Registry.Declare` non-atomicity on direct variadic calls, missing `flushErrors` increment on zero-flush `Close()` open failure, `selfView` aliasing `c.last.Hists`) and all limits from `NOTES.md` are reported plainly.
- **Cost benchmarks:** Both the builder's 2000× numbers from `NOTES.md` and our own 500× measurements on vm-1 are reported side by side with the ±30% shared-VM variance caveat; zero-allocation claims are tied strictly to the `TestRecordingAllocatesNothing` gate.

## Commands run

```sh
export PATH="/usr/local/go/bin:$HOME/.local/bin:$PATH"
# Captured real CSV files, csv-assert.sh output, DuckDB 1.5.5 queries, registry count, and benchmarks
RUN_DIR="$tmp/runs" go test -count=1 -run '^TestScenarioFlush$' ./internal/obs/simrun -seed=1
scripts/csv-assert.sh "$tmp/runs"
duckdb -batch -box :memory:   # baseline summary over runs JOIN final; p50/p99 over final_hist
go test -v -count=1 -run '^TestEveryDeclaredMetricIsEmittedAndEveryEmittedOneDocumented$' ./internal/obs/simrun
go test -run '^$' -bench 'Record|Publish' -benchtime 500x ./internal/obs/
go test -run '^$' -bench FlushFixture -benchtime 500x ./internal/obs/csvflush/

# Verified formatting, AST identity, build, vet, full tests, prose paths, and site build
gofmt -l .
(cd tools/detlint && go run ./cmd/astdiff "$tmp_preprose" "/root/Software/mini-gateway-work/c03")
go build ./... && go vet ./... && go test -count=1 ./...
scripts/site.sh "$tmp_site"
```
