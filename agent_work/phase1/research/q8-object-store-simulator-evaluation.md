# Q8 evidence: can the object-store simulator be mini-gateway's starting point?

Date: 2026-11-24. Read-only evaluation of `/root/Software/object-store` at HEAD `379e622`
(tag `freeze-0b` = `ceb5438`). No repo file was modified; `git status` in object-store was
clean after the test run.

**Short answer: (b).** Copy a small set of files and conventions and write a new kernel in
mini-gateway. About 5% of `internal/sim` product lines survive unchanged, about 19% more
survive as edited code, and about 76% is cluster/disk/datagram machinery that does not
transfer. The single largest need, a byte-stream connection with partial reads,
backpressure, half-close and reset, does not exist there in any form.

Legend: **[V]** verified by reading the code or running it. **[I]** my inference.

---

## 1. Inventory

Go version and dependencies [V]: `go.mod` declares `go 1.26.0`, `toolchain go1.26.11`, and
one requirement, `pgregory.net/rapid v1.3.0`. Installed toolchain is `go1.26.11 linux/amd64`.
rapid is imported only from `_test.go` files (in the simulator: `internal/sim/prop_test.go`).
Non-test simulator code imports the standard library plus `internal/env` only.

`testing/synctest` [V]: not used anywhere in `internal/sim`, `internal/env`, `simtest`,
`simtoy` or `cmd/objsim`. It is used only by the real-runtime layer's tests
(`internal/noderuntime/hazards_test.go:11`, `request_once_test.go:7`).

### Product code (`wc -l`)

| File | Lines | What it does |
|---|---|---|
| `internal/env/env.go` | 144 | `NodeID`, `Region`, `Time`, interfaces `Clock`, `Timer`, `Transport`, `Disk`, `Process`, the `Env` bundle |
| `internal/env/errors.go` | 174 | sentinels, wire status `Code`, `CodeTable`, `RemoteError` |
| `internal/sim/sim.go` | 799 | `Config`, `Sim`, node registry, `schedule`, `Step`/`Run*`, `exec` dispatch, invariants, panic guard, error types, `At`, `Workload` |
| `internal/sim/queue.go` | 178 | event kinds, dispositions, the `event` struct, indexed min-heap |
| `internal/sim/clock.go` | 114 | per-node skew/drift math, saturating time helpers, timers |
| `internal/sim/handle.go` | 165 | per-incarnation `Transport` (Send, Request), serve/refuse/complete, `cancel` |
| `internal/sim/net.go` | 318 | `NetConfig`, directed links, latency, drop/duplicate, partitions, clog |
| `internal/sim/process.go` | 146 | crash, torn crash, restart, pause/resume, wipe-rejoin, heal-all |
| `internal/sim/disk.go` | 381 | two-layer (written/durable) blob disk, bit rot, read errors |
| `internal/sim/fault.go` | 250 | `Fault`, `FaultKind` (18 kinds), `Apply`, `InjectAt`, `ScheduleFromBytes` |
| `internal/sim/nemesis.go` | 409 | chaos/heal phase driver with a fault `Budget` |
| `internal/sim/log.go` | 170 | 38-byte event records, ring buffer, SHA-256 event and state digests |
| `internal/sim/stream.go` | 19 | `NewStream(seed, label)` PRNG derivation |
| `internal/sim/rand.go` | 17 | stream label constants, `NodeStream` |
| **`internal/sim` product total** | **2,966** | |
| `internal/sim/simtest/seeds.go` | 156 | `-seed` flag, `DST_SEED*`, pinned `testdata/seeds.txt`, `Seeds`, `Sweep` |
| `internal/sim/simtest/determinism.go` | 153 | `CheckDeterministic`, `RequireDeterministic`, `Report`, `ReplayCommand` |
| `internal/sim/simtest/cluster.go` | 19 | `Cluster` helper |
| `internal/sim/simtoy/simtoy.go` | 367 | max-register flood toy protocol used by the simulator's own tests |
| `cmd/objsim/main.go` + `scenarios.go` + `help.txt` | 104 + 13 + 10 | run a named scenario from a seed outside `go test`, print digests, REPLAY line |
| **All product** | **4,106** | |

