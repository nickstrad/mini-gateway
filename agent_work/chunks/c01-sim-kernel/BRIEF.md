# mini-gateway — chunk 1: Simulator kernel (Commit 0, part 1) — builder brief

Seat: Opus (product Go and tests, logic only). Chunk directory name: `c01-sim-kernel`.
Worktree: `/root/Software/mini-gateway-work/c01` — branch `work/c01-sim-kernel`.

Read, in this order, before writing anything:

1. `/root/briefs/zgateway-p2-builder-rules.md` — the rules for every builder. They bind you.
2. `PLAN.md`: "Chunk 1 — Simulator kernel" (your scope), section 5 (what is copied from the
   object-store simulator and what is built new), section 6.2 (the testing rules), section
   7.1–7.3 (snapshot conventions), decisions D1 and D2, and "Chunk 2" (your first consumer:
   read it so your API serves it, but build none of it).
3. `agent_work/phase1/research/q8-object-store-simulator-evaluation.md` — file-by-file
   guidance on what to copy from `/root/Software/object-store` (read-only for you).
4. `docs/knowledge/foundation-gates.md` — the gate, the rules files, the marker mechanism.

## Purpose

The deterministic core of the test harness: a virtual clock, one seeded single-threaded
event loop, simulated byte-stream connections, and the proof that a run repeats exactly.
No proxy code and no Postgres knowledge in this chunk.

## Deliverables

### `internal/sim` — the kernel

- `Time`: 64-bit nanoseconds since the start of the run, with saturating add/sub helpers
  (copy from object-store `internal/sim/clock.go` / `internal/env`; only the time type and
  helpers, not the clock and timer interfaces).
- Named random streams: `NewStream(seed, label)` (copy object-store `internal/sim/stream.go`
  as-is). Stream isolation: drawing more from one label never shifts another.
- The event heap ordered by (time, sequence) with real removal on cancel (copy object-store
  `internal/sim/queue.go` lines for the indexed min-heap; drop its domain event kinds).
- An **opaque-event loop**: events carry a time, a sequence number and a callback or an
  opaque handler value — the loop knows no domain kinds. `Step`, `RunUntil(t)`, `RunFor(d)`,
  `RunUntilQuiet`, a step budget with a typed stall error (budget exhausted; time not
  advancing), and a panic guard that converts an actor panic into a typed failure carrying
  the seed and the REPLAY line. Events scheduled for the same instant run in sequence order.
- Cancellable timers as value handles (`After(d, fn) Timer`, `Timer.Cancel() bool`),
  thousands at once; a cancelled timer never fires and is removed from the heap.
- Invariant registration: `RegisterInvariant(name, func() error)` checked after every event
  (with a sampling option for expensive ones), plus a **global quiesce check** that actors
  register into (`RegisterQuiesce(name, func() error)`): after a scenario it must report
  nothing in use, nothing queued, no pending timer other than those explicitly marked
  background.
- A **decision trace** any actor can append to (fixed-shape integer records plus a short
  kind tag; no pointers, no formatted floats), which can be switched off, with **two
  separate SHA-256 digests**: one over the trace, one over final state (actors register a
  `StateDigest(w)` writer; registration order is fixed, never map order). A bounded ring of
  the last N records is kept for failure reports.
- A **fault schedule**: timed fault entries (kind, target, parameters) applied by the loop
  at their instants and recorded in the trace; `ScheduleFromBytes([]byte)` decoding for the
  fuzzer (total: every byte string decodes to a valid schedule, bounded length); and a
  **delete-one-fault shrinker** (`Shrink(schedule, stillFails func) schedule`) on the
  standard library (no `rapid`). Fault kinds are an open registry the world packages extend
  (chunk 2 adds database and client faults); this chunk registers the simnet kinds.

### `internal/sim/simnet` — simulated byte-stream connections

- `Listen`/`Dial`/`Accept` between named endpoints on the loop; a connection is two ordered
  byte streams.
- Delivery chunking configurable per connection and per direction: whole writes, fixed
  chunk size down to **one byte**, or seeded random splits. Bytes out = bytes in, in order,
  for every chunking.
- Latency per direction (fixed, or seeded integer distribution), on the virtual clock.
- A **bounded send window**: a writer is told how many bytes were accepted; when the window
  is full, `Write` accepts 0 and the writer gets a "writable" event when space frees
  (back pressure). Reads are event-driven: the reader gets "readable" and reads up to n.
  The reader can stop reading (the window then fills end to end).
