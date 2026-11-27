# mini-gateway — change request CR-2: one retry in fuzz.sh for Go's own deadline error — builder brief

Seat: Opus. Chunk directory name: `cr02-fuzz-deadline-retry`.
Worktree: `/root/Software/mini-gateway-work/cr02` — branch `work/cr02-fuzz-deadline-retry`.
Small: about 40 lines of shell and 150 of test.

Read first: `/root/briefs/zgateway-p2-builder-rules.md` (they bind you);
`agent_work/chunks/cr02-fuzz-deadline-retry/CHANGE-REQUEST.md` (the request and its
evidence — it is your scope, exactly); `scripts/fuzz.sh`, `scripts/fuzz_test.go` (the
`FUZZ_GO` stub seam and the recorded-output tests of CR-1), `scripts/gate.sh` (how G5's
counts reach `result.json`), `scripts/gate-canaries.sh` (the G5 canaries),
`docs/knowledge/foundation-gates.md` ("G5: fuzz rates and seed sweeps");
`agent_work/chunks/cr01-shrink-fuzz-rules/NOTES.md`.

## Deliverables

1. `scripts/fuzz.sh`: the retry exactly as the change request defines it — the signature
   (all five conditions), one retry per target, the second run's result stands, at most
   two retried targets per run (`FUZZ_MAX_RETRIES`, default 2; 0 turns the retry off; the
   gate does not let a caller raise it above 2 for a landing run), the retry printed as a
   line `fuzz: RETRY: <dir> <target>: go's own deadline, no failing input`, a `retried`
   column (0 or 1) at the END of the report's columns so existing readers of the first
   five still work, and the NOTE line saying how many were retried.
2. `scripts/gate.sh`: `fuzz_retries` in `result.json` beside the other G5 counts (`null`
   when G5 did not run); it never changes whether the run counts for landing by itself.
3. `scripts/fuzz_test.go` (fast, through the `FUZZ_GO` stub, no real fuzzing): the
   signature retried and passing; the signature twice in a row fails; each near miss is
   NOT retried and fails at once — the same message with a "Failing input written to"
   line; the same message plus a panic; a different message under `--- FAIL`; the message
   appearing only inside a passing target's log line; a new file under
   `testdata/fuzz/<target>/`; a third target with the signature when two were already
   retried; `FUZZ_MAX_RETRIES=0`. The rate comes from the run that stands. The stub must
   be told which invocation it is serving (a counter file), so a test can give different
   output on the first and second run of the same target.
4. Two gate canaries in `scripts/gate-canaries.sh`: `g5-retry-reported` (a planted
   first-run deadline signature is retried, reported and counted) and
   `g5-crasher-not-retried` (a planted failing input is never retried and fails G5).
5. `docs/knowledge/foundation-gates.md`: the rule, in the G5 section. NOTES.md at
   `agent_work/chunks/cr02-fuzz-deadline-retry/NOTES.md`.

## Claims (an adversary will attack each)

1. A real failure is never retried: any first run that wrote a failing input, panicked,
   or printed any failure text other than the exact signature fails the run at once.
2. The retry happens at most once per target and for at most two targets per run.
3. A second failure of a retried target fails the run, whatever its message.
4. Every retry is visible: the RETRY line, the report column, the NOTE line and
   `fuzz_retries` in the gate result agree.
5. Nothing else changed: rates, the floor, the seed-sweep class, fail-closed on an
   unreadable rate, and the first five report columns are as before (CR-1's tests and
   canaries pass unchanged).
6. The retry cannot be widened from the environment for a landing run.

## Finish

As the builder rules say. Run the fast checks and `go test ./scripts/`; then the two new
canaries (`CANARY_ONLY=g5-retry-reported,g5-crasher-not-retried scripts/gate-canaries.sh`)
and `GATE_ONLY=G0,G1,G2,G5 scripts/gate.sh cr02-fuzz-deadline-retry`. Do NOT run the full
gate (another gate is running on this machine; the orchestrator runs the landing gate).
Summary, then a final line containing exactly AGENT-DONE.