### Tests

`internal/sim/*_test.go` 3,921 lines; `simtest/simtest_test.go` 350; `env/env_test.go` 171;
`cmd/objsim/main_test.go` 152. Total 4,594.

### Related code outside the simulator

- `internal/saga/sagasim/` (2,291 product lines in `cluster.go` 512, `codec.go` 250,
  `matrix.go` 537, `ops.go` 170, `scenarios.go` 256, `store.go` 566, plus 1,142 test lines):
  a model cluster and failure-point matrix for the saga engine. Entirely saga-specific. Its
  only transferable content is the pattern of a workload client written as a node
  (`cluster.go:259-316`).
- Determinism linter: `agent_work/gates/detlint/`, a nested module `detlint` (`go 1.26`,
  stdlib only). `detlint.go` 835, `load.go` 384, `main.go` 49, `internal/report/report.go`
  200 = 1,468 lines, plus `detlint_test.go` 302 and a `testdata/mod` fixture tree. Four
  sibling tools under `cmd/`: `importcheck`, `paniccheck`, `nocomments`, `astdiff`.
- Notes: `agent_work/0b-simulator/build-notes.md` (628), `frozen-api.txt`,
  `REVIEW-1..3.md`; `agent_work/adversarial/0b-simulator/{ATTACKS,RESULTS}.md`;
  `docs/knowledge/chunk-0b-simulator.md`; `docs/knowledge/sim-kernel-extraction-note.md`;
  `book/chapters/01-deterministic-simulation.md` (246); `PLAN.md` sections 2.1, 2.9, 5.1, 7.1.

---

## 2. Core abstractions

### Environment interface (`internal/env/env.go`) [V]

```go
type NodeID string                     // :17
type Region string                     // :20
type Time int64                        // :23  virtual nanoseconds

type Clock interface {                 // :39
	Now() Time
	AfterFunc(d time.Duration, fn func()) Timer
}
type Timer interface {                 // :50
	Stop() bool
	Reset(d time.Duration) bool
}
type Transport interface {             // :62
	Send(to NodeID, msg []byte) error
	Request(to NodeID, req []byte, timeout time.Duration, cb func(reply []byte, err error))
}
type Disk interface { Read; Write; Append; Sync; Delete; List }   // :77
type Reply func(resp []byte, err error)                           // :100
type Process interface {               // :103
	Start()
	OnDatagram(from NodeID, msg []byte)
	OnRequest(from NodeID, req []byte, reply Reply)
	EncodeState() []byte
}
type Env struct { ID NodeID; Region Region; Clock Clock; Rand *rand.Rand; Net Transport; Disk Disk }  // :119
```

Protocol cores here are callback-style objects that hold an `Env` and call out
(`Clock.AfterFunc`, `Net.Request` with a callback). They are not "event in, actions out"
state machines. That is a style difference from mini-gateway's stated sans-I/O shape.

### Scheduler and event loop [V]

- Ordering: indexed min-heap ordered by `(at, seq)`; `seq` is a global counter assigned in
  `schedule`, so ties are FIFO by scheduling order (`queue.go:86-93`, `sim.go:440-452`).
  No randomised tie-breaking.
- Cancellation removes the entry from the heap in O(log n) through the stored heap index
  (`queue.go:153-171`, `handle.go:152-166`). No tombstones accumulate.
- `Step` pops one event, advances `now`, executes, records it, then runs due invariants
  (`sim.go:565-599`). Drivers: `RunUntil`, `RunFor`, `RunUntilQuiet`, `RunUntilConverged`
  (`sim.go:603-670`). Stalls are typed: `ErrQueueEmpty`, `ErrBudgetExhausted`
  (`DefaultMaxSteps = 10_000_000`, `sim.go:33`), `ErrDeadline`.
- Time is bounded by `Horizon = 1<<62` ns; events past it are never queued (`sim.go:37, 444`).
- Every callback into user code runs under `guard`, which turns a panic into a sticky
  `*PanicError` with seed and stack (`sim.go:540-553`).
