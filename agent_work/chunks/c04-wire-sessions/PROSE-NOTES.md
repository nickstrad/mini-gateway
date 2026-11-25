# Chunk 4 — Wire sessions: prose pass notes (Neon seat)

Date: 2026-11-25  
Branch: `work/c04-wire-sessions`  
Pre-prose commit: `e71b59c`

## What was written

1. **Go comments (comments only, zero AST changes vs `e71b59c`):**
   - `internal/pgwire`: package doc comment (in `frame.go`; no `doc.go` added per brief), doc comments on every exported type, constant, variable, function, and method, plus concise inline comments on key invariants (lazy body growth/drop, last-value-wins startup params, CVE-2021-23214/23222 early-bytes guard, unknown-user mock SCRAM exchange, `AnswerNone` conservation on early fail, clearing `s.scram` on close/fail, skipped `AuthenticationSASLFinal` check, single-use SCRAM step, and `CycleTracker` body skipping) across `frame.go`, `frame_encode.go`, `fatal.go`, `params.go`, `prestartup.go`, `client.go`, `client_tenants.go`, `backend.go`, `scram.go`, `scram_server.go`, `scram_client.go`, `cycle.go`, and `snapshot_wire.go`.
   - `internal/pgwire/pgwiretest` and `internal/pgwire/pgwiretest/wireworld`: package doc comments, exported identifier doc comments, and test-property comments across `proxy.go`, `fidelity.go`, `fidelity_sim.go`, `wireworld.go`, `proxy_test.go`, `fidelity_test.go`, and `fidelity_sim_test.go`.
   - `internal/sim/simtls`: package doc comment, exported identifier doc comments, and test comment (`simtls.go`, `simtls_test.go`).
   - `internal/obs/wiresrc`: package doc comment, exported identifier doc comments, and test comments (`wiresrc.go`, `wiresrc_test.go`).
   - Chunk 4 additions to existing packages: doc comments on added exported identifiers in `internal/sim/fakepg/auth.go`, `internal/sim/fakepg/frame.go` (`TakeRaw`), `internal/sim/workload/startup.go`, `internal/obs/simsrc/simsrc.go` (`FakeAuthMetrics`, `MapFakeAuth`), and `internal/obs/simrun/wire.go` (`WireInterval`, `WireFingerprint`, `Wire`), plus test comments on `internal/pgwire/*_test.go`, `internal/sim/workload/startup_test.go`, `internal/obs/simrun/wire_test.go`, and `internal/obs/simrun/wiresql_test.go`.
2. **Book chapter and manifest:**
   - Added `book/chapters/04-wire-sessions.md` ("Speaking Postgres: TLS or not, startup, login, fragments").
   - Set Chapter 4's `status` to `live` in `book/manifest.json` and updated `book/index.md` to list Chapters 1 through 4 as live.
3. **Reader-facing status pages:**
   - Updated `README.md` (status, live Chapter 4 link, Chunk 5 paused note, 11 fuzz targets, 62 SQL files with `TestScenarioWireFlush`, tier R SCRAM container).
   - Updated `docs/index.md` (Chunk 4 landed, Chapter 4 live link, Chunk 5 explicitly stated as paused while its scope is reviewed).
   - Untouched: `PLAN.md`, `STATE.md`, `docs/csv-schema.md`, `docs/c4/`, all `agent_work/` files except this file.

## Claims narrowed or left out for lack of evidence

- **Claim 9 (proxy-issued key uniqueness):** Written strictly as narrowed in `ADVERSARIAL.md` and `NOTES.md`—unique among the sessions of one `KeyIssuer` (2^31 − 1 unique non-zero process IDs per issuer, random 4-byte secret, unrelated to backend keys); two independent issuers fed the same random stream issue the same keys.
- **Claim 5 (unknown user vs wrong password indistinguishability):** Written strictly as narrowed—holds for reply byte shapes and for SCRAM challenges (`s=` length, `i=`) when every verifier in the tenant table shares the same iteration count and salt length (default `MockIterations = 0`); mixed tables can leak via `i=` or `s=`; timing is explicitly excluded.
- **Real TLS cryptography and CPU cost:** Did not claim per-statement TLS encryption cost or real socket TLS termination in `internal/pgwire` (deferred to Chunk 6 / Chunk 14; simulated TLS is described accurately as 16-byte tokens modelling outcome and virtual duration).
- **Repeated GSS requests against live Postgres 17:** Only repeated `SSLRequest` was observed live against Postgres 17.11 (because the `postgres:17` image answers `G` to GSS requests); stated accurately without overclaiming live GSS verification.
- **Chunk 5 (statement classifier):** Not described as built; stated plainly as paused while its scope is reviewed.
- **Gate `result.json`:** `agent_work/chunks/c04-wire-sessions/gate/result.json` was deleted by the builder prior to commit per `NOTES.md:500`; gate numbers quoted in Chapter 4 come directly from `NOTES.md`, `REVIEW-3.md`, and the committed `agent_work/chunks/c04-wire-sessions/gate/G*.log` logs, plus commands run directly during this prose pass.

## Commands run during this prose pass

- `gofmt -l .` (empty)
- `go build ./... && go vet ./...` (exit 0)
- `tools/detlint/cmd/astdiff` comparing `e71b59c` against the working tree (exit 0, zero AST differences)
- `go test -v -count=1 ./internal/pgwire -run '^(TestBytesSentEarlyAfterAnAcceptedTLSRequest|TestWrongPasswordAndUnknownUserLookAlike|TestPlantedEarlyBytesBugFailsTheSmuggleScenario|TestCycleTrackerStreamsManyRowsWithoutBuffering|TestScramRFC7677ServerSide|TestScramRFC7677ClientSide)$'` (pass, 0.194s)
- `go test -v -count=1 ./internal/pgwire/pgwiretest` (pass, 0.164s)
- `RUN_DIR=/tmp/c04-wire-runs go test -count=1 -run '^(TestScenarioFlush|TestScenarioWireFlush)$' ./internal/obs/simrun -seed=1 && scripts/csv-assert.sh /tmp/c04-wire-runs` (`62 SQL files run, 0 failures; v1.5.5 (Variegata) d8cdaa33fd`)
- DuckDB `1.5.5` query via `test/analysis/views.sql` over all 19 flushed wire scenario runs (seed 1) producing the summary table quoted in Chapter 4
- `go test -count=1 ./...` (full repository test pass verified after final edits)
- `scripts/site.sh /tmp/c04-site` (local site build and link check verifying `book/04-wire-sessions.html` is built and listed in `book/index.html` and `pages.txt`)
- `GATE_STAGE=final GATE_PREPROSE=e71b59c GATE_ONLY=G0,G1,G2,G8,G9` verification after committing
