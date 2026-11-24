# *Release It!* on pooled connections that die silently (Nick's ruling 6, 2026-11-24)

Input to the chunk 7 (pool) brief. Written before the chunk is built, as PLAN.md "Chunk 7 — Pool, Step before the build" requires.

**Source.** `/root/briefs/release-it-book.pdf`, SHA-256 `d4818e64…8bd6cc` (checked). It is the **first edition**: Michael T. Nygard, *Release It! Design and Deploy Production-Ready Software*, Pragmatic Bookshelf, first printing April 2007, 350 pages. Page numbers below are the printed ones (they equal the PDF page index). Chapter numbers are first-edition numbers; they differ from the second edition that Nick's notes follow.

**Three things to know before reading on.**

- The first edition never mentions TCP keepalive or NAT. Its fix for the firewall story was an Oracle feature that happened to put a packet on the wire. Everything about keepalive, Postgres settings and Go below is marked **[ours]**: it is this note's translation, not the book. The facts used were checked in this run against the Postgres 17 documentation, the PgBouncer configuration page, the AWS documentation and `go doc` on Go 1.26; anything not checked says so.
- The book's advice is for an application that *uses* a vendor pool. Much of it turns into "what our pool must do" rather than "what to configure".
- Config key names below are suggestions. Every number is "initial".

---

## 1. What the book says

### The 5 a.m. problem (ch. 4 "Stability Antipatterns", 4.1 "Integration Points", pp. 50–56)

1. **Symptom.** Thirty application servers hung at about 5 a.m. every day; every request thread was blocked in a socket read or write inside the Oracle driver (p. 50). Packets left the application servers and got no reply, while the database was idle and healthy (p. 50); on the database side of the firewall there was no traffic at all (p. 53).
2. **A TCP connection is only memory at the two ends.** It "can exist for days without a single packet being sent by either side" (p. 53). Nothing on the wire says it is alive.
3. **The firewall forgets, and tells nobody.** It keeps a table of connections with a last-packet time and drops an entry that has been idle too long — one hour there. "There's no way for a third party to tell the endpoints that their connection is being torn down" (p. 54).
4. **What the next write looks like.** Not a reset and not an error. The stack sends, gets no ACK and retransmits, and the firewall drops every packet with no ICMP reply (p. 54). Linux 2.6 with `tcp_retries2` = 15 took about twenty minutes to report the connection broken; HP-UX thirty. A read is worse: "It could block forever" (p. 55).
5. **Why a quiet night kills the whole pool.** The pool lent last-in first-out. At night one connection did all the work and the other thirty-nine sat idle past the firewall's limit (p. 55). At the morning ramp every one of them swallowed a request thread, and the one good connection ended up held by a thread blocked on a bad one (p. 55).
6. **The obvious fixes hang too.** A validity query at checkout (`SELECT SYSDATE FROM DUAL`) blocks exactly as the real query would. Discarding connections older than the limit sends a session-teardown packet: "Hang." A reaper thread that replaces connections before they expire is called "really hairy" (p. 55).
7. **What worked.** Oracle's dead connection detection: the *server* pings the client periodically. The ping reset the firewall's timer, which was the point (pp. 55–56). So the book's cure is traffic on idle connections, not detection after the fact.
8. **Longevity testing.** Run load for days and leave the scripts idle for a few hours each day; that "will catch connection pool and firewall timeouts" (ch. 3 "Introducing Stability", sidebar, p. 38).
9. **Firewalls belong in test.** Hours of downtime came from firewalls that existed in production and not in QA (ch. 14 "Administration", p. 243).

### What TCP does and does not report (ch. 4, 4.1, pp. 48–50; ch. 5, 5.7 "Test Harness", pp. 137–139)