- Half-close (FIN after pending bytes drain), reset (pending bytes discarded, both ends see
  the reset in their defined order), stall (bytes stop moving in one or both directions for
  a window, then resume), **half-open** (one end is gone, the other sees no reset: its
  writes are accepted up to the window and never acknowledged), accept-then-silence, dial
  refused, dial stalled.
- Every fault above is injectable per connection through the fault schedule, and clearable.
- `Snapshot()`: connections opened, open now, closed by reason; bytes written, delivered and
  discarded by direction; faults injected by kind; writes refused by a full window.
- Quiesce registration: no open connection, no byte in flight.

### `internal/sim/simtest` — seeds and replay plumbing

Copy and adapt object-store `internal/sim/simtest/seeds.go` and `determinism.go`: the
`-seed` flag, `DST_SEED` and `DST_SEEDS` variables, pinned seeds in `testdata/seeds.txt`,
`Seeds`, `Sweep`, the run-twice check (`RequireDeterministic`), the `REPLAY:` line printed
on every failure with the exact command that reproduces it. Keep those names.

### `internal/obs` — measurement half only

- Snapshot conventions as code: the small helpers and interfaces every later `Snapshot()`
  follows (plain structs; integer counters and gauges; a `Counters`-style conservation
  helper asserting accepted = completed + failed + shed + queued + in flight, with named
  terms so a failure says which side is off).
- The **integer histogram**: fixed-size, integer buckets, no floats; record, merge,
  percentile (returns a bucket upper bound); range 1 µs to 60 s at two significant digits
  for durations, with a generic constructor for other ranges (bytes, counts); out-of-range
  values are clamped and counted in separate low/high clamp counters; zero allocations per
  recorded value (an allocation test proves it). Bucket iteration yields (lo, hi, count)
  for later exporters.
- Nothing else of `internal/obs` (collector and adapters are chunk 3).

### The toy and the CLI

- `internal/sim/simtoy`: a toy line-echo protocol (a client actor sends lines, a server
  actor echoes them, over simnet) with a **planted-bug switch** reachable only from tests
  (for example: the server drops the last byte of a line that straddles a delivery
  boundary). Used only by the meta-tests and `cmd/mgsim`.
- `cmd/mgsim`: skeleton CLI — `mgsim run <scenario> -seed N [-trace]` runs a named scenario
  outside `go test`, prints the two digests and the REPLAY line; `mgsim list`. The only
  scenarios at this chunk are the toy's.
- `scripts/sweep.sh`: build it (delete its marker): `scripts/sweep.sh [-seeds N]
  [-run REGEXP]` runs the simulator scenario tests over N seeds (default 200) via
  `DST_SEEDS`, prints a REPLAY line for the first failure, exits non-zero on any failure.
- `scripts/fuzz.sh`: build it (delete its marker): runs each fuzz target in the repository
  for `FUZZ_SECONDS` (default 20) each; lists targets it ran; non-zero on any crasher.

Copied object-store files carry their source in a way that survives the no-comments rule:
record every copied file, its source path and the source commit
(`git -C /root/Software/object-store rev-parse HEAD`) in
`docs/knowledge/sim-kernel-provenance.md`, not in Go comments.

## Tests (write these first)

**Meta-tests (the harness tests itself):**
- Same-process replay: the toy scenario run twice with one seed → identical trace digest
  and identical state digest. Different seeds → different trace digests.
- Cross-process replay: a test that runs `mgsim` (or the test binary) twice as a
  subprocess and compares digests.
- Golden digests for the pinned seeds, in testdata (a deliberate, reviewed update path:
  `-update` flag).
- Stream isolation: an extra draw on stream A leaves every value of stream B unchanged.
- **Planted bug caught and shrunk**: with the toy's bug switch on and a fault schedule of
  many faults, the scenario fails, and the shrinker reduces the schedule to a minimal one
  that still fails (assert an upper bound on its length).
- **Harness self-tests**: a fault on connection A leaves traffic on connection B
  byte-identical to a run without the fault; a fault that is injected and cleared before
  any traffic leaves the run identical (trace digest of the traffic part, state digest) to
  a never-faulted run; the quiesce check FAILS when a connection, an in-flight byte or a
  timer is leaked on purpose; the step budget turns a livelock into the typed stall error;
  a panic in an actor becomes the typed failure with a REPLAY line.