- Seeding: `NewStream(seed, label) = rand.New(rand.NewPCG(uint64(seed), fnv64a(label)))`
  (`stream.go:10-12`). Streams: `net`, `disk`, `nemesis`, `workload`, and `node/<id>`
  (`rand.go:10-17`), created in `New` (`sim.go:239-242`) and `AddNode` (`sim.go:326`).

### Clock and timers [V]

Each node has its own skewed, drifting view: `local = g + skew + g*drift/1e6` in integers
(`clock.go:15-18`); `AfterFunc(d)` converts local `d` to a global delay (`clock.go:23-42`).
A timer is one heap event; `Stop` removes it; `Reset` is `Stop` plus a newly allocated
event (`clock.go:74-113`). Each `AfterFunc` allocates a `timer` and an `event`; the `event`
struct is wide, about 20 fields including payload, fault, nemesis and release bookkeeping
(`queue.go:59-82`).

### Network model [V]

Message-oriented. Two classes, no connection object.

- Datagrams: per-send partition check, per-link loss, global `DropPPM`, `DuplicatePPM`
  (second copy with its own latency), latency drawn uniformly from a `Range` per copy, so
  reordering is natural (`net.go:101-113`).
- Request/response ("stream" in this codebase's vocabulary): one whole message each way,
  up to `MaxMessage` (16 MiB), FIFO per **directed node pair** by forcing each delivery time
  to be at least the link's previous one (`net.go:118-130`, field `lastStream`). Never
  duplicated. Outcomes: reply, `ErrTimeout`, `ErrUnreachable` (target down, "refused"),
  `ErrTooLarge` (`handle.go:48-75, 124-148`).
- Link state is a dense matrix `links [][]link` grown on every `AddNode` (`net.go:62-77`),
  so memory is quadratic in node count.
- Faults: `Partition`, `PartitionOneWay`, `Isolate`, `PartitionRegion`, `SetLinkLoss`,
  `Clog`/`Unclog` (hold and release in order), `HealNet` (`net.go:191-306`).
- Latency is two uniform ranges, intra-region and inter-region, chosen by comparing node
  regions (`net.go:88-97`). There is no other distribution.

Absent [V]: any connection handle, byte-level delivery, partial read or partial write,
receive window or send buffer, backpressure, half-close, reset as an event the peer
observes, per-connection identity (ordering state is per node pair). `PLAN.md:1023` and
`docs/knowledge/chunk-0b-simulator.md` ("Narrowed claims") say the same: short reads,
kernel buffers and connection resets are not modelled.

### Node lifecycle and disk [V]

`Factory func(e env.Env) env.Process` (`sim.go:92`), registered with
`AddNode(NodeSpec{ID, Region, Skew, DriftPPM}, f)` (`sim.go:303`). Crash bumps an
incarnation counter, closes the old handle, drops volatile state and rolls the disk back to
its durable layer; restart schedules a new `Start` event (`process.go:23-76`, `sim.go:339-346`).
Pause holds arriving events and releases them FIFO on resume (`process.go:82-105`,
`sim.go:787-799`). Every node gets a `Disk` whether it wants one or not (`sim.go:329`).

### How tests are written [V]

A scenario is `func(seed int64) *sim.Sim` that builds its own `sim.New`, runs it, and
returns the finished sim with any failure in `Sim.Err()`.

```go
func CheckDeterministic(seed int64, run func(seed int64) *sim.Sim) (*sim.Sim, error)   // determinism.go:25
func RequireDeterministic(t testing.TB, seed int64, run func(seed int64) *sim.Sim) *sim.Sim  // :41
func Seeds(t testing.TB) []int64                       // seeds.go:119
func Sweep(t *testing.T, fn func(t *testing.T, seed int64))   // seeds.go:151
func Report(t testing.TB, s *sim.Sim)                  // determinism.go:128
func (s *Sim) AddInvariant(name string, every int, fn func() error)   // sim.go:484
func (s *Sim) Verify() error                           // sim.go:525
func (s *Sim) At(t env.Time, name string, fn func())   // sim.go:456
func (s *Sim) Workload(start env.Time, count int, maxGap time.Duration, fn func(i int))  // sim.go:462
```

- Replay check: run twice, compare `Digests{Events, State}`, then `Verify`
  (`determinism.go:25-36`). `Events` is SHA-256 over every 38-byte record; `State` is
  SHA-256 over each node's id, region, flags, incarnation, `EncodeState()` and disk
  (`log.go:139-170`).
- Seed selection: `-seed`, else `DST_SEED`, else pinned seeds then `DST_SEED_BASE` +
  `DST_SEEDS` (default 3) (`seeds.go:119-148`). Failure output ends with a `PIN:` line and
  `REPLAY: go test <pkg> -run '<anchored>' -seed=<n> -count=1` (`determinism.go:94-98, 150-151`).
- Meta-tests (`meta_test.go`): `TestReplaySameProcess`, `TestReplayAcrossProcesses`
  (re-executes the test binary with `GOMAXPROCS=1 GOGC=1` and `GOMAXPROCS=4 GOGC=off`),
  `TestGoldenDigests`, `TestDifferentSeedsDiffer`, and three canaries (unseeded draw, map
  order, dropped invariant).
- `Verify` returns `ErrInvariantNotRun` if an invariant ran fewer times than scheduled
  (`sim.go:529-534`), which catches an invariant that was silently skipped.

### Shrinking [V]

There is no simulator-native shrinker. The string "shrink" does not occur in
`internal/sim/*.go`. Minimisation comes from two outside mechanisms:

- rapid's automatic shrinking in `prop_test.go` (external dependency).
- Go native fuzzing over `ScheduleFromBytes` (`fault.go:217`), which decodes 4 bytes per
  fault into a timed fault schedule, driven by `FuzzSimSchedule` (`fuzz_test.go:34-50`).

### Determinism linter [V]

`detlint` type-checks the named packages with `go/types` and reads the module path from the
target's `go.mod` (`detlint.go:38`), so it is not tied to object-store. Rules
(`README-tools.txt:25-60`, `detlint.go:206-212, 224-231`):

- `import`: non-test files may import only the root module and a fixed stdlib allowlist
  (`bufio bytes cmp container/* crypto/sha256 ... math/rand/v2 slices sort strconv strings time ...`).
  `os`, `sync`, `runtime`, `reflect`, `context`, `net` are not on it.
- `wallclock`: `time.Now/Since/Until/Sleep/After/AfterFunc/Tick/NewTimer/NewTicker`,
  `context.WithTimeout/WithDeadline`.
- `globalrand`: package-level `math/rand` and `math/rand/v2` functions other than constructors.
- `concurrency`: `go`, `select`, any channel type or operation, any use of `sync` or `sync/atomic`.
- `maprange`: ranging over a map; `maps.Keys/Values/All` not wrapped in `slices.Sorted`.
- `float`: any floating-point expression, variable or type parameter.
- `procstate`, `realio`, `ptrfmt`, `buildtag`, `cryptorand`.
- Per-function allowlist file with justification; unused entries fail
  (`agent_work/gates/detlint-allow.txt`, one entry: `runtime/debug` in `Sim.guard`).

---

## 3. Fit against mini-gateway's needs

| Need | Verdict | Evidence and what changes |
|---|---|---|
| Single-threaded seeded event loop | ADAPT, about 250 lines rewritten from a 799-line file | `Step`/`Run*`/stall errors/`guard`/invariants are kernel-shaped, but `exec` switches on ten domain event kinds and indexes nodes and links (`sim.go:672-757`), and `Sim` mixes loop state with nodes, links, code table and four named PRNGs (`sim.go:192-214`). The project's own extraction note says the loop "does NOT treat kind/node/peer as opaque integers". |
| Indexed heap, `(at, seq)` order | REUSE AS-IS, about 95 lines | `queue.go:86-178`. The `event` struct above it (`:59-82`) must be replaced. |
| Named PRNG streams | REUSE AS-IS, 19 lines | `stream.go`. New labels needed (per tenant, per client, backend). |
| Virtual clock, many cancelable timers | ADAPT, about 60 lines | Heap cancel is real O(log n) removal, which suits per-request deadline timers. The timer is bound to `*handle` and node incarnation, and time passes through per-node skew/drift (`clock.go:15-42, 74-113`). Drop skew/drift and incarnations. `Reset` allocates a new event each call [V]; whether that matters at 5,000 QPS with per-request timers is unmeasured [I]. The saturating helpers `later`/`scaled` (`clock.go:45-69`) copy as-is. |
| `env.Time`, `Clock`, `Timer` interfaces | REUSE AS-IS, about 37 lines | `env.go:23-57`. Useful only if cores keep a clock handle. If cores are pure "event in, actions out" with `now` passed in, only `Time` is needed. |
| Sans-I/O cores as event/action state machines | DOESN'T TRANSFER | `env.Process` is `OnDatagram`/`OnRequest` with callbacks (`env.go:103-115`). No actions-out convention exists. |
| Byte-stream connection (ordered, reliable until reset; partial reads/writes; backpressure; half-close; reset) | MISSING, build new | See "Absent" above. The request class delivers whole messages, has no connection object, and orders per node pair (`net.go:118-130`). This is the central piece of mini-gateway's simulated world. My estimate for a new `simconn`: 300 to 500 lines plus tests [I]. |
| Simulated backend (fake Postgres: latency distributions, connection limit, errors, stalls, resets, kill mid-transaction) | MISSING, build new | A `Process` could answer `OnRequest`, but only at whole-message granularity, which bypasses the wire codec. Latency is a uniform range only (`net.go:88-97`). No connection-count concept. |
| Simulated clients (many tenants, Zipf keys, disconnect/cancel mid-request) | MISSING, build new | The pattern "a client is a node driven by test actions" exists (`sagasim/cluster.go:259-316`). No tenants, no Zipf, no cancel. Modelling 500 to 1,000 client connections as nodes costs a quadratic link matrix (`net.go:71-77`) and node ids are packed into `uint16` in the log (`log.go:34-35`). |
| Open-loop arrival process | MISSING apart from a 19-line seed | `Workload` schedules `count` actions with uniform gaps in `[1, maxGap]` independent of responses (`sim.go:462-480`). No rate, no Poisson, no burst, no intended-send-time bookkeeping. |
| Fault injection | ADAPT the shape, rewrite the content | Timed fault events recorded in the log (`fault.go:149-213`) and a chaos/heal nemesis on its own stream (`nemesis.go`) are a good template. All 18 `FaultKind`s are node, partition or disk faults (`fault.go:14-43`). None is "stall the backend 150 ms", "reset this connection", "deliver 1 byte at a time". 659 lines, none reusable verbatim. |
| Seed replay | REUSE AS-IS, 156 lines, plus ADAPT 153 | `seeds.go` imports stdlib only and names nothing from object-store. `determinism.go` needs nine `*sim.Sim` methods and `sim.PanicError`; it copies with an import-path change if the new kernel keeps those method names. |
| Shrinking | MISSING under the module policy | Relies on rapid, which mini-gateway cannot import. The bytes-to-schedule fuzz pattern (`fault.go:217-250`, `fuzz_test.go:34-50`) works with stdlib `go test -fuzz` and is worth copying as a pattern. A ddmin-style shrinker would be new code. |
| Invariant checkers | ADAPT, about 60 lines | `AddInvariant`/`runInvariant`/`CheckInvariants`/`Verify` (`sim.go:482-536`) are generic already. Copy with the loop. |
| Double-run determinism check, golden digests, cross-process replay, canaries | ADAPT (pattern and test skeletons) | `meta_test.go` is written against `simtoy`; rewrite against a gateway toy. |
| Decision trace (accepted, coalesced, shed, timed-out) | MISSING, small | The log records simulator events only. `record` has one caller, `Step` (`sim.go:587`, `log.go:41`); a process cannot append a record. Final state can reach the `State` digest through `EncodeState`, but a per-decision trace cannot. The log also cannot be switched off (`sim.go:232-234`) and hashes every event with SHA-256. |
| Metrics capture, latency histograms | MISSING | No occurrence of histogram, percentile, Zipf or Poisson in `internal/sim`, `internal/env`, `cmd/objsim` (grep). |
| Scenario CLI | REUSE with rename, 104 lines | `cmd/objsim/main.go` depends only on `sim.Scenario`, `Digests`, `Verify`, `LogTail`, `PanicError`. |
| Determinism lint for cores | REUSE AS-IS by copy, 1,468 lines + tests | Module-agnostic. Two rules need a decision for mini-gateway: `float` (CoDel's control law uses a square root; AIMD, Zipf and histograms are commonly written with floats) and `concurrency` (correct for cores, must exclude the goroutine shell). The per-function allowlist handles exceptions. |
| Goroutine shell under `testing/synctest` and `-race` | DOESN'T TRANSFER from the simulator | Not in `internal/sim`. `internal/noderuntime` shows the pattern of posting everything to one loop and guarding stale timer fires with a generation counter (`realenv.go:24-56`), tested with synctest. It is RPC and file-system specific (`runtime.go` 801, `realenv.go` 353). Idea only. |
| 3-instance fleet scenarios | ADAPT [I] | A new kernel that hosts N actors covers it. Object-store's node registry would work but drags incarnations, regions and disks. |

Hard-coded object-store concepts in `internal/sim` and `internal/env` [V]: `NodeID` and
`Region` in the env bundle and in latency selection; `AddCluster` assigning regions
round-robin (`sim.go:288-298`); incarnations on every event; a `Disk` per node; `CodeTable`
in `Config`; `Digests.State` hashing region and disk; `uint16` node packing in the log;
`PanicError.Node env.NodeID`. No gossip, erasure or Merkle concept leaks in.

---

## 4. Coupling, freeze status, known defects

**Imports [V].** Non-test files in `internal/sim`, `simtest`, `simtoy` import only stdlib
and `internal/env` (13 files) and, for `simtest`/`simtoy`/`objsim`, `internal/sim`.
`internal/env` imports stdlib only. Nothing imports gossip, storage, saga, merkle, occ or
erasure. Copying the whole tree out is mechanically trivial: two packages, one import-path
rewrite. Since the freeze tag, the only change to these files is the module rename
(`git diff freeze-0b HEAD -- internal/sim internal/env`: 29 files, 50 insertions, 50
deletions, all import paths).

**Internal entanglement [V].** The difficulty is inside the package, not at its edge.
`docs/knowledge/sim-kernel-extraction-note.md` lists it: clock math is a method on the
domain `node`; `timer` holds the handle that is also Transport and Disk; `timer.Stop` calls
`Sim.cancel`, which adjusts hold counters on links and nodes (`handle.go:152-166`); the
`event` struct carries domain fields; `exec` owns clog/pause hold-and-release; the log is
not behind an interface; `Digests` mixes kernel and domain halves. I confirmed each of
these in the code. The note's own first steps for an extraction are: opaque event kind with
two integer actors, hold-and-release moved into the network, log behind an interface with a
no-op implementation, `Digests` split, clock math on a value type.

**Freeze [V].** `STATE.md:47`: "LANDED ceb5438, tag `freeze-0b` (API frozen)". `PLAN.md:921`:
exported API of `internal/env`, `internal/sim`, `simtest` changes only by a simulator change
request. `STATE.md:174-175` and the extraction note record the 2026-11-21 decision that
kernel extraction is "later, not now". The freeze binds object-store; a copy in
mini-gateway is free to diverge, and object-store must not be edited for mini-gateway's
sake.

**Recorded defects and limits [V, from the notes].**

- Adversarial pass (`agent_work/adversarial/0b-simulator/RESULTS.md`): run 1 held 52, broke
  8; final run held 58, broke 2. Of the final two, one is a claim narrowed in writing (which
  blob a nemesis disk fault hits depends on what the protocol wrote) and one is repaired but
  still scored broken by the unchanged harness (`clock-overflow`). Repairs covered overflow
  in read-fault counts, latency draws and timer durations, panics in fault callbacks, and
  the CLI failure report.
- Review found a major ordering bug under Pause plus Clog, fixed and covered by
  `TestPropStreamOrderPerConnection` and `TestReleaseKeepsFIFOAtTheReleaseInstant`
  (`build-notes.md:323`).
- Not modelled: real sockets, short reads, kernel buffers, connection resets, real file
  systems, the Go scheduler (`chunk-0b-simulator.md`, `PLAN.md:1023`, book chapter line 221).
- Requests are never duplicated; no runtime clock jump (`build-notes.md:290-299`).
- The event log cannot be disabled or replaced; fewer than 65,535 actors (extraction note).
- `go test ./internal/sim/... -rapid.checks=2000` fails in `simtest` because that package
  does not import rapid (`chunk-0b-simulator.md`, Pitfalls).
- Single-threaded cores cannot be preempted; an infinite loop in a core hangs the run
  (book chapter line 117).

---

## 5. Test run

Command, from `/root/Software/object-store`, with `GOFLAGS=-count=1` and `GOCACHE` under
`/tmp/mg-phase1` (removed afterwards):

```
go test ./internal/sim/... ./internal/env/... ./cmd/objsim/...
ok   .../internal/sim            4.829s
ok   .../internal/sim/simtest    0.019s
?    .../internal/sim/simtoy     [no test files]
ok   .../internal/env            0.011s
ok   .../cmd/objsim              0.061s
```

All pass. Wall time 32.7 s including a cold compile; test time under 5 s. One side effect
outside `/tmp`: the Go tool downloaded `pgregory.net/rapid v1.3.0` into the shared module
cache (`go: downloading pgregory.net/rapid v1.3.0`). No repo file changed.

Not run: the 500-seed sweep, `-race`, fuzzing, and the adversarial harness (its notes say
it rewrites tracked evidence files). Figures quoted from the project's notes, not
re-measured: about 230,000 events/s at 100 nodes; 500-seed package sweep about 93 to 100 s
(`build-notes.md:211-219`).

---

## 6. Recommendation: (b) reuse specific pieces and conventions, new kernel in mini-gateway

### Why not (a)

1. The world mini-gateway simulates is connections carrying bytes. Object-store's world is
   nodes exchanging whole messages, with disks. The first is absent and the second is most
   of the code: `net.go` + `handle.go` + `process.go` + `disk.go` + `fault.go` +
   `nemesis.go` + `rand.go` = 1,686 of 2,966 lines with no use in a proxy simulation, and a
   further ~580 lines of `sim.go`, `clock.go`, `log.go`, `queue.go` are node, link or
   incarnation logic.
2. Starting from a full copy means the first work is deletion and untangling inside a
   package whose own maintainers list the tangles, before any gateway feature exists.
3. Its tests (3,921 lines) assert the cluster semantics. They would be deleted along with
   the code they cover, so the "already attacked" assurance does not carry over to what
   remains except for the heap, stream derivation and seed plumbing.
4. The per-event SHA-256 log that cannot be turned off, and one event per message, are
   acceptable at 16k events per run. A 5,000 QPS scenario with byte fragmentation produces
   millions of events [I]; the gateway kernel should make the per-event log optional and
   hash an application decision trace instead.

### Why not (c)

The conventions were built and attacked, and a handful of files are clean. Rewriting seed
plumbing, stream derivation, the replay report and a 1,468-line type-checked lint from
scratch would spend effort for no gain.

### What to copy (vendoring by copy, with a recorded source SHA `379e622`)

| Copy | Lines | Treatment |
|---|---|---|
| `internal/sim/stream.go` | 19 | verbatim |
| `internal/sim/simtest/seeds.go` | 156 | verbatim |
| `internal/sim/queue.go:86-178` (heap) | ~95 | verbatim; new event struct |
| `internal/sim/clock.go:45-69` (`later`, `scaled`) | ~25 | verbatim |
| `internal/env/env.go:23-57` (`Time`, `Clock`, `Timer`) | ~37 | verbatim if cores keep a clock handle |
| `internal/sim/simtest/determinism.go` | 153 | import path only, if the new kernel keeps `Seed/Err/Steps/Now/Digests/Verify/LogTail/LogLen/WriteLog` and `PanicError` |
| `cmd/objsim/main.go` | 104 | rename only |
| `sim.go` error types, invariants, `guard`, `Step`/`Run*`, `At` | ~300 | edit: opaque event kinds, no nodes |
| `log.go` record ring and `WriteLog` | ~120 | edit: behind an interface, optional |
| `clock.go:74-113` timers | ~40 | edit: no handle, no drift |
| `agent_work/gates/detlint/` | 1,468 + tests | verbatim as a nested tool module; decide the `float` rule first |
| `meta_test.go` structure (same-process, cross-process, golden, canaries) | pattern | rewrite against a gateway toy |
| `ScheduleFromBytes` + `FuzzSimSchedule` | pattern | rewrite for gateway fault kinds |

### Surviving share

- Of `internal/sim` product code (2,966): about 140 lines verbatim (**about 5%**), about 550
  more as edited code (about 19%), about 2,270 not transferred (about 76%).
- Of all simulator product code (4,106): about 330 lines strictly verbatim (**about 8%**);
  about 590 (**about 14%**) counting files that change only by import path or name; about
  1,050 (about 26%) counting edited code.
- `detlint` is outside those totals and survives whole.

These are my line estimates from reading the files, not a measured diff [I].

### Must be built new in mini-gateway

Byte-stream connection model (chunking, flow control, half-close, reset, stalled reader),
fake Postgres backend, client/tenant load generators with open-loop arrivals and Zipf keys,
gateway fault kinds, application decision trace and its digest, histograms, a shrinker if
one is wanted without rapid, and the synctest shell tests. My rough size for the new kernel
is 600 to 900 lines, matching the earlier estimate in
`/root/briefs/shared-sim-investigation.md:15`; the world models on top are additional.

### What a later shared kernel would need as its common core

Consistent with the extraction note and the investigation, and with what both simulators
would then demonstrably share:

1. `Time` as int64 nanoseconds with saturating arithmetic and a horizon.
2. Named PRNG stream derivation from `(seed, label)`.
3. An indexed `(time, seq)` heap and a loop whose events carry an opaque kind and two
   integer actor ids, with cancel by handle. No node, link or incarnation knowledge.
4. Cancelable timers as a value type that takes any clock transform as a parameter
   (object-store passes skew/drift; mini-gateway passes none).
5. Step budget, typed stall errors, sticky first error, panic guard carrying the seed.
6. Invariant registration with the "not run as often as scheduled" check.
7. A trace sink interface: object-store's fixed-record event log is one implementation, a
   gateway decision trace another, and a no-op a third. Digest split into a kernel half and
   a world-supplied half.
8. Seed and replay plumbing (`-seed`, `DST_*`, pinned seeds, `Sweep`, run-twice check,
   `REPLAY:` line) written against a small interface instead of `*sim.Sim`.
9. `detlint` as the shared determinism gate.

Worlds stay separate on top: object-store's `simnet` (datagram and request/response, links,
partitions), `simdisk`, node lifecycle and nemesis; mini-gateway's `simconn`, fake backend
and load generators. If mini-gateway keeps the copied files' names and semantics unchanged
(items 1, 2, 3, 8), the later extraction is a move, not a redesign.

### Open points for the owner

- `float` rule: object-store bans floats in decisions and uses parts-per-million. CoDel's
  control law, AIMD and histogram bucketing need either integer formulations or allowlisted
  functions. This is a protocol decision, not mine to settle.
- Whether cores keep an injected `Clock` handle (object-store style) or take `now` and
  return actions including "arm timer" (the spec's stated style). It decides whether
  `Clock`/`Timer` are copied at all.
- Nick's 2026-11-21 preference was a shared repo that projects import
  (`/root/briefs/shared-sim-ruling-note.md`). No such repo exists, object-store ruled
  "later, not now", and mini-gateway's stdlib-only policy means copying. Recommendation (b)
  is compatible with a later extraction but does not itself satisfy that preference.