10. A refused connection fails in milliseconds. A full listen queue blocks the caller until the connect times out — minutes by default (p. 49). A peer that accepts and never answers blocks a read forever unless a timeout is set (p. 49).
11. Failures are fast or slow, and the slow ones cost capacity: "a slow response is a lot worse than no response" (p. 50).
12. Integration-point failures do not arrive through the protocol; you see "some kind of protocol violation, slow response, or outright hang" (p. 59).
13. The harness list of socket faults (p. 137): refused; stuck in the listen queue; SYN/ACK and then no data; nothing but resets; a full receive window never drained; established and never a byte; lost packets with retransmit delays; a peer that never acknowledges, "causing endless retransmits"; one byte every thirty seconds; megabytes where kilobytes were expected; all credentials refused. The harness should log what it received, in case the application dies silently (p. 139).

### Stale connections after a database failover (ch. 2 "The Exception That Grounded an Airline", pp. 31–34; ch. 11 "Networking", pp. 224–225)

14. After a failover moved the database's address, every pooled connection made before it was dead, and a write on one fails only "after the operating system and network driver finally decide that the TCP connection is dead" (p. 32). The driver still believed it was connected because it checked only its own flag (p. 33).
15. The dead connections alone did not ground the airline. Closing a statement on a dead connection threw, the code skipped the release, and after forty calls the pool was empty and every thread blocked at checkout (pp. 33–34). Dead connections turn error paths that never ran before into leaks.
16. A failover loses uncommitted transactions. Oracle's drivers re-run queries aborted by a failover, but "Updates, inserts, or stored procedure calls cannot be automatically repeated" (p. 224). The next packet may reach a different host, the error appears "in strange places", and a retry against the new node needs a circuit breaker around it (p. 225).

### Bad connections inside a pool (ch. 10 "Capacity Patterns", 10.1 "Pool Connections", pp. 206–207; ch. 4, 4.4 "Cascading Failures", p. 65)

17. A bad connection that is put back gets picked more often than its share, because good ones are out working: "One bad connection out of ten will cause more than 10% of requests to error out" (p. 206).
18. The opposite mistake: one system tore the connection down on every error and dialled again on every request, so a dead database got a connect attempt and two errors per page (p. 65).
19. Opening a database connection is expensive: "up to 250 milliseconds" (p. 176), 400–500 ms with authentication and session setup (p. 206).

### Waits at checkout, sizing, timeouts (ch. 3, p. 40; ch. 4, 4.4 p. 66 and 4.5 "Blocked Threads" pp. 83–87; ch. 5 "Stability Patterns", 5.1 "Use Timeouts" pp. 111–113, 5.2 "Circuit Breaker" p. 117, 5.5 "Fail Fast" pp. 131–132; ch. 9 "Capacity Antipatterns", 9.1 "Resource Pool Contention" pp. 176–179; ch. 16 "Phenomenal Cosmic Powers, Itty-Bitty Living Space", pp. 259–263; ch. 17 "Transparency", pp. 270, 297)

20. Most pools block the caller forever when empty. "Safe resource pools always limit the time a thread can wait to check out a resource" (p. 66; the same rule on pp. 40, 112, 178, 207), and the caller must know what to do when it gets nothing (pp. 178, 207).
21. Check out, query, read the result, check in: "At least three points in that interaction could hang indefinitely" (p. 112). Each needs its own limit.
22. A second case study: 3,000 threads on 100 servers idle, all waiting on a pool with no timeout behind a back end that had stopped answering (p. 259). It was fixed live by setting that pool's maximum to zero at run time and restarting only the pool (pp. 262–263).
23. Connections are lost for good by a deadlock in the database or by wrong exception handling (p. 87). Libraries that pool internally block forever and should be tested against a harness that ties up every connection (p. 86).
24. Sizing: with fewer connections than threads, throughput flattens at the pool size (pp. 176–178). The book says size the pool to the thread count (p. 179) and warns in the same breath that the database pays: 20 hosts × 5 instances × 50 connections is 5,000 connections (p. 177), and after a failover one node must hold all of them (p. 179).
25. An immediate retry after a timeout probably fails again, and making the caller wait through it may push the caller past its own timeout (p. 113).
26. A circuit breaker may count failure types separately — a lower threshold for timeouts than for refusals (p. 117). State changes are logged and exposed, and operators can trip or reset it by hand (p. 117). A system failure must be reported differently from an application failure, or bad input trips somebody's breaker (p. 132).
27. Fail fast: take the connections and check the breakers at the start of the work, not partway (p. 131). At start-up, open some pool connections; if none can be opened the application is in a failed state but keeps running so it can be inspected (ch. 14, p. 247).
28. Pool numbers to expose: in use, high-water mark, created, destroyed, checkouts, callers blocked now and in total, "number of times a connection was not available" (pp. 179, 270, 297). Database health: errors thrown, queries, average response time (p. 297).
29. A hand-written socket reader that does not drain its receive buffer stalls TCP and looks like a slow peer (ch. 4, 4.9 "Slow Responses", p. 100).

