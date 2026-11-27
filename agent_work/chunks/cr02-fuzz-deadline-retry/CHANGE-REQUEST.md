# Change request CR-2 — one retry in `scripts/fuzz.sh` for the Go fuzz tool's own deadline error

Filed by the Fable orchestrator on 2026-11-27 against the landing gate (G5). **Not ruled
by Nick: it is listed in STATE.md as his to veto.** It goes through the same chain as a
chunk (Opus build, Neon review, Astra attack, gate, landing), so the gate is not changed by
the seat that runs it.

## The evidence

Go's fuzz tool (go1.26.8) sometimes ends a target at the end of its `-fuzztime` with

    --- FAIL: FuzzX (20.83s)
        context deadline exceeded
    FAIL

and writes no failing input. It is the tool reporting its own time limit, not the target
failing. `scripts/fuzz.sh` fails closed on any non-zero exit, so the whole gate (about 40
minutes) is lost. Seen four times in two days, on four different targets, none of which
the chunk under test had changed:

| When | Gate run | Target | Machine |
| --- | --- | --- | --- |
| 2026-11-26 | CR-1 repair, build stage | `internal/sim FuzzScheduleFromBytes` | loaded |
| 2026-11-26 | CR-1 repair, G5 only | `internal/pgwire FuzzClientSession` | loaded |
| 2026-11-26 | chunk 5 final, run 1 | `internal/pgwire FuzzLex` | quiet |
| 2026-11-27 | chunk 6 final, run 2 | `internal/pgwire FuzzClientSession` | quiet |

Each target passed when run again. With 16 targets and more coming, the chance that a
gate run meets it at least once is high.

## What is requested

`scripts/fuzz.sh` runs a target a second time, once, when — and only when — its first run
ended with exactly this signature: non-zero exit, a `--- FAIL: <target>` block whose only
message is `context deadline exceeded`, no "Failing input written to" line, no panic or
other failure text, and no new file under the package's `testdata/fuzz/<target>/`. The
second run's result stands, whatever it is. The retry is printed, written to the report
(a `retried` column), counted in the gate's `result.json` (`fuzz_retries`), and bounded:
more than two retried targets in one run fails the run, because that many is no longer
an accident.

## What does not change

Any other failure fails the run at once, as now. A target with no readable rate still
fails closed. The rate table, the floor and the seed-sweep class are as CR-1 left them
(the rate is taken from the run that stands).

## Status

| Step | State |
| --- | --- |
| Filed | 2026-11-27, this file |
| Brief | `BRIEF.md` beside this file |
| Build, review, attack, landing | see STATE.md |
