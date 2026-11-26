# Chunk 6 — Walking skeleton: builder notes

Builder: Opus (`claude-opus-5-5`), 2026-11-26, branch `work/c06-walking-skeleton`. Two helper
agents worked inside this build under the builder's direction and are part of it: one wrote
`internal/shell`, `internal/config` and `cmd/mini-gateway` against the engine API the
builder fixed first; one closed the items carried from chunks 3 and 4. The builder
integrated, fixed and committed their work.

No SPLIT-REQUEST: every deliverable of the brief is built and green (see "Size" for the line
counts, which pass the brief's 5,500 mark).

## Simulator change requests

None. The frozen fake has no fault that answers a reset wrongly, so the reset-reply faults
of scenario 4b are injected by a test-only relay placed in front of the fake
(`test/e2e/tamper.go`), not by changing `internal/sim`. The fake also has no per-backend
settings accessor or crossing counter; scenario 4b computes both from the fake's
statement log (see "Deviations").

## What was built

| Package | What it is |
| --- | --- |
| `internal/proxy` | The engine (D1): one sans-I/O state machine — events in, actions out, `now` as an argument, determinism-linted. Client and database life cycles, the stage seams with pass-through implementations, the transaction lease, the reset after every lease, relay with back pressure, close on doubt, the "reached the database?" fact, the error-contract replies, timers from the timeout budget, `Snapshot()`, invariants, contract rows for the generated documents. |
| `internal/proxy/proxysim` | The engine driven by the simulator on simnet: the same read protocol as the shell (exactly 8 bytes first, exactly 1 for a TLS database leg, one unprocessed read at a time), simulated TLS on both legs, quiesce/invariant/state hooks, `World` (sim + simnet + fakepg + harness), `Finish()`. |
| `internal/shell` | Goroutine shell for real sockets: listener, per-connection reader and writer goroutines, one bounded mailbox, the engine goroutine, TLS on both legs with the exact-read rule, deadlines on every socket operation, admin listener (`/healthz`, `/stats`, `/metrics`) from the last published snapshot, CSV flush in real-process mode, clean teardown. |
| `internal/config` | JSON config file, defaults, fail-closed validation naming the key, fingerprint, `DeriveVerifier`; `certtest` generates certificates for tests. |
| `cmd/mini-gateway` | The binary: `-config`, `-check`, `-verifier`; SIGTERM/SIGINT a plain stop. |
| `internal/obs/enginesrc` | Mapping of `proxy.Snapshot` and `proxy.ShellSnapshot` to component `engine`; registers wire and classifier sources beside it; health rule `database`. |
| `test/e2e` | The scenario harness: `Spec` written once, run in tier S (`RunSim`) and tier R (`RunReal` against the binary and Postgres 17 with client TLS on); 21 tier S scenarios; 14 tier R tests; canaries and ablations; `FuzzEngineOps`; the reset tamper relay; `ProxyDriver` (chunk 2's `world.Driver` with the proxy inserted). |
| `test/analysis/e2e-*`, `tierr-*` | SQL assertions over the flushed files of every scenario (both tiers). |
| Docs | Generated `docs/timeout-budget.md`, `docs/capacity-table.md`; hand-written `docs/extension-seams.md`; regenerated `docs/csv-schema.md`. |
| Scripts | `scripts/chaos.sh` (`make chaos`, marker deleted), `scripts/fuzz-plant.sh`, `scripts/gen-docs.d/c06-contracts.sh`, `scripts/tier-r.sh` extended. |

## The engine contract (for the shell and for chunks 7–13)

`proxy.New(cfg) (*Engine, error)`; `Start(now, out)` opens the fixed set; `Step(now, ev,
out)`; `Shutdown(now, out)` (plain stop: closes every client and connection);
`Snapshot()`; `WireSnapshot()`, `ClassifierSnapshot()`, `ReplySnapshot()`,
`SyncSnapshot()`; `Invariants()`; `Quiesce()`.

Events: `EvClientAccepted`, `EvClientData`, `EvClientClosed`, `EvClientWrote{N}`,
`EvClientTLS{TLS}`, `EvDBOpened`, `EvDBDialFailed`, `EvDBData`, `EvDBClosed`,
`EvDBWrote{N}`, `EvDBWriteRefused`, `EvDBTLS{TLS}`, `EvTimer`, `EvPublish`, `EvDraining{On}`. Client ids come
from the driver, database ids from the engine. Events for an unknown or closed id are
ignored (counted for database data).

Actions: `ActClientWrite`, `ActClientClose{Reason}`, `ActClientStartTLS`,
`ActClientPause`, `ActClientResume`, `ActDBOpen{TLSFirst}`, `ActDBWrite`, `ActDBClose`,
`ActDBStartTLS`, `ActDBPause`, `ActDBResume`, `ActWake{At}` (one timer; `0` = none),
`ActPublish`.

Read protocol the driver must keep: read exactly `FirstClientRead` (8) bytes of a new
client, then nothing until `ActClientResume` or `ActClientStartTLS`; with `TLSFirst` read
exactly `FirstDBTLSRead` (1) byte of a database connection, then wait the same way; read at
most `MaxRead` bytes at a time and **never post a second read of a connection before the
engine has taken the first** (the shell: `conn.inflight`; the harness: one reader queue).
`StartTLS` runs only after every write queued before it.

Write reports the driver must keep (rule G8, repair round): every `EvDBWrote` of a
database connection is posted before its `EvDBClosed`; when a write stopped with bytes not
accepted (an error after fewer bytes than given, or queued writes dropped at the close), the
driver posts `EvDBWriteRefused` — "no byte at or past the acknowledged offset was accepted"
— before `EvDBClosed`. The shell's database reader waits for its writer to exit before it
posts the close; `proxysim` reports the bytes a failing write accepted and refuses the
unwritten rest at the close.

Deadline rule (repair round): time passes before the event. `Step(now, ev)` first fires
every timer due at or before `now`, then processes `ev`, so a reply, a byte or a freed
connection that arrives at the very instant of its deadline is late. One rule for every
engine deadline; stated in `docs/timeout-budget.md`.

## Seam contracts (`internal/proxy/seams.go`)

Called in the fixed order of PLAN.md 10.1; each refusal has its own row and counter
(`stage_refused{mode=<stage>}`, `replies{reason=<row>}`).

| Stage | Interface | Contract | Chunk 6 | Row when it refuses |
| --- | --- | --- | --- | --- |
| classify | `Classifier.Classify(tenant, text, inTx) work.Unit` | `text` without its NUL; `inTx` = client status `T`/`E` | `*pgwire.Classifier` | — |
| reject list | `unit.Class == work.ClassReject` | nothing of the message is forwarded | chunk 5 courtesy list | `unsupported-feature` 0A000 (and `unsupported-extended` for extended messages, discard until Sync) |
| draining | `EvDraining{On}` input | refuse new work from clients outside a transaction; close the client after the reply | reserved, unit-tested | `draining` 53M05 |
| breaker | `Breaker.Allow(now)`, `Observe(now, outcome)` | `Allow` false → refuse before the queue | `ClosedBreaker` | `breaker-open` 53M03 |
| rate gate | `RateGate.Allow(now, tenant)` | false → refuse | `OpenRate` | `rate-gate` 53M02 |
| coalesce | `Coalescer.Join(now, id, *unit) bool`, `Leave(now, id)` | true → the request waits on a flight, takes no queue slot | `NoCoalesce` | (chunk 11) |
| queue | `Queue.Push(now, tenant, id) bool`, `Pop(now)`, `Remove(id)`, `Len()`, `Cap()` | Push false → shed; the engine removes a request whose client left or whose wait expired | `FIFO` (one, tenant-keyed API) | `shed` 53M01 (full or `timeouts.queue_wait`) |
| lease | `Pool.Checkout(now, tenant) (id, ok)`, `Checkin(now, id)`, `Remove(id)`, `Idle()` | the engine owns each connection's protocol state; the pool only knows which ready ids may be lent | `FixedSet` | — |
| forward | — | Query written as its own message; statement deadline armed | — | rows of the loss table below |
| release | `ReleasePolicy.AtRelease(now, ReleaseInput) ReleaseAction` | reset / lend as is / close; input carries status, last tenant, unread bytes | `ResetAlwaysPolicy` (status `I` and nothing unread → reset, else close) | not a row (counted) |
| retry | `RetryPolicy.Retry(now, Failure) bool` | receives `Reached` (`work.Tri`), row, tenant, in-transaction | `RetryOff` | — |

A client inside a transaction (status `T`/`E`) skips draining…queue: its statements go to
the held connection (D4). The status byte alone ends a lease: `ReadyForQuery` `I` →
release; `T`/`E` → held.

## The reset (D8)

At release with status `I` and nothing unread, the engine writes `DISCARD ALL` as its own
`Query`, counts `resets_started`, arms `timeouts.reset` (1 s) and keeps the connection in
`resetting` (counted in use, not lendable). The reply is read by a dedicated framer
(`resetJudge`, at most 4,096 bytes): `ParameterStatus` and `NoticeResponse` are consumed;
exactly one `CommandComplete` whose tag is `DISCARD ALL`, then `ReadyForQuery` `I` →
`resets_completed`, the connection becomes idle. Anything else closes it and counts
`resets_failed{reason}`: `error` (ErrorResponse), `wrong-tag` (another tag, a second tag,
or `ReadyForQuery` before the tag), `wrong-status`, `deadline`, `connection-lost`,
`protocol-surprise` (any other message or a framing error), `closed` (the proxy closed it,
at shutdown). Nothing of the reply reaches a client. Bytes arriving on an idle connection
close it (`unsolicited-bytes`). Unread bytes (repair round): bytes in the same read after
the `ReadyForQuery` that ends a lease make the release input `Unread` (the policy closes
the connection, `release-dirty`, no reset is started); bytes in the same read after the
reset's own `ReadyForQuery` fail the reset as `protocol-surprise`. No leftover byte is
ever given to the reset judge or to the next lease. A connection closed at its reset deadline is gone: a late
reply cannot be read. `lease.reset` accepts only `always`; the no-reset arm is
`proxy.PlantNoReset`, reachable from tests only.

## Decisions the brief left open

1. **One timer.** The engine keeps its own deadline heap and asks for one wake-up
   (`ActWake{At}`), rather than one action per timer.
2. **IDs.** Client ids come from the driver (accept order in the shell, the simnet
   connection id in tier S); database ids from the engine.
3. **D5 back pressure.** The engine pauses a client from the moment a request starts until
   its reply has been handed to the writer *and written* (`out == 0`); bytes already read
   wait in a per-client buffer of at most `4 × MaxRead` (64 KiB; past it: FATAL 08P01).
4. **Relay framing.** Reply bytes are relayed as they arrive, except `ErrorResponse` and
   `ReadyForQuery`, which are held whole (bounded by the database max message + 5 bytes) so
   a FATAL from the database can be turned into the error contract instead of being
   relayed. A FATAL with nothing of the reply relayed → 08006/08007; after a part was
   relayed → the client is closed.
5. **Database pause threshold.** Reading the lent connection is paused when the client's
   unwritten bytes pass `limits.reply_buffer − MaxRead`, resumed at half the buffer; with
   one read in flight per connection the high-water stays ≤ `limits.reply_buffer`.
6. **Loss rows.** `lostRow`: part of the reply relayed → `partial-reply` (close); inside a
   transaction and nothing reached → `FATAL 08006` (`lost-in-transaction`); inside a
   transaction and reached → `08007`; outside, nothing reached or `SafeRead` → `08006`;
   outside, reached and not `SafeRead` → `08007` (COMMIT included). Between statements
   inside a transaction → `FATAL 08006`, client closed. "Reached" (rule G8, repair round):
   once a request's `ActDBWrite` has been handed to the driver the request may have
   reached the database (`work.Yes`); it is `No` only when the driver reported
   `EvDBWriteRefused` at an acknowledged offset at or before the request's first byte (the
   first write was refused with zero bytes accepted) and nothing of the reply was relayed
   (`reachedFor` in `lease.go`). A close that arrives before any write report is "may have
   reached".
7. **A failure with nothing reached outside a transaction** uses `08006` (lost-after-read):
   the true facts are "nothing was applied, safe to retry"; the row's "Reached: Yes" column
   is the general case. The engine's own `failures_reached{outcome=no}` counter records
   the truth, and the retry seam receives it.
8. **Queue wait** has a deadline in chunk 6 too (`timeouts.queue_wait`, 5 s, → 53M01), so
   no wait is unbounded before chunk 8's queue rule.
9. **Client idle inside a transaction** uses `timeouts.client_idle` (10 min) until chunk 7
   adds the 5 s idle-in-transaction rule; the held connection is then closed.
10. **Redial.** At most one dial at a time; after a failed connect the next dial waits
    `timeouts.redial_delay` (100 ms); after the loss of an established connection it dials
    at once.
11. **Health.** Rule `database` (enginesrc): degraded while no connection is idle, lent or
    resetting and the last connect failed. The shell adds `listener` (failed while the
    client listener is down) and the collector rule.
12. **Config format: JSON** (`encoding/json` with a token pass that refuses unknown keys,
    repeated keys and wrong-case keys with the full path), durations as Go duration
    strings, zero = default. Relative file paths resolve against the config file.
13. **Fingerprint**: first 16 hex characters of SHA-256 of the canonical JSON of the
    effective config with the password blanked.
14. **Mailbox**: one channel of 4,096 envelopes; readers and writers block when full; the
    engine goroutine never posts to itself (a refused `ActDBOpen` becomes a deferred
    `EvDBDialFailed`).
15. **Write queue per connection**: 64 MiB + 2 MiB and 65,536 items (closes the connection
    when passed; the engine's own bound keeps far below it).
16. **CSV run ids of the binary**: `<obs.csv.scenario>/<instance>/<start ns>` (new key
    `obs.csv.scenario`, default `real`), so `csv-assert.sh` root mode pairs them with
    `test/analysis/<scenario>/`.
17. **Plain stop**: the shell calls `Engine.Shutdown` before its final publish, so the final
    flush holds no request in flight; a reset cut by the stop is `resets_failed{closed}`.
18. **Tier S client auth** is `AuthTrustForTestsOnly` for the scripted clients (SCRAM on
    both legs is covered by tier R, the chunk 4 wire scenarios and `ProxyDriver`'s
    workload); tier R uses SCRAM on both legs.

## Fault × path matrix (first rows)

Expected row = the error-contract row the client gets; budget = the deadline that bounds
the client's wait; "reached" = the engine's fact for that failure.

| Fault | Where in the conversation | Expected row | Elapsed budget | Reached? | Test (tier S / tier R) |
| --- | --- | --- | --- | --- | --- |
| Database reset before the reply, statement a safe read | after the Query was read by the database | `lost-after-read` 08006, usable | immediate | Yes | `e2e-kill-before-read` / `TestTierRDatabaseKilled` (blocked read killed) |
| Same, statement a write | same | `lost-after-write` 08007, usable | immediate | Yes | `e2e-kill-before-write` / `TestTierRDatabaseKilled` |
| Database reset during the reply | after `RowDescription` was relayed | `partial-reply`: client closed, no end marker | immediate | Yes | `e2e-kill-during-reply` / `TestTierRDatabaseKilled` |
| Database reset between statements in a transaction | after `ReadyForQuery T` | `lost-in-transaction` FATAL 08006, client closed | immediate | Yes | `e2e-kill-mid-transaction` / `TestTierRDatabaseKilled` |
| Database reset right after the reply | during the reset or while idle | none (client has its reply); connection closed and re-opened | — | — | `e2e-kill-after-reply` |
| Database accepts and never answers startup | connect | queued request `shed` 53M01 | `timeouts.queue_wait` (connect deadline closes the dial first) | No | `e2e-silent-db-startup` / `TestTierRSilentDatabase` |
| Database accepts a query and never answers | statement | `lost-after-read` 08006 for a safe read (08007 otherwise) | `timeouts.statement` | Yes | `e2e-silent-db-query` / `TestTierRSilentDatabase` (pg_sleep → 08007) |
| Queue full | queue stage | `shed` 53M01, usable | immediate | No | `e2e-queue-full` / `TestTierRQueueFull` |
| Reject-list statement, extended-protocol message | reject stage | `unsupported-feature` / `unsupported-extended` 0A000, usable | immediate | No | `e2e-reject-list` / `TestTierRRejectListAndExtendedProtocol` |
| Client vanishes mid-statement or mid-transaction | lease | none (no client); the database connection is closed | — | Yes | `e2e-client-vanish` / `TestTierRClientVanishes` |
| Client stops reading a large reply | relay | none; client dropped, connection closed | `timeouts.client_write` | Yes | `e2e-slow-reader` / `TestTierRSlowReader` |
| Reset reply wrong (error, tag, status, silence, drop, late) | release | none; connection closed, never lent | `timeouts.reset` for silence and late | — | `e2e-reset-*` |
| Draining | draining stage | `draining` 53M05, closed when idle | immediate | No | `TestDrainingRefusesNewWorkWith53M05AndClosesWhenIdle` (unit) |
| Plaintext startup under `require` | startup | `tls-required` FATAL 28000 | immediate | No | `TestPlaintextStartupUnderRequireGets28000` / `TestTierRTLS` |
| Client cap | accept | `too-many-clients` FATAL 53300 | immediate | No | `TestClientCapRefusesWith53300` |

## Config reference

`internal/config/testdata/example.json` is the example; every key, its default and its
validation are in `internal/config` (`decode.go`, `validate.go`); `TestValidationFailsClosedNamingTheKey`
has 62 failing cases.

| Key | Default | Validation (fails closed, names the key) |
| --- | --- | --- |
| `listen`, `admin_listen` | — | host:port, different from each other |
| `instance` | `gw-1` | ≤ 64 characters of letters, digits, `.`, `_`, `-` |
| `database.address`, `.name`, `.user` | — | required; address host:port with a numeric port |
| `database.password` / `.password_file` | — | exactly one |
| `client_tls.mode` | `off` | `off`, `allow`, `require`; not `off` → `cert_file` and `key_file` must load as a pair |
| `backend_tls.mode` | `disable` | `disable`, `require`, `verify-full`; `verify-full` → readable PEM `ca_file`, and a host name (`server_name` or the address host) |
| `tenants[]` | — | at least one; unique users; verifiers parse; one iteration count and one salt length across the table (chunk 4 carried item) |
| `pool.connections` | 4 | 1–4,096; ≤ `fleet.backend_connections` / `fleet.instances` |
| `queue.capacity` | 256 | 1–1,048,576; ≤ `limits.max_clients` |
| `limits.max_clients` / `.max_startup` / `.reply_buffer` | 1,024 / 128 / 262,144 | `max_startup` ≤ `max_clients`; `reply_buffer` 32 KiB–64 MiB |
| `timeouts.*` | PLAN.md 9.2 values (`docs/timeout-budget.md`) | each > 0 and ≤ 24 h; the nesting table holds |
| `lease.reset` | `always` | only `always` |
| `obs.publish_interval`, `.detail`, `.csv.dir`, `.csv.max_bytes`, `.csv.scenario` | 1 s, false, off, 0 (default cap), `real` | interval > 0; max_bytes ≥ 0 |
| `fleet.instances`, `.backend_connections`, `.connect_rate_per_s`, `.backend_max_connections` | 1, pool × instances, 10, 100 | per-instance values ≥ 1, no remainder, budget + 3 reserved ≤ the database limit |

## Claims and the tests that prove them

| # | Claim | Tests |
| --- | --- | --- |
| 1 | Results through the proxy equal direct, under any fragmentation | `internal/proxy`: `TestFragmentedQueryOutlivesTheMidMessageDeadline` (repair); `test/e2e`: `e2e-results`, `e2e-fragment` (one-byte chunks both sides) via `TestScenarioE2E` + `checkResults` (compares with a direct run of the same spec and seed); `TestResultsThroughTheProxyEqualDirectOnEveryPinnedSeed`; `TestScenarioChunk2BaselineThroughTheProxy` (chunk 2's `direct-mixed-transactions` behind `world.Driver`); tier R `TestTierRResultsThroughTheProxyEqualDirect`, `TestTierRFragmentationChangesNothing` (client writes byte by byte) |
| 2 | No statement runs inside another client's transaction; release only at `I` | `e2e-tx-integrity` (`TxCrossings` over the fake's statement log); canary `TestCanaryReleaseOnAnyReadyBreaksTransactionIntegrity` (132 crossings); tier R `TestTierRTransactionIntegrity` (markers per transaction); invariant "each lent connection has exactly one client" |
| 3 | Lent again only after a completed reset; every other ending closes | `internal/proxy`: `TestBytesAfterReadyForQueryEndingALeaseCloseTheConnection`, `TestBytesAfterTheResetReplyCloseTheConnection`, `TestResetReplyAtItsDeadlineIsLate` (repair); `e2e-session-state` (`resets completed = leases ended`), the six `e2e-reset-*` tamper scenarios (`checkTamper`: one failure of the right reason, the backend runs nothing after it), `e2e-client-vanish`, `e2e-kill-*`; canary `TestCanaryLendBeforeResetReplyIsCaught`; `resets.sql` in every scenario |
| 3b | Session state never crosses clients | `e2e-session-state` (`SessionCrossings` = 0: statement-log replay + observers' SHOW); ablation `TestAblationWithoutResetSessionStateCrosses` (138 crossings); tier R `TestTierRSessionStateNeverCrossesClients` (hidden SET, temp table, advisory lock, PREPARE, LISTEN in functions; `test/oracles/dirty-check.sql` after every lease: 0 dirty) and ablation `TestTierRSessionAblationWithoutResetCrosses` (32 dirty) |
| 4 | Every proxy-made refusal or failure is its row, with a correct end marker; never a short reply that looks complete | `TestInputOverflowAfterAPartialReplyClosesWithoutSplicingAnError` (repair); `checkKill`, `checkQueueFull`, `checkSilent*`, `checkRejects`; `rows.sql` (rows = codes the clients saw); `TestTierRDatabaseKilled`; `FuzzEngineOps` (every reply ends with exactly one end marker; tags match the statement) |
| 5 | "Reached: No" only when no byte was accepted | `reachedFor` (repair: No only on an explicit zero-byte refusal); `internal/proxy`: `TestDatabaseClosedBeforeTheWriteReportIsMayHaveReached`, `TestWriteRefusedWithZeroBytesIsNotReached`, `TestWriteRefusedAfterPartBytesIsMayHaveReached`, `TestClientVanishedBeforeTheWriteReportIsMayHaveReached`; `proxysim`: `TestWriteRefusedByAResetSocketIsReportedBeforeTheClose`; shell: `TestDatabaseCloseIsPostedAfterTheWriterReports`, `TestDatabaseWriteRefusedWithZeroBytesIsReportedBeforeTheClose`; `checkKill` (reached Yes for kills after the Query was read); `failures_reached` exported |
| 6 | Bounded memory | `e2e-slow-reader`, `TestTierRSlowReader` (high-water ≤ cap; before the shell's read credit it reached 1.4 MB against a 64 KiB cap — found and fixed); shell `TestReaderPostsNoSecondReadBeforeTheEngineTakesTheFirst` (repair: the read credit, fast); invariant "relay high-water ≤ cap"; shell `TestMailboxNeverExceedsItsCap`, `TestWriteQueueOverflowClosesTheConnection`; `docs/capacity-table.md` |
| 7 | Every wait has a deadline; nesting holds; silent peers end at the deadline | `internal/proxy` (repair, deadline at equality): `TestResetReplyAtItsDeadlineIsLate`, `TestResetReplyJustBeforeItsDeadlineCompletes`, `TestStatementReplyAtItsDeadlineIsLate`, `TestMessageRestAtTheMidMessageDeadlineIsLate`, `TestConnectionFreedAtTheQueueDeadlineDoesNotServeTheExpiredRequest`; shell `TestEngineCloseCancelsADialInProgress`; `e2e-silent-db-startup`, `e2e-silent-db-query` (elapsed checked against the budget), `e2e-reset-silent`, `e2e-reset-late`; `TestTierRSilentDatabase`; `TestConfigValidationNamesTheKey`, `TestDefaultTimeoutsNest`, config `TestValidationFailsClosedNamingTheKey`; shell `TestClientStalledMidHandshakeIsClosedAtTheStartupDeadline`, `TestStuckWriterIsBoundedByTheWriteDeadline`; `docs/timeout-budget.md` |
| 8 | No goroutine leak; admin answers while the data path is blocked | shell `TestCloseShutsTheEngineDownBeforeTheFinalFlush` (repair: plain stop before the final flush), `TestTeardownLeavesNoGoroutine` (8 teardown paths, `synctest`), `TestAdminAnswersWhileTheEngineIsBlocked`; `-race` (`internal/shell`, `internal/config` added to the race list) |
| 9 | TLS: nothing buffered across the upgrade; `require` never serves plaintext; `verify-full` never connects unverified | shell `TestExactlyEightBytesAreReadBeforeTheDecision`, `TestSmuggledPlaintextAfterTheTLSRequestClosesTheConnection`, `TestClientTLSLeg`, `TestDatabaseTLSModes`; `TestPlaintextStartupUnderRequireGets28000`; tier R `TestTierRTLS` (psql require / verify-full, disable refused with 28000, smuggled bytes, stall closed at the startup deadline, cancel after the upgrade recognised), `TestTierRDatabaseLegTLSModes` (`pg_stat_ssl` per mode; wrong CA → `failed-verification`, connect failure class `tls-failed`) |
| 10 | Every documented reject-list statement is refused end to end; extended clients refused cleanly | `e2e-reject-list` (fake ran only the SELECTs), `TestTierRRejectListAndExtendedProtocol` (pg_stat_statements moved by 2 for 15 refusals and one SELECT) |
| 11 | Flushed numbers equal the snapshot; tier S files byte-identical per seed; conservation from the files | `cmd/mini-gateway` `TestEveryMetricTheBinaryExportsIsInTheCatalog` (repair); `runScenario` (`Flush.Check` round trip + byte-identical files of two runs); `conservation.sql`, `completed.sql`, `rows.sql`, `resets.sql` per scenario; tier R `stopAndRead` (`csvflush.RoundTrip` + conservation from the file) and `tierr-*/*.sql` |
| 12 | Deterministic, sans-I/O; replay law | detlint over `internal/proxy`, `proxysim`, `enginesrc`; `runScenario` compares trace and state digests of two runs of every seed |
| 13 | Config validation fails closed (narrowed, see "Narrowed claims") | `TestValidationFailsClosedNamingTheKey`, `TestZeroCapMeansTheDefaultAndANegativeCapIsRefused` (repair), `TestNoConfigOrCommandFileMentionsThePlantSwitch` |
| 14 | The fuzz target: no seed from a hash, total and local decoding, finds the planted bug | `OpSeed` constant; `TestOpDecodingIsTotalBoundedAndLocal`; `TestFuzzEngineOpsCostIsBounded`; `scripts/fuzz-plant.sh` (found in 1–4 s, 5 of 5 runs); `TestShrunkQueuedVanishScenarioReplays` |

## Canaries and ablations (all test-only `proxy.Plant` bits)

| Switch | Must fail | Test |
| --- | --- | --- |
| `PlantReleaseOnAnyReady` | scenario 2 | `TestCanaryReleaseOnAnyReadyBreaksTransactionIntegrity` |
| `PlantLendBeforeResetReply` | scenario 4b (or an invariant) | `TestCanaryLendBeforeResetReplyIsCaught` |
| `PlantNoReset` (ablation) | scenario 4b, both tiers | `TestAblationWithoutResetSessionStateCrosses`, `TestTierRSessionAblationWithoutResetCrosses` (in-process shell, real Postgres) |
| `PlantQueuedVanishKeepsSlot` (fuzz ordering bug) | `FuzzEngineOps` within 20 s | `scripts/fuzz-plant.sh`, `TestShrunkQueuedVanishScenarioReplays` |
| `PlantSkipCourtesy` | — (lets session statements reach the fake in 4b) | used by `SessionSpec` |

## Operation fuzzing

`FuzzEngineOps` (`test/e2e/fuzz_test.go`, world in `opfuzz.go`). Seed source: the fixed
constant `OpSeed = 1`; the input is never hashed. Decoding: 4 bytes per operation (kind,
client, argument, gap in ms below 32), at most 48 operations, trailing bytes ignored — every
byte string is a valid bounded list and a one-byte change alters one operation. Operations:
connect, read, write, begin, commit, rollback, reject, extended, slow read, big read,
disconnect, vanish (reset), stop/resume reading, cancel, advance time, database fragment,
first-byte stall, database reset at a point, a wrong reset reply. World: 3 clients, 1
connection, queue 2, short deadlines. (Two connections first: random inputs reached the
planted bug in 1.25% of cases and the gate's 20 s run missed it in about half the tries;
with one connection 10.5% of random inputs reach a queued client, and
`scripts/fuzz-plant.sh` found the bug in 1–4 s in 5 of 5 runs.) After every event the engine's invariants hold
(registered with the simulator); every client reply is checked (exactly one end marker per
query, tags match the statement, no `DISCARD` tag ever reaches a client, nothing
unsolicited); after the operations the world quiesces. Rate on vm-1: about 200–400 per
second (gate run with two connections: 251/s; one connection on a loaded host: about
130/s) — above the 100/s floor. The seed corpus (11 entries plus one found input) runs as
plain tests. Fuzzing without the plant found one input that failed: after a database
reset between statements, the proxy correctly sent `FATAL 08006` and closed, and the fuzz
client reported the closed session as an undecodable reply. That was a harness defect (the
client now treats a proxy FATAL as a close); the input is kept in
`test/e2e/testdata/fuzz/FuzzEngineOps/` as a regression. No product bug was found.

The pinned inventory test in `agent_work/adversarial/cr01-shrink-fuzz-rules/adv_test.go`
(14 targets) now sees 15; `TestAdv_6_GateSeedSweepCounts` and
`TestAdv_7_InventoryAndHiddenHashImports` are expected to be the only red adversarial tests
until the orchestrator re-pins (addendum item 4).

## Observability

`proxy.Snapshot` → component `engine` (`enginesrc.Metrics`, 70+ metrics): clients
connected / in startup / in transaction (with peaks), by TLS version, accepted, refused,
closes by reason, cancel requests; requests received / accepted / completed / failed /
shed / rejected, queued and in flight (with peaks), replies by error-contract row, stage
passed and refused by stage, failures by reached, failures without reply, retry asked;
statement-time, queue-wait and reset-time histograms; queue capacity; relay buffer
high-water and capacity, relay pauses, clients dropped, ParameterStatus in turn; leases
started / ended / ended by outcome; resets started / completed / failed by reason,
connections resetting; database open / connecting / lent / idle (with peaks), capacity,
`database_down`, dials, dial failures, ready, closes by reason, connect failures by class;
timers pending; publishes; draining; events. Shell metrics under the same component
(`shell_*`): mailbox depth / peak / capacity / full, goroutines, readers and writers by
side, accepted, admin requests, handshakes failed, deadlines, accept refused / errors,
write-queue overflows, stale events. Conservation (`Snapshot.Check`, an invariant in every
simulator run): received = accepted + rejected; accepted = completed + failed + shed +
queued + in flight; replies by row + failures without reply = failed + shed + rejected +
non-request replies; reached facts = failures; resets started = completed + failed +
resetting; leases ended = by reset + by close + as is; resets started = leases ended by
reset; open + connecting ≤ cap; queue ≤ cap; relay high-water ≤ cap. Wire and classifier
snapshots are registered beside it. Tests: `TestMappingMovesEveryMetricAndExportsEveryField`,
`TestZeroSnapshotEmitsEveryMetricAndPlainStructs`, `TestDatabaseHealthRuleIsDegradedNotFailed`,
`TestStageCountersMoveAndConserve`, `TestSnapshotCheckCatchesBrokenConservation`, and
simrun's `TestEveryDeclaredMetricIsEmittedAndEveryEmittedOneDocumented` (now fed by the e2e
runs).

Health: `ok`; `degraded` while the database is unreachable (no usable connection and the
last dial failed) or the collector is failing; `failed` when the client listener is down.

Coverage row: clients, requests by stage and outcome, the queue, the fixed set, the reset,
the relay, the shell mailbox.

## Steady-state row

| What grows | What removes it | Gauge |
| --- | --- | --- |
| per-client state (map entry, input buffer ≤ 64 KiB) | client close | `clients_connected` |
| relay bytes per client (≤ `limits.reply_buffer`) | writes reported by the shell | `relay_buffer_high_water` |
| queue entries (≤ `queue.capacity`) | dispatch, shed, client gone | `requests_queued` |
| engine timers | fire or cancel | `timers_pending` |
| connections resetting | the reset's reply or its deadline | `connections_resetting` |

Quiesce after every scenario: zero clients, queue empty, no waiting request, every
connection idle (the fixed set stays open), then after `Shutdown` zero sockets in simnet
and zero backends in the fake. `timers_pending` is zero after `Shutdown`.

## Stated limits

1. **The reset's residue** (PLAN.md D8): the random seed, the existence of a custom setting
   name, extension and procedural-language memory, a replication-origin session survive
   `DISCARD ALL`. Chunk 7's re-seed closes the first.
2. **`SafeRead` decides 08006 against 08007** with chunk 5's stated limits: a view, a
   user-defined operator, a function shadowing an allow-listed name, or a table with a
   row-level security policy can hide a write behind a text that fits the safe shape; for
   such a statement the proxy may say 08006 (retry is safe) when a write happened. Databases
   with such objects should treat 08006 like 08007 until chunk 11's `coalesce: off` /
   `hinted` story covers them.
3. **A client that closes (FIN) while its request is queued or running** is noticed only
   when the proxy next reads that client (after its reply), as Postgres itself notices it;
   the statement completes and the connection is reset normally. A reset (RST) is noticed
   at once and the database connection is closed.
4. **The proxy's client-side TLS version gauge** counts TLS 1.3 for a completed simulated
   handshake whose version is unknown.
5. **Tier R round trip**: the binary's in-memory snapshot lives in another process; tier R
   checks the file's own consistency (`csvflush.RoundTrip` of its last flush, order and
   persistence rules) and conservation from the file, not equality with memory. The
   in-memory round trip is checked in every tier S run.
6. **Cancel requests** are recognised and counted only; nothing is cancelled (chunk 7).
7. **Idle inside a transaction** is bounded by `timeouts.client_idle` (10 min) until chunk 7.
8. **One dial at a time**, no breaker: with the database down every queued request waits
   for `timeouts.queue_wait` (chunk 7 adds the breaker's fast refusal).
9. **Close on doubt after `ReadyForQuery`** (repair round): an asynchronous message the
   database sends in the same read right after the `ReadyForQuery` that ends a lease or a
   reset (a `NoticeResponse`, `ParameterStatus` or `NotificationResponse`) closes the
   connection; in a later read on an idle connection it closes it as `unsolicited-bytes`.
10. **"Reached: No" needs the driver's word** (repair round): a request whose write was
   still queued when the connection closed is "may have reached" unless the driver posts
   `EvDBWriteRefused`; the shell and `proxysim` do so when they dropped or failed a write,
   so a false "Yes" can only come from a driver that does not report.

## Deviations

- **Scenario harness interface.** "Written once against the driver interface (from chunk
  2)": chunk 2's `world.Driver` takes a `workload.Config`, which has no real-socket
  implementation before chunk 6a's load tool. The e2e scenarios are written once as
  `e2e.Spec` (scripted clients) and run by `RunSim` and `RunReal`; chunk 2's interface is
  also implemented with the proxy inserted (`ProxyDriver`, tier S) and runs chunk 2's
  baseline.
- **Scenario 4b in tier S** reads "every lease starts on a backend whose settings equal the
  defaults" from the fake's statement log (replaying SET / RESET / DISCARD per backend, a
  lease boundary being a change of the client tag), because the frozen fake exposes no
  per-backend settings; observers' `SHOW` replies are checked too. The crossing count is
  computed by the Go check, not counted by the fake, so the flushed files assert the
  proxy-side half (resets completed = leases ended) and not "crossings = 0".
- **Reset-reply faults** come from a relay in front of the fake (`tamper.go`), not from the
  fake (no change request against the frozen simulator).
- **Tier R ablation** runs the shell in process (`startInProcess`) with `PlantNoReset`,
  against the same real Postgres, because the test-only switch must not be reachable from
  the binary's config.
- **Tier R scenario names** are `tierr-*` with their own SQL files: the binary has no
  workload component, so tier S's `completed.sql` and `rows.sql` (which join the workload)
  do not apply; conservation and lease/reset accounting are asserted on both tiers.
- **Size**: product Go about 5,900 lines (engine 2,517; simulator harness 614; shell 1,299;
  config 981 with `certtest`; binary 211; mapping 313) plus 2,941 lines of non-test e2e
  harness in `test/e2e` — over the brief's ~2,350 × 2. Complete, so no split.
- **`promtext` replacers** are built once (a chunk 3 hot spot: half of every simulated
  flush was spent building a `strings.Replacer` per label).

## One error table (chunk 4's FATAL helper absorbed)

Every FATAL the client session writes now names its row of chunk 5's error table
(`pgwire.AppendRowFatal(dst, row, message)`, code from the row, Postgres-matching text kept
where Postgres adds detail such as the user name). Three rows were appended for startup
refusals that had only a local code: `unknown-database` (3D000), `protocol-version`
(0A000; version 2 and older still get the old-format error, its close code taken from the
row) and `no-user` (28000). The bytes on the wire are unchanged (the golden replies of the
existing rows did not move; `TestReplyGoldenBytes` regenerated for the three new rows only),
and `docs/error-contract.md` lists them. `fatal.go` keeps only the encoders the fake and the
test proxy also use (`AppendErrorResponse`, `AppendFatal`, `AppendLegacyFatal`) and the
SQLSTATE constants the database side reads.

## Items carried from chunks 3 and 4

| Item | Change | Regression test |
| --- | --- | --- |
| 3.1 `Registry.Declare` all-or-nothing | stages onto a clone, commits on success (`metric.go`) | `TestDeclareIsAllOrNothing` |
| 3.2 zero-flush `Close` with failing `open` counts a flush error | `csvflush/writer.go` | `TestZeroFlushCloseCountsAFailedOpen` |
| 3.3 `selfView` shares no histogram slice | starts from `c.last.Clone()` (`self.go`) | `TestSelfViewDoesNotShareHistogramsWithTheLastSnapshot` |
| 3.x every source the binary registers is in the catalog | engine metrics declared in `catalog.Registry()`; e2e runs feed the simrun registry test; the binary's own path (`shellConfig` → `shell.New` → a real flush) is checked in the repair round | `cmd/mini-gateway` `TestEveryMetricTheBinaryExportsIsInTheCatalog`, `TestEveryDeclaredMetricIsEmittedAndEveryEmittedOneDocumented`, `TestMappingMovesEveryMetricAndExportsEveryField` |
| 4.1 salt longer than the mock cap refused | `NewTenantTable` | `TestNewTenantTableRefusesAnOversizedSalt` |
| 4.2 stale `TLSFailed[HandshakeViolation]` skip removed | `snapshot_wire_test.go` | `TestWireCountersMoveAndConserve` |
| 4.3 mixed iteration counts or salt lengths refused | `NewTenantTable` (and so config validation) | `TestNewTenantTableRefusesMixedShapes`, config cases |
| 4.4 key issuer from `crypto/rand`, one per instance | `cmd/mini-gateway` `serve` | `TestCryptoRandomVaries` |
| 4.5 one-raw-export rule mirrored both ways; every queried metric in `-- requires:` | `simrun/imports_test.go`; 57 wire SQL headers (8 metrics added) | `TestOneRawExportImportRule` |

Two existing tests built mixed tenant tables by accident and were aligned
(`TestMockChallengeTakesTheTableShapeByDefault`, `testTenants` in `client_script_test.go`).
For the prose seat: two existing comments still say "modal" iterations and salt length
(`internal/pgwire/client_tenants.go` above the table's shape fields, and
`client_mock_test.go` line 26); since 4.3 every tenant shares one shape, so "modal" can
become "the table's". The builder may not reword comments (the comment gate counts a
reworded comment as new).

## Commands

```
go test ./internal/proxy/... ./internal/shell/... ./internal/config/... ./cmd/mini-gateway ./internal/obs/enginesrc
go test ./test/e2e                                   # tier S scenarios (pinned seeds), canaries, fuzz corpus
DST_SEEDS=200 RUN_DIR=/tmp/s go test -run '^TestScenarioE2E$' ./test/e2e
go test -race ./internal/shell/... ./internal/config/...
go test ./test/e2e -run '^$' -fuzz '^FuzzEngineOps$' -fuzztime 30s
scripts/fuzz-plant.sh                                # the planted ordering bug is found
RUN_DIR=/tmp/r scripts/tier-r.sh                     # every tier R test, chunk 6 included (~5 min)
MG_TIER_R=1 go test -run '^TestTierR' ./test/e2e     # chunk 6 tier R alone (starts its own container)
make chaos                                           # tier S + tier R + SQL, one line per scenario
scripts/csv-assert.sh <run dir of one scenario> test/analysis/<scenario>/*.sql
scripts/gen-docs.sh --check
scripts/gate.sh c06-walking-skeleton
```

## Narrowed claims

- **Claim 13** (attack `TestAdv_13_ExplicitZeroCapacity`, orchestrator's ruling: the behaviour
  stays). Narrowed wording: "Config validation fails closed on every invalid setting it
  documents. A cap (`pool.connections`, `queue.capacity`, `limits.max_clients`,
  `limits.max_startup`, `limits.reply_buffer`) that is zero or omitted takes its default —
  zero is not an invalid setting; a negative cap, or one outside its documented range, is
  refused with an error naming the key." The generated `docs/capacity-table.md` says "zero
  means the default" for each of those caps; `TestZeroCapMeansTheDefaultAndANegativeCapIsRefused`
  holds both halves per key. Durations follow the same rule (decision 12: zero = default).

## Adversarial tests that assert against a ruling

- **`TestAdv_5_ZeroAcceptedBytes`** (claim 5) now fails, by design. It hands an `UPDATE` to
  the driver (`ActDBWrite` emitted), posts `EvDBClosed` with no write report at all, and
  asserts `reached_no = 1`. That is exactly review 1 finding 3's reproduction
  (`TestRepro_DBClosedBeforeDBWroteClaimsReachedNoOnWrite`), and the orchestrator's ruling
  for this round says the opposite: once a write is handed to the driver the request "may
  have reached" unless the driver reports zero bytes accepted. In the shell a close can
  overtake the writer's report, so silence is not evidence of zero bytes. The engine now
  claims No only on the driver's explicit `EvDBWriteRefused`
  (`TestWriteRefusedWithZeroBytesIsNotReached` is the corrected form of the attack). The
  attack's two siblings, `TestAdv_5_FirstByteAccepted` and `TestAdv_5_ResponseBeforeWriteAck`,
  still hold.
- **`TestAdv_13_ExplicitZeroCapacity`** stays red: it asserts the literal "caps are
  positive" against an explicit zero, which the ruling keeps as "zero means the default"
  (see "Narrowed claims").

## Repair round

Repair builder: Opus (`claude-opus-5-5`), 2026-11-26, one round on review 1 (FAIL) and the
adversarial pass (43 held, 3 broken). Every finding was reproduced first: each regression
test below was run and seen to fail before its fix (the shell tests also against the
review's mutations M1 and M2).

| Finding / attack | Outcome | Commit | Regression test(s) |
| --- | --- | --- | --- |
| R1-1 BLOCKER mid-message timer left armed | fixed: cancelled whenever the cursor is at a message boundary, whatever the phase; the timer acts only while a message is incomplete | `1f12441` | `TestFragmentedQueryOutlivesTheMidMessageDeadline` |
| R1-2 BLOCKER bytes after `ReadyForQuery 'I'` not unread | fixed: lease end with leftover bytes → `Unread` → closed `release-dirty`, no reset | `06ef106` | `TestBytesAfterReadyForQueryEndingALeaseCloseTheConnection` |
| `TestAdv_3_ResetWithTrailingReply` (same family) | fixed: leftover bytes after the reset's `ReadyForQuery` fail the reset (`protocol-surprise`), connection closed, never lent | `06ef106` | `TestBytesAfterTheResetReplyCloseTheConnection`; the attack now holds |
| `TestAdv_3_ResetDeadlineTie` | fixed: one rule for every deadline — `Step` fires timers due at or before the event's time first; stated here and in `docs/timeout-budget.md`'s source | `f7f59f3` | `TestResetReplyAtItsDeadlineIsLate`, `TestResetReplyJustBeforeItsDeadlineCompletes`, `TestStatementReplyAtItsDeadlineIsLate`, `TestMessageRestAtTheMidMessageDeadlineIsLate`, `TestConnectionFreedAtTheQueueDeadlineDoesNotServeTheExpiredRequest`; the attack now holds |
| R1-3 MAJOR "reached: No" after a close overtook the write report | fixed at three levels: engine (`reachedFor`: may have reached unless `EvDBWriteRefused` at or before the request's first byte), shell (database reader posts the close only after the writer exits, `EvDBWriteRefused` first when a write stopped short or queued writes were dropped), `proxysim.flush` (bytes accepted with an error reported; unwritten bytes refused before the close) | `8077048` | engine: `TestDatabaseClosedBeforeTheWriteReportIsMayHaveReached`, `TestWriteRefusedWithZeroBytesIsNotReached`, `TestWriteRefusedAfterPartBytesIsMayHaveReached`, `TestClientVanishedBeforeTheWriteReportIsMayHaveReached`; shell: `TestDatabaseCloseIsPostedAfterTheWriterReports`, `TestDatabaseWriteRefusedWithZeroBytesIsReportedBeforeTheClose`; proxysim: `TestWriteRefusedByAResetSocketIsReportedBeforeTheClose` |
| R1-4 MAJOR shell fixes without regression tests | fixed (tests added; both fail under the review's mutations M1 and M2) | `7a66e55` | `TestReaderPostsNoSecondReadBeforeTheEngineTakesTheFirst`, `TestCloseShutsTheEngineDownBeforeTheFinalFlush` |
| R1-5 MAJOR no catalog test on the binary's registration path | fixed | `ab08320` | `TestEveryMetricTheBinaryExportsIsInTheCatalog` (fails when an engine metric is dropped from the catalog) |
| R1-6 MINOR importlint reverse rule missing `enginesrc` | fixed | `cdaedac` | `importlint`, `TestOneRawExportImportRule` |
| R1-7 MINOR safe-shape / RLS limit missing from the error contract's source | fixed (also words "Reached? no" as rule G8) | `d2cb674` | `TestClassifierWriteContractDocs` / `scripts/gen-docs.sh --check` |
| R1-8.1 MINOR FATAL 08P01 spliced after a partial reply | fixed: closed with nothing more written | `adc3f06` | `TestInputOverflowAfterAPartialReplyClosesWithoutSplicingAnError` |
| R1-8.2 MINOR `ActDBClose` during a dial waited for the connect deadline | fixed: `closeByEngine` cancels the dial (and still queues the close item) | `7d2b323` | `TestEngineCloseCancelsADialInProgress` |
| `TestAdv_13_ExplicitZeroCapacity` | narrowed (claim 13, see "Narrowed claims"); capacity document says "zero means the default" per cap | `269bcff` | `TestZeroCapMeansTheDefaultAndANegativeCapIsRefused`, `TestContractDocumentsAreGeneratedFromTheCode` |
| `TestAdv_5_ZeroAcceptedBytes` (was held) | disputed: now red by the R1-3 ruling (see "Adversarial tests that assert against a ruling") | `8077048` | `TestWriteRefusedWithZeroBytesIsNotReached` |

Scenario digests: no trace or state digest is pinned in the repository (the replay law
compares two runs of the same seed), so none needed re-recording. The deadline rule and the
write reports change the event order of some tier S runs (for example a timer due at a data
event's instant now fires inside that event's `Step`); every scenario still replays
bit-identically per seed. The fuzz corpus and the shrunk scenario file are unchanged and
pass.

## Gate result

`scripts/gate.sh c06-walking-skeleton` (build stage) at `6dcbb7a`, 2026-11-26, about 40
minutes: **overall fail, for one expected reason only.** Every check passes except G3 unit,
whose single failing test is `TestAdv_6_GateSeedSweepCounts` in
`agent_work/adversarial/cr01-shrink-fuzz-rules` (the pinned fuzz inventory, 14 targets; the
module now has 16 sim-level targets with `FuzzEngineOps`). The brief's addendum item 4
expects exactly this until the orchestrator reviews the new target and re-pins; builders may
not edit that directory. `TestAdv_7_InventoryAndHiddenHashImports` passed.

| Check | Result |
| --- | --- |
| G0 tools, gofmt, build, vet, tools-vet, tools-test, tidy | pass |
| G1 detlint, importlint, boundlint, paniccheck | pass |
| G2 comments | pass |
| G3 unit | fail: only `TestAdv_6_GateSeedSweepCounts` (expected, see above) |
| G3 race | pass |
| G4 markers, sweep (200 seeds) | pass (sweep 355 s) |
| G5 fuzz | pass: 16 targets, 0 seed sweeps; `FuzzEngineOps` 2,998 executions, 142/s, class fuzz |
| G6 tier R | pass (177 s; chunks 2, 4, 5 and 6) |
| G7 CSV assertions | pass (195 SQL files over the sweep and tier R runs, 0 failures) |
| G8 generated documents | pass |
| G9 site | pass |
| G10 chunk extra | pass: canaries and ablations, shrunk scenario, `fuzz-plant.sh` found the planted bug after 1 s, simrun SQL with DuckDB required |

An earlier full run (at `7dd4085`) differed in one row: G10 failed because the planted-bug
fuzz check missed its 20 s budget with the two-connection fuzz world; that is why the fuzz
world now has one connection (see "Operation fuzzing"). `result.json` is not committed
(the orchestrator runs the landing gate).