---

## 2. Already covered by PLAN.md

| Book point | Where the plan covers it |
| --- | --- |
| 10, 11, 21: every wait bounded | Section 9.2: connect 2 s, statement 30 s, cancel and clean-up 1 s, client rows; nesting checked at load. Section 10.3 "Integration points" question. |
| 20, 22: no unbounded wait at checkout | D3: there is no wait inside the pool; the one wait is the tenant queue, bounded by the queue rule, ending in `53M01`. Stronger than the book's "block for a limited time". |
| 4, 12, 13: silent and hostile peers as test inputs | Fault catalog 10.4: accepts and never answers (at startup, after a query), connect stalls, slow first byte, trickle, resets at every point, garbage, frozen, **half-open (peer gone, no reset seen)**. Chunk 2's fake Postgres keeps a log (13's last sentence). |
| 14, 16: database restart or failover | Chunk 7 test "database restart"; error-contract rows `08006`, `08007`, `FATAL 08006`; breaker with one trial; chunk 12 retries only pre-forward. |
| 15, 23: a dead connection must not become a leak | D7 destroy on doubt; quiesce invariant (in use = 0 after every scenario); 10.3 "Blocked threads" question; the dirty-connection table. |
| 17: a bad connection is not put back | D7: reuse only when known idle and clean; "closes by reason" in the pool snapshot. (Its siblings are a gap — G6.) |
| 18: no dial per request against a dead database | The single breaker with connect lockout, `53M03` at O(1), creation-rate cap, "recovery without a dogpile". |
| 19: connects are expensive | Creation-rate cap and concurrent-connect cap; cold-start scenario; headline benchmark 1. |
| 5, 6 (in outline) | Chunk 7 deliverables "idle timeout and maximum lifetime with jitter" and "check before reuse after long idle". Named, but with no values, no definition of the check and no deadline for it — G2, G3, G4. |
| 24: fleet multiplier | Section 8.1: the fleet connection budget plus a reserve must fit under `backend_max_connections`, checked at load. |
| 25: retry restraint | Chunk 12: default off, pre-forward only, budgeted, never holding a slot. |
| 26: breaker visible; system and application failures told apart | Breaker state and transitions in the snapshot; health `degraded` while open; SQL errors never count; one SQLSTATE per refusal. |
| 27: fail fast | Section 10.1 order: classify → reject list → draining → breaker → rate gate → queue → lease. |
| 28: pool numbers | Pool snapshot: open, idle, lent, connecting, creation rate, exhaustion events, closes by reason, lease-time histogram. |
| 29: slow reader | D5 bounded reply buffers; chunk 6 "slow reader" test. |

---

## 3. Gaps — changes to the chunk 7 brief

The plan has the right nouns (half-open fault, idle timeout, check before reuse). It lacks the mechanics the book's story turns on: the first write to a dead connection *succeeds*; the check and the close can hang like the request did; the cause hits every idle connection at once; and the cure is traffic on idle connections.

### G1 — Fault modes: split "half-open" into three, and add an idle-triggered one (section 10.4, chunk 2's virtual network)

"Half-open (peer gone, no reset seen)" is one phrase for behaviours a pool must tell apart. The scenario list needs, per database connection:

- **`blackhole`** (the book's firewall; also Azure Load Balancer's default, which silently drops idle flows [ours, Azure docs]). From time T, bytes written are *accepted and reported as written*, never delivered, and nothing arrives. No error for `retransmit_give_up` (fault parameter, default 15 min — longer than any scenario, as in the book). New connections to the same database work.
- **`peer_forgot`** (the book's failover; a restart with no FIN; an AWS Network Load Balancer after its idle limit, which answers late data with a reset [ours, AWS docs]). The first write succeeds; one network delay later the connection is reset. The error arrives *after* the bytes were handed over.
- **`idle_drop(D)`** (the middlebox itself). Any connection with no packet in either direction for D becomes `blackhole` (or `peer_forgot`, as a parameter). Keepalive probes count as packets when the connection has keepalive on (G2). This is the fault that shows the remedies working; the two above only show the damage.
- **Close on a dead connection.** On a `blackhole` connection the peer never answers a close either. Any step of the proxy's close path that waits for the peer therefore waits until its own deadline, and the scenario fails if that wait holds up a lease, a release or the dispatcher (G5).
- **Slots held on the server.** After `blackhole`, the fake Postgres keeps the old connection's slot for `server_reap_after` (parameter). Until then re-dials count against its `max_connections` and can be refused with `53300`.

### G2 — TCP keepalive on database connections, both directions (config, defaults, validation)

The book's fix was the server pinging the client. The Postgres equivalents [ours]:

| Key | Default | Meaning |
| --- | --- | --- |
| `backend.tcp_keepalive.idle` | 30 s | Proxy-side probe after this much silence. Go: `net.Dialer.KeepAliveConfig{Enable: true, Idle, Interval, Count}`. |
| `backend.tcp_keepalive.interval` | 10 s | |
| `backend.tcp_keepalive.count` | 3 | An idle connection whose peer is gone is reported dead after 30 + 3 × 10 = 60 s. |
| `backend.server_keepalive` | on, same three values | Sent as startup parameters `tcp_keepalives_idle`, `tcp_keepalives_interval`, `tcp_keepalives_count`, as part of the pool's fixed session settings. This is the direct analogue of Oracle's dead connection detection: Postgres probes the proxy, and it frees the server's slot when the *proxy* has vanished. |
| `backend.path_idle_limit` | 240 s | The operator's statement of the shortest idle timeout of anything between proxy and database. Default sits under the AWS NLB and NAT gateway limits (350 s, checked in the AWS docs) and at Azure Load Balancer's default (4 min, from memory, not re-read). The book's firewall was 1 hour. |

Validation at load, failing closed: `tcp_keepalive.idle ≤ path_idle_limit / 2`; `pool.idle_timeout < path_idle_limit` (G3). Setting keepalive off is allowed and logged as a warning.

Why not leave defaults: Go's default is 15 s idle, 15 s interval, 9 probes — 150 s to detect, and implicit. Postgres's server-side default is the operating system's, on Linux 7,200 s idle, which is longer than every middlebox limit above. To verify in tier R: the Postgres documentation places no "server configuration only" restriction on the three settings, so a startup parameter should work — confirm with `SHOW tcp_keepalives_idle` on a pooled connection.

What keepalive does not do: it runs only while nothing is unacknowledged. Once a statement has been written into a `blackhole`, the kernel is in retransmission, not keepalive, and the only bound is the statement deadline (G5, G9).

### G3 — Idle timeout and lifetime: values, and the lend order (config, defaults)

- `pool.idle_timeout` = 180 s, jitter ±10% per connection. Must be below `path_idle_limit`.
- `pool.max_lifetime` = 30 min, jitter ±10% (the plan already requires jitter). Enforced at release and by timer while idle, never mid-lease.
- **Lend the most recently used idle connection, and say so in the brief.** It is what lets the idle timeout shrink the pool, and it is exactly the order that left thirty-nine connections to rot in the book. That is safe here only because idle connections are covered three ways: keepalive (G2), the idle timeout, and the check (G4).
- Consequence to state in the brief: after a quiet night the pool is small or empty, so the morning ramp is a cold start. The existing cold-start scenario and the creation-rate cap are what cover the 5 a.m. ramp.

### G4 — Check before reuse: what it is, its deadline, what failure does (deliverable, timeout row, config)

- **When.** A connection is about to be lent and either (a) it has been idle for `pool.check_after_idle` (default 30 s — PgBouncer's `server_check_delay`) or longer, or (b) it carries the `suspect` flag (G6).
- **What.** One simple-protocol `Query` message with an empty string. The only acceptable reply is `EmptyQueryResponse` followed by `ReadyForQuery` with status `I`, with nothing else on the wire (an unsolicited `ParameterStatus` or notice marks it dirty per D8). This is PgBouncer's default check (`server_check_query = <empty>`); it takes no snapshot and runs no SQL.
- **Deadline.** `pool.check_timeout` = 250 ms, a new row in section 9.2: "Check before reuse — 250 ms — connection closed, reason `check_failed`". Validation: less than the statement deadline. The book's point 6 is the reason this row must exist: a check without a deadline is the hang.
- **Off the request's path (keeps D3).** The connection moves to a `checking` state: counted as open, not lendable. No client request is bound to it. The request that prompted the check stays in its tenant queue and takes whichever connection becomes lendable first — another idle one, this one once it passes, or a new dial. The queue rule keeps shedding as usual, so the check adds no hidden wait and needs no nesting under the queue wait.
- **On failure** (timeout, error, any unexpected byte): close without waiting (G5); count a connection-class failure toward the breaker; set `suspect` on every other idle connection (G6); the replacement dial goes through the creation-rate cap. **No client sees an error.**
- **Why this is the important one.** Whether a write into a half-open socket arrived cannot be known (G8). The check makes the first, unknowable write on a stale connection an empty query instead of a client's `UPDATE`.
- **Idle connections are also watched.** An idle connection that becomes readable — FIN, reset, a keepalive error, or any bytes such as `FATAL 57P01` — is closed at once and never lent (D7's "no unread bytes", made explicit for end-of-stream). This catches every death the kernel *is* told about; the check catches the rest.

### G5 — What the first write to a silently dead connection looks like, and what the client gets (fault × path matrix rows, counter)

The write returns success. Nothing comes back. Nothing errors. The proxy learns only when the **statement deadline** (30 s) expires; the cancel then goes out on a new connection and finds nothing to cancel, the 1 s clean-up wait expires, and the connection is closed. The connection was occupied for about 31 s. Rows to add to the matrix (fault `blackhole`, detected by reply timeout with zero reply bytes):

| Where | Client gets | Row of section 9.1 |
| --- | --- | --- |
| Statement classified read-only, outside a transaction | `ERROR 08006` after the statement deadline; client connection stays usable | "connection lost or reply timed out … read-only" |
| Statement that could write, or `COMMIT` | `ERROR 08007` after the statement deadline | "transaction resolution unknown" |
| Inside a transaction, mid-statement | The table has no row for this. Suggested: `FATAL 08006`, client connection closed, when the message holds no `COMMIT` or `END` (the transaction cannot have committed); `08007` when it does or the classifier cannot tell | Extend "lost while inside a transaction, between statements" |
| `peer_forgot` in the same three places | The same three rows, within one network delay instead of 30 s | — |
| Connection idle ≥ `check_after_idle`, or `suspect` | Nothing. The check absorbs it | — |

- Exposure that remains, to state in the book chapter: a connection that died less than `check_after_idle` ago, or during a lease, costs one client one statement deadline. The bound is one such error per dead connection, because G6 stops the second.
- **Closing never waits.** `Terminate` is written best-effort with no wait for the peer; the socket close does not linger; a TLS close-notify gets a short write deadline. A connection being closed stays counted in open + connecting until its descriptor is closed, so the cap holds.
- New counter: `backend_no_reply_total` — statements whose bytes were accepted and that had received zero reply bytes at the deadline. A healthy system shows zero; the 5 a.m. problem shows a burst equal to the pool size.

### G6 — One connection-class failure makes every idle sibling suspect (deliverable, test)

The causes in the book — a firewall table, a failover — kill all pooled connections together, and the trial dial of a half-open breaker *succeeds* because new connections work. Without a rule, the breaker closes over a pool still full of dead connections, and each costs a client 30 s.

- On any connection-class failure (`check_failed`, reset, `backend_no_reply`, idle read error — never a SQL error): set `suspect` on every idle connection and on every lent connection as it is released. A suspect connection must pass the check (G4) before its next lend, whatever its idle age. Passing clears the flag.
- Do not close them all at once (point 18, and the plan's own dogpile rule): suspects are checked as they are needed, replacements dial under the creation-rate cap.
- A reply timeout with zero bytes is counted on its own and sets `suspect`, but does not by itself count toward the breaker: a slow query on a healthy database must not open it (point 26 — the one use we make of "track failure types separately").

### G7 — Counters and snapshot additions (observability)

- Closes by reason gain: `check_failed`, `peer_closed_idle`, `idle_error` (keepalive or read error while idle), `no_reply`, alongside the planned `idle`, `lifetime`, `dirty`, `error`.
- `pool_checks_total` by result (`ok`, `timeout`, `error`, `unexpected`); gauge `checking`; gauge `suspect`.
- Histogram `idle_age_at_lend`. It answers "are we lending stale connections?" and is the number the idle timeout and check threshold are tuned from.
- `backend_no_reply_total` (G5). None of these carries a tenant label.
- Steady-state row for the brief: `suspect` flags and `checking` states are removed by a passed check or a close; the gauges return to zero in the quiesce check.

### G8 — "Nothing reached the database" cannot be claimed after a successful write (retry seam, sections 9.1 and 10.2 item 8)

A write that returns success says the kernel took the bytes, nothing more. A write that returns an error after taking some bytes says no more. So:

- The engine's "reached the database?" fact is **No** only when (a) no write was attempted for this request on any database connection, or (b) the first write returned an error with zero bytes accepted. Every other outcome is "sent, delivery unknown" and is reported as the plan's "Yes" rows. Suggest the 9.1 column heading say "may have reached".
- The plan's example of a certain No — "a lent connection found dead before the first byte was written" — happens only when the kernel already knew (FIN, reset, keepalive failure) or the check failed. For a `blackhole` it never happens: the connection looks perfect until the deadline. With G4 in place those detections occur before any request is bound, so they are not request failures at all.
- For chunk 12: the proxy's own retry stays limited to (a) and (b). It must never retry on `08006` after a reply timeout, even for a statement classified read-only; that retry belongs to the client, as the table already says. Unit test: a write error with n = 0 gives No; n > 0 gives "may have reached"; the fake's write log confirms both.

### G9 — Tests to add to chunk 7 (simulator unless noted; each with its ablation)

1. **Night trickle (the 5 a.m. scenario, book p. 38).** `idle_drop(1 h)`; one request every few seconds for two virtual hours; then full load. With keepalive, idle timeout and check on: zero client errors, `backend_no_reply_total` = 0, no request waits past the queue rule, dial rate ≤ the cap. Ablation — all three off: the ramp produces one statement-deadline hang per pooled connection and the invariant fails. Run it also with each remedy alone, so the note for the book chapter can say which one carried it.
2. **Silent mass death under load.** Every connection becomes `blackhole` at T; new dials work. Client errors ≤ connections lent at T, each the matrix row from G5; idle ones are caught by `suspect` checks with no client error; open + connecting ≤ cap throughout; goodput returns within statement deadline + clean-up; no dial spike. Ablation — `suspect` rule off: errors continue after the breaker closes.
3. **Failover (`peer_forgot` on all).** Same assertions, with errors arriving in one network delay.
4. **Polite idle close.** The database closes idle connections with a FIN (as `idle_session_timeout` or a restart does): zero client errors, closes counted as `peer_closed_idle`.
5. **The check hangs.** `blackhole` on a long-idle connection: the check ends at 250 ms, the queued request is served by another connection or shed by the queue rule on time, the close does not block, quiesce shows no timer or task left.
6. **Server slots still held.** After test 2, the fake keeps old slots for `server_reap_after` and refuses re-dials with `53300`: the proxy does not spin (dials ≤ the creation rate), and recovers when slots are reaped. With `backend.server_keepalive` modelled, the reap time is 60 s.
7. **Tier R.** Read the keepalive options back from a pooled socket with `getsockopt`; `SHOW tcp_keepalives_idle` returns the configured value; the empty-query check against Postgres 17 returns exactly `EmptyQueryResponse` + `ReadyForQuery(I)`. Optional, root only: a packet-drop rule on the database port to reproduce `blackhole` against a real kernel.
8. **Harness self-test** (chunk 2 rule): `blackhole` on connection A leaves connection B's behaviour unchanged; clearing `idle_drop` restores a never-faulted network.

### G10 — Optional: `TCP_USER_TIMEOUT` (config; declining is reasonable)

[ours; not in the book, which shows only the 20–30 minute kernel default.] On Linux, `backend.tcp_user_timeout` (suggest 10 s) makes a write into a `blackhole` fail after 10 s of unacknowledged data instead of at the 30 s statement deadline, and it is the only socket-level bound that works mid-statement. PgBouncer and Postgres both expose it. Costs: Linux only, set through the dialer's control hook, and one more fault knob in the simulator ("local error after T of unacknowledged data"). It does not change G8: an error from it still means "sent, delivery unknown".

---

## 4. Not adopted

| Book point | Why not |
| --- | --- |
| Validate every connection at checkout with a query (p. 55, the vendor pool's feature) | A round trip per transaction on the hot path. We check only after 30 s idle or on suspicion (G4), and always with a deadline. |
| A reaper that replaces connections just before the firewall limit (p. 55) | The book itself calls it hairy. Idle connections are closed, not renewed; keepalive keeps the ones we hold alive. |
| Size the pool to the number of request threads so nobody waits (pp. 176, 179) | The opposite of a transaction pooler: thousands of clients over tens of connections is the product. Contention is designed, bounded by the queue rule and shed. |
| Let the pool grow when exhausted (p. 40) | Breaks the hard cap, which is the pool's first invariant. |
| A blocking checkout with a timeout (pp. 40, 112, 178, 207) | Replaced by something stricter: no wait inside the pool at all (D3). |
| Re-run queries automatically after a failover; retry against the new node (pp. 224–225) | Only what certainly did not reach the database may be retried, by a default-off policy (chunk 12, G8). |
| Queue the work and retry it later (p. 113) | Decoupling middleware is rejected in section 10.1: it contradicts honest "may have been applied" errors. |
| Open connections at start-up and report a failed state if none opens (p. 247) | The pool fills on demand under the creation-rate cap. An unreachable database is `degraded`, not `failed`, so a balancer does not remove every proxy (section 10.3 "Cascading failures"). |
| Separate breaker thresholds per failure type (p. 117) | One breaker, one threshold. We take only the separation of zero-reply timeouts from connection failures (G6). |
| Operator trip and reset of the breaker; changing the pool maximum at run time (pp. 117, 262–263) | v1 has no control commands on the admin listener; the pool size changes by config and restart. Worth a follow-up: it is what saved the retailer in chapter 16. |
| Do not write your own connection pool (p. 83) | The pool is the project. The book's worry — unprovable concurrency — is answered by the reference-model property tests and the deterministic simulator. |
| Packet capture as the diagnostic (pp. 51–53); buy the firewall for QA (p. 243) | Operations practice. The simulator's `idle_drop` fault is our firewall in QA; `backend_no_reply_total` and `idle_age_at_lend` are meant to make the capture unnecessary. |