- Trace off vs on: the state digest is identical either way.

**Property tests** (seeded, standard library; many seeds under `-short`-aware budgets):
- Heap: pops are ordered by (time, sequence); cancel removes exactly that entry; against a
  sorted-slice reference model.
- Timers: a cancelled timer never fires; N timers fire in (deadline, creation) order; time
  saturates instead of overflowing.
- Histogram: recorded value falls in its reported bucket; relative error of the percentile
  ≤ the stated bound (1% at two significant digits — state and test the exact bound you
  implement); merge(a, b) equals recording both streams into one; clamps are counted;
  zero allocations per record.
- Byte streams: for every chunking mode and random write sizes, bytes delivered = bytes
  written, in order; total in flight never exceeds the window; a stopped reader makes the
  writer block within window bytes; after half-close the reader sees all bytes then EOF;
  after reset no further byte is delivered; half-open end never learns.
- `ScheduleFromBytes` is total (fuzz target `FuzzScheduleFromBytes`: no panic, bounded
  length) and applying any decoded schedule to the toy never panics the kernel.

**End-to-end for this chunk:** the toy echo under EVERY simnet fault kind, on the pinned
seeds, each as a named scenario with: the expected outcome class (echo completes / client
sees reset / client times out on its own virtual-clock deadline), the recovery assertion
(after the fault clears, a fresh exchange succeeds), the quiesce check, the replay check,
non-zero fault counters in the simnet snapshot ("counters must move"), and for the stall
and chunking faults the ablation: the same seed without the fault gives the control result.

## Observability obligations (landing gate)

- `simnet.Snapshot()` as above, with a test that every counter moves in some scenario and
  that bytes written = delivered + discarded + in flight at every snapshot.
- The histogram and snapshot conventions land here; everything later measures through them.
- Snapshots contain no time and no map-order dependence: two runs with one seed give
  byte-identical snapshots (encode with `encoding/json` or `fmt` of plain structs in a test).

## Claims this chunk makes (an adversary will try to break each)

1. Replay law: same seed, same binary → bit-identical trace digest and state digest, in
   process and across processes; a `REPLAY:` line reproduces any failure.
2. Stream isolation.
3. Events at one instant run in sequence order; a cancelled timer never fires; time never
   overflows.
4. Byte streams deliver exactly the written bytes in order under every chunking and
   latency setting; the send window bounds in-flight bytes; back pressure reaches the writer.
5. Half-close, reset, stall, half-open, accept-then-silence, dial refused and dial stalled
   each behave as their definition in your NOTES.md says, for both ends.
6. Fault isolation: a fault on one connection changes nothing on another; a cleared fault
   leaves no residue.
7. The quiesce check catches a leaked connection, byte or timer.
8. The histogram's stated error bound, merge law and clamp counting hold; recording
   allocates nothing.
9. The shrinker returns a schedule that still fails and from which no single fault can be
   removed without the failure disappearing.
10. Nothing in `internal/sim/...` or `internal/obs` reads the wall clock, starts a
    goroutine, uses a global random source, floats, or lets map order reach an output
    (the determinism lint passes with an allow file that is empty or justified line by line).

Define each fault's exact semantics in NOTES.md (who sees what, in what order) — the
adversary and chunk 2 both work from those definitions.

## Rules files to update

- `tools/lint-rules/det-packages.txt`: add `internal/sim/...` and `internal/obs`
  (`cmd/mgsim` and `simtest` may need `os`/`flag`: keep them out of the determinism list or
  allow-list them with a justification; the flags-and-environment code in `simtest` is
  test plumbing, not simulation).
- Makefile: `make test` now runs the CI set (delete its marker).

## Steady-state row (PLAN.md section 10.1)

What grows here: the event heap, the trace ring, per-connection buffers. What removes it:
event execution and cancel; ring overwrite; delivery, close and reset. Which gauge shows the
level: heap length and pending timers on the kernel snapshot; bytes in flight and open
connections on the simnet snapshot. Provide a kernel `Snapshot()` with those gauges.

## Size

About 1,500 lines of product code, budgeted up to twice that. Tests at least as large.

## Finish

As the builder rules say: gate green at the build stage (`scripts/gate.sh c01-sim-kernel`),
NOTES.md complete at `agent_work/chunks/c01-sim-kernel/NOTES.md`, clean tree, summary, and a
final line containing exactly AGENT-DONE.
