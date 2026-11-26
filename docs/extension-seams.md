# Extension seams

Operational facts as of Chunk 6 (the walking skeleton). [Section 4 of the plan](../PLAN.md#4-steering-answer-1-does-a-generic-backend-interface-stay-honest-for-postgres) explains why `mini-gateway` is Postgres-first rather than hiding Postgres behind a generic backend interface; this page records where each seam lives in the code and what a second backend protocol would need to supply.

## The three seams

| Seam | Where | What crosses it | Held by |
| --- | --- | --- | --- |
| Work vocabulary | `internal/work` | `work.Unit` (tenant, class reject / start a lease / continue a lease, `SafeRead`, `Shareable`, share key), `work.Outcome`, `work.Refusal`, `work.Tri` | importlint: the cores (`internal/pool`, `internal/admission`, `internal/coalesce`, `internal/retry`, `internal/work`) import neither `internal/pgwire` nor `pgproto3` |
| Backend transport | `internal/proxy` events and actions (`api.go`) | a byte stream per database connection: `ActDBOpen`, `EvDBOpened`, `EvDBDialFailed`, `EvDBData`, `ActDBWrite`, `EvDBWrote`, `EvDBWriteRefused` (posted before `EvDBClosed` when a write stopped with bytes not accepted), `ActDBClose`, `EvDBClosed`, `ActDBPause`/`ActDBResume`, `ActDBStartTLS`/`EvDBTLS` | two implementations run the same scenarios: simnet in `internal/proxy/proxysim` (tier S) and TCP/TLS in `internal/shell` (tier R) |
| Wire boundary | `internal/pgwire` | bytes in, work units and outcomes out; replies built from the error table | importlint: only `internal/pgwire`, the fake Postgres and the test clients import `pgproto3` |

## Stage seams inside the engine (`internal/proxy/seams.go`)

Each is an interface with a pass-through implementation in chunk 6. The engine calls them in
the fixed order of PLAN.md 10.1: classify → reject list → draining → breaker → rate gate →
coalesce join → queue → lease → forward → release.

| Stage | Interface | Chunk 6 implementation | Replaced by |
| --- | --- | --- | --- |
| Classify | `Classifier.Classify(tenant, text, inTx) work.Unit` | `*pgwire.Classifier` | — |
| Reject list | the `work.ClassReject` class of the unit | courtesy list of chunk 5 | — |
| Draining | engine input `EvDraining` | refuses with `53M05` | chunk 13 sequences it |
| Breaker | `Breaker.Allow(now)`, `Breaker.Observe(now, outcome)` | `ClosedBreaker` (always closed) | chunk 7 |
| Rate gate | `RateGate.Allow(now, tenant)` | `OpenRate` (always allows) | chunk 10 |
| Coalesce join | `Coalescer.Join(now, id, unit)`, `Coalescer.Leave(now, id)` | `NoCoalesce` (never joins) | chunk 11 |
| Queue | `Queue.Push(now, tenant, id)`, `Pop(now)`, `Remove(id)`, `Len()`, `Cap()` | `FIFO` (one bounded queue, tenant-keyed API) | chunks 8, 9 |
| Lease | `Pool.Checkout(now, tenant)`, `Checkin(now, id)`, `Remove(id)`, `Idle()` | `FixedSet` (N connections opened at start, re-opened on loss, one dial at a time) | chunk 7 |
| Release | `ReleasePolicy.AtRelease(now, ReleaseInput) ReleaseAction` (reset / lend as is / close) | `ResetAlwaysPolicy` | chunk 7 adds `on-switch` and the re-seed |
| Retry | `RetryPolicy.Retry(now, Failure) bool` with the "reached the database?" fact | `RetryOff` | chunk 12 |

## What a second protocol would have to supply

The ten rows of PLAN.md section 4.1, answered as the chunk 6 engine needs them.

| # | Postgres fact | What the engine uses today | A second protocol must supply |
| --- | --- | --- | --- |
| 1 | The reply decides when a connection is free | the status byte of `ReadyForQuery` (`I`, `T`, `E`) from `pgwire.CycleTracker` | a reply-end event and a "still held" flag |
| 2 | The unit of admission is the transaction | `ClassStartLease` / `ClassContinueLease` | the same two classes |
| 3 | A reply is a stream of many messages | relayed while it arrives, message-aligned, bounded per client | message boundaries and a relay that never buffers a whole reply |
| 4 | Errors are in-band | proxy-made `ErrorResponse` plus `ReadyForQuery` from `pgwire.ReplyBuilder` | an encoder for its own error replies with a correct end marker |
| 5 | Failure after forwarding has no reply | `08006` / `08007` / `FATAL 08006` / close by the error table | its rows for "reached, outcome unknown" |
| 6 | Session state outlives the transaction | `DISCARD ALL` after every lease, judged by tag and status | a reset command and a way to judge its reply, or a statement that it has no session state |
| 7 | Sharing a connection is legal only for some requests | not done (batching dropped, D9) | — |
| 8 | Sharing a result needs to know the statement | `SafeRead`, `Shareable`, share key | its own recogniser or nothing |
| 9 | Connecting is a conversation | `pgwire.BackendSession` (TLS, SCRAM, startup) | a session machine with the same actions |
| 10 | Cancelling uses a second connection | recognised and counted only (mapping is chunk 7) | its cancel path |

## Engine code that is Postgres-shaped

| Code | Why it is Postgres-shaped |
| --- | --- |
| `internal/proxy/lease.go`: `relay`, `relayEvents`, `replyDone`, `lostRow` | follows `ReadyForQuery` status bytes, holds `ErrorResponse` and `ReadyForQuery` messages to decide FATAL handling, picks rows of the Postgres error table |
| `internal/proxy/lease.go`: `startReset`, `resetJudge` | `DISCARD ALL` and its `CommandComplete` tag |
| `internal/proxy/client.go`: `startupData`, `front` | `pgwire.ClientSession`, `FrontendTracker`, `SyncDiscard` |
| `internal/proxy/dbconn.go`: `backendActs` | `pgwire.BackendSession` |
| `internal/proxy/cursor.go` | the 5-byte typed message header |

Everything else in the engine (timers, the fixed set, the queue, back pressure, the client
and connection life cycles, the counters) uses only connection ids, byte counts and the
work vocabulary.
