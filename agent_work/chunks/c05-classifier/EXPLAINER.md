# Chunk 5 (statement classifier) — the situation explained

Written by the Fable orchestrator on 2026-11-25, at Nick's request
(`/root/briefs/zgateway-c05-explainer-request.md`, relayed by Twin). It sits beside
[SCOPE-ASSESSMENT.md](SCOPE-ASSESSMENT.md) and does not replace it: the assessment has the
line counts and the option table; this document explains what the classifier is for, why it
is hard, what each option really chooses, and what happens afterwards.

Nothing changed in this run except this file and one pointer line in `STATE.md`. Chunk 5 is
still parked on branch `work/c05-classifier` (tip `d39ecfb`). Chunk 4 has landed since the
request was written (`b342e48`), so the build chain is now stopped and waiting on this ruling.

Sources: `PLAN.md`, the steering answers, `REVIEW-2.md` on the chunk branch, and
`INFLUENCES.md` entries I1 (PgBouncer) and I27 (RDS Proxy). Statements marked "from memory"
were not checked in this run. One point in section 3 is new: a detail of option (b) that the
assessment left open.

**The ruling in one paragraph.** The plan promised three things about every statement and
made all three depend on reading its SQL text correctly. Three review rounds showed that the
reading cannot be finished. Option (a) keeps the promises as written and keeps paying for
the reading. Option (b) keeps the promises but moves the dangerous one (session state never
crosses tenants) onto a mechanism that does not read SQL, and shrinks the other two until
the reading they need is trivial. The price of (b) is that a client using a disguised
session feature is no longer refused; the feature works until the transaction ends and is
then wiped. I recommend (b), with the uncontested third of the chunk landed first (d1).

---

## 1. Context: where the classifier sits

### One simple query, end to end

Take a client that is already logged in and sends `SELECT v FROM kv WHERE k = 'a'`.

```
client socket
  │  bytes, in any fragments
  ▼
wire layer (chunk 4, landed)      frames the bytes into one Query message; knows the tenant
  │  message type + SQL text
  ▼
classifier (chunk 5, parked)      turns the text into a work unit
  │  work unit: forward or refuse, read-only?, shareable? + key, tenant
  ▼
gates (chunks 6, 7, 10)           reject list → draining → breaker → rate gate
  ▼
coalescer (chunk 11)              shareable: join an identical read already in flight, or lead one
  ▼
admission queue (chunks 8, 9)     wait for a connection; may be shed
  ▼
pool lease (chunk 7)              borrow one database connection
  ▼
database connection               forward the Query bytes; relay the reply
  ▼
reply tracker (chunk 4)           follows the reply to ReadyForQuery, reads its status byte
  ▼
lease end                         status I (idle) → the connection goes back to the pool
```

Before the classifier, the proxy knows only protocol facts: who the client is, which tenant,
the message type, and an opaque string. It has made no decision about the statement.

After the classifier, everything downstream works from the **work unit** (`internal/work`)
and never looks at SQL again. The pool, the queues and the coalescer are written against
that vocabulary and nothing else. The classifier is therefore the only place in the proxy
where SQL text is interpreted. That is good for the design, and it is also why every
mistake about SQL lands in this one chunk.

The reply path makes one decision the classifier does not: when the transaction ends. The
database states it in the status byte of every `ReadyForQuery` (decision D4), and the pool
acts on that byte, not on a guess from the text.

### Why a proxy needs these decisions

A proxy that gave each client its own database connection would need none of them. Each
decision exists because the proxy shares something.

| Decision | The sharing that requires it | Consumer | If wrong or absent |
| --- | --- | --- | --- |
| Does this statement change session state? | One database connection serves many clients in turn (transaction pooling). Settings, prepared statements, temporary tables, `LISTEN` and session advisory locks belong to the connection and outlive the transaction. | Reject list (plan 9.4), pool | The next client of that connection, possibly another tenant, runs with the first client's `search_path`, role settings or temporary tables. Nothing errors. This is the tenant-isolation failure. |
| Is this statement shareable? | One database read answers many callers (coalescing). | Coalescer | A caller gets a result that was not its own: a write runs once for several callers who each needed it, or `nextval`, `now()` or `random()` hands the same value to all. Wrong data, silently. |
| Is this statement provably read-only? | The proxy answers for the database when the connection is lost mid-statement. | Error contract (plan 9.1): `08006` "safe to retry" or `08007` "a write may have been applied" | The client is told a retry is safe, retries, and applies a write twice. |
| When does the transaction end? | The connection is lent per transaction. | Pool, admission | A connection is lent while another client's transaction is open. Not at risk here: the database reports it. |
| Is this an extended-protocol message? What do we say when refusing? | — | Every chunk | Protocol facts and a table. No SQL involved. |

The first three are the proxy's safety promises. The first is the worst to get wrong because
it is silent, crosses tenants, and persists for the life of the connection.

---

## 2. The root problem

### The tension

The simple-query protocol limits which *messages* a client may send. It does not limit the
*language*: a `Query` message carries arbitrary SQL, so all of PostgreSQL can arrive in it.
The plan stated the three promises as judgments about that text: this text does not change
session state, this text is a read, this text is safe to share. The proxy has only the
text. PostgreSQL has the parser, the catalog and the function bodies. So the proxy is
promising something about what the database will do, using less information than the
database uses to do it.

### Why that is hard in principle

The difficulty has three layers, and only the first can be closed.

1. **The lexical layer: where strings, comments and statements begin and end.** This is
   finite: a few dozen rules in PostgreSQL's scanner. But every other judgment stands on
   it. If the proxy's lexer and PostgreSQL's disagree about where one string ends, the
   proxy classifies one statement while PostgreSQL executes another, and every rule fails
   together. Review 2's blocker is exactly this: a string continued across a newline
   (`E'ok'` newline `'\' …`) made the lexer see a harmless `SELECT` where PostgreSQL ran
   `set_config('search_path', …)`, and the classifier marked it read-only and shareable.
   A lexer can be made exact by porting the scanner rule by rule and fuzzing against a real
   server. As built, it was written from knowledge of SQL, and correctness hangs on it.

2. **The grammar layer: what a statement does.** Here the reject list is a deny-list over
   an open language. "Changes session state" is not one statement; it is `SET` in a dozen
   forms, `RESET`, `set_config()`, `UPDATE pg_settings`, a `search_path` string naming
   `pg_temp`, and more. No document lists them all, and each PostgreSQL release adds
   statement forms, types and functions. A deny-list is complete only if its author knew
   every member, and a reviewer needs to find just one more. The same holds for "returns a
   different value per call": review 2's major finding was that `'now'` cast to `tstzrange`
   or `_timestamptz` behaves like `'now'::timestamptz`, which the list had.

3. **The catalog layer: what cannot be read from text at all.** `SELECT f()` changes
   session state if `f` does. So does a read from a view over such a function. The answer
   is in the database's catalog, not in the message. The plan already states this as a
   limit (section 9.4), which means the careful work in layer 2 defends against spellings
   an attacker has no need to use.

The review record illustrates this; it is not the argument. Eleven holes in three rounds
(3 found by the builder's own checks, 6 by review 1 and the first attack, 2 by review 2),
and the count per round is not falling to zero. Each one was a real way to share a read
that should not be shared or to let session state through. The builder did not do poor
work: the chunk covers every form on the plan's list, with 562 corpus statements and
differential tests against Postgres 17. The task has no last case.

### What the reference proxies do instead

Neither reference point makes a safety promise that depends on reading SQL correctly.

- **PgBouncer** in transaction mode does not interpret SQL. It lends the connection for the
  transaction and documents the list of session features that break (`INFLUENCES.md` I1:
  "breaks session state by design"). What it gives up: the client gets no error. A session
  `SET` silently stops applying, or silently applies to whoever gets the connection next.
  From memory: it can run a reset query (`DISCARD ALL`) when a connection is returned, off
  by default in transaction mode; and its pools are per database and user, so a leak stays
  inside one login. Mini-gateway shares one pool across tenants, so it cannot borrow that
  second protection.
- **RDS Proxy** detects session state and pins the client to its connection for the rest of
  the session (I27). What it gives up: the pooling itself. A pinned client holds a
  connection whether or not it is using it, and a workload that pins often gets little
  fan-in. From memory: its detection errs towards pinning when unsure. A detection mistake
  in that direction costs efficiency and never isolation.

The common principle: arrange things so that being wrong about the text is cheap.
PgBouncer does it by promising nothing. RDS Proxy does it by making the fallback safe.
Mini-gateway's plan (steering answer 9: "rejected, not pinned") chose a third position,
an explicit error for every session feature, and that is the only one of the three whose
safety needs the reading to be complete.

---

## 3. The actions available

Sizes are in the assessment. Here each option is stated as the choice it is.

### (a) Continue as planned

- **What Nick is choosing:** the promise as written. Every visible session feature gets
  `0A000`, the widest set of reads is shared with no client change, and safety continues to
  rest on the lexer and the lists being complete.
- **Cost now:** small. Fix two findings (100–150 lines), resume the arbitration, land.
- **Cost later:** unbounded. Each new hole is a build and review cycle, and holes found
  after release are tenant-isolation or wrong-data bugs.
- **Risk retired:** none. **Risk kept:** all three layers of section 2.
- **Revisits:** nothing. If the reset is added as a safety net, as the assessment advises
  even under (a), it revisits D8, which makes `DISCARD ALL` an optional switch.

### (b) Narrow the classifier — recommended

- **What Nick is choosing:** the same three promises, held up differently. Session safety
  comes from resetting the connection (`DISCARD ALL`) at lease end unless the lease is
  known clean. Sharing is automatic only for reads in a deliberately tiny shape, and
  otherwise needs the client's `/* mg:share */` hint. "Read-only" becomes the same set as
  "shareable". The reject list stays, as a courtesy on the first keyword.
- **What changes for a client:** a session feature that gets past the courtesy check is not
  refused. It works for the rest of that transaction and is then gone. Fewer reads are
  shared without a hint; anything with a double quote, a line break, a comment or a
  backslash is outside the shape, which includes most SQL that libraries generate. More
  failures after forwarding are reported as `08007` instead of `08006`.
- **Cost now:** one fresh Opus build of about 400 syntax lines replacing about 1,500, plus
  80–120 lines in chunks 6 and 7 for the reset. Plan edits to sections 9.1, 9.4, D8, D10
  and chunks 5, 6, 7 and 11.
- **Cost later:** a reset round trip on leases not known clean, measured in chunk 14; a
  hinted client wherever wide sharing is wanted.
- **Risk retired:** the lexer leaves the trust base, so layer 1 is gone. Layer 2 turns from
  a correctness risk into a performance cost: a missed spelling now means one reset more or
  one share fewer. In strict mode (reset after every lease) layer 3 is closed as well,
  which no amount of work under (a) can do.
- **Risk kept:** in the default mode the list of tainting words is still a list. A miss now
  requires a session change hidden inside a function *and* a text showing none of the
  words. That is narrower than the limit the plan already accepts.
- **Revisits:** steering answer 9 (session features are still rejected when visible, and
  reset instead of rejected when not; still never pinned); D10 (`auto` coalescing stays
  "provably a read" with a much smaller provable set); D8 (the reset becomes a default
  conditional action instead of an optional switch). The one-dependency rule is untouched.
- **One detail the assessment left open.** "Every statement's first keyword is on the
  allow-list" assumes the proxy can find where each statement in a message begins, and
  that is the lexer's job. Review 2's first reproduction hid `; SET search_path = …` behind
  a string trick, and `SET` is not among the tainting words listed. The fail-safe rule is
  simple and belongs in the new brief: a message with a semicolon anywhere except at its
  very end is not known clean. This does not change the recommendation. It is the kind of
  point the new build's review exists to catch, and under (b) each such point costs a
  reset, not a hole.

### (c1) A real parser (`pg_query_go`)

- **What Nick is choosing:** to buy an exact answer to layer 1 and a reliable statement
  tree for layer 2, with a C library through cgo.
- **Cost:** a second external dependency, cgo in every build, gate and CI job.
- **Risk retired:** layer 1. **Risk kept:** the lists of layer 2 are still ours to
  complete, and layer 3 is untouched: a parse tree does not say what a function does.
- **Revisits:** the one-dependency rule in steering answer 9 (pgproto3 is the single named
  exception, for the codec only). Not recommended.

### (c2) Let Postgres enforce read-only

- **What Nick is choosing:** to send a shareable read as `BEGIN READ ONLY; …; COMMIT`, so
  the database refuses a write itself.
- **Cost:** about 150 lines; two extra replies to strip from each wrapped read.
- **Risk retired:** the read-only decision becomes a fact the database enforces.
  **Risk kept:** session state, and reads whose result differs per call.
- **Revisits:** nothing. A possible later strengthening of (b), not a ruling for today.

### (d1) Land the uncontested third now, as "5a"

- **What Nick is choosing:** sequencing only. The work vocabulary, the error table, the
  reply builders and the extended-protocol refusal (about 660 lines, no finding in two
  reviews) land now so chunk 6 can start.
- **Cost:** one rebase and split of the parked branch. **Risk:** none added, none retired.
- **Revisits:** nothing decided; it splits chunk 5 in two. Combines with (a) or (b).

### (d2) Defer automatic sharing

- **What Nick is choosing:** (b) without the small recogniser. Sharing is hinted-only until
  chunk 11, and "read-only-provable" is dropped until then.
- **Cost now:** the least reading of any option (about 250 syntax lines). **Cost later:**
  headline benchmark 2 needs a hinted client, and chunk 11 grows by the automatic mode.
- **Revisits:** D10 (`auto` is postponed) and steering answer 9, as in (b).

---

## 4. What happens next

### (i) In the running system: three statements after classification

"Continue" is option (a) with the two findings fixed. "Narrow" is option (b) in its default
mode. The gates, the queue and the checkout are the same in both and are left out.

**An ordinary read:** `SELECT v FROM kv WHERE k = 'a'`

| Step | (a) continue | (b) narrow |
| --- | --- | --- |
| Classification result | Read-only, shareable; proved by lexing the statement and checking it against the lists | Read-only, shareable; it fits the small shape. Known clean |
| Coalescer | Joins or leads a flight keyed by the text | Same |
| Forward and reply | One lease, statement forwarded, reply relayed and copied to followers | Same |
| Lease end (status `I`) | Connection returned as it is | Known clean, so returned as it is. No reset |
| Connection lost mid-statement | `08006`, safe to retry | Same |

No difference at run time. Now write the same read the way a library would,
`SELECT "v" FROM "kv" WHERE "k" = 'a'`. Under (a) it is still shared automatically. Under
(b) it is outside the shape, so it is forwarded on its own lease unless it carries the
hint, and a lost connection gives `08007`. That is the visible cost of (b).

**A write:** `UPDATE kv SET v = 'x' WHERE k = 'a'`

| Step | (a) continue | (b) narrow |
| --- | --- | --- |
| Classification result | Not read-only, not shareable | Same. Known clean (`UPDATE` is an allowed first keyword, no tainting word) |
| Coalescer | Passed by | Same |
| Forward and reply | Own lease | Same |
| Lease end | Returned as it is | Returned as it is |
| Connection lost mid-statement | `08007` | Same |

No difference, with one exception: a write to a table named `temperature` contains the
substring `temp`. Under (b) that lease is not known clean and costs one `DISCARD ALL`. This
is the over-reporting the design accepts, and a counter makes it visible.

**A session-feature statement,** in three spellings.

| Spelling | (a) continue | (b) narrow |
| --- | --- | --- |
| Visible: `SET search_path = x` | `0A000`. Nothing reaches the database, no lease | Same: the courtesy check on the first keyword |
| Disguised: review 2's `SELECT E'ok'` newline `'\' \|\| ' \|\| set_config('search_path', 'h', false) --'` | As the branch stands: classified read-only and shareable, forwarded, shared with any caller sending the same text, and the connection goes back to the pool with the changed `search_path` for the next tenant. After the fix this spelling is refused; the next unknown one behaves the same way | First keyword `SELECT`, so it is forwarded. It contains a backslash, so it is not shared. It contains `set_config`, so the lease is not known clean. The setting holds until the transaction ends; then `DISCARD ALL` runs before the connection is returned, or the connection is closed if the reset fails. The next tenant gets a clean session |
| Hidden: `SELECT f()` where `f` changes a setting | Forwarded, not shared (`f` is not on the allow-list). The state leaks to the next client unless the setting is one PostgreSQL reports, which D8's watch catches | Default mode: the same leak, unless the text shows a tainting word. Strict mode: every lease is reset, so no leak |

I assume D8's watch on `ParameterStatus` stays under (b). The assessment does not say, and
the new brief should.

Summary of the runtime difference: for ordinary traffic nothing changes except how many
reads are shared without a hint. For hostile or unusual traffic, (a) fails by leaking and
(b) fails by resetting.

### (ii) In the project

| | (a) continue | (b) narrow, staged with (d1) |
| --- | --- | --- |
| **Unblocks** | Nothing until chunk 5 passes arbitration | Chunk 6 as soon as 5a lands |
| **Lands next** | The repair of two findings, then Muse's arbitration, a gate run by the orchestrator, the rebase onto main and the test moves listed in `STATE.md` | 5a (rebase, split, gate). Then, in parallel: plan edits and a new chunk 5 brief with a fresh Opus build, Neon review and Astra attack; and the chunk 6 build |
| **Chunk 6 inherits** | The plan as written. Its reject-list scenario tests "refused" | One new action, reset on release when not known clean, with a timeout row, a counter and a config key (about 80 lines). Its scenario becomes "session state never crosses clients", checked exactly in the fake Postgres, which already tracks session settings |
| **Chunk 7 inherits** | Nothing new | One more row in the dirty-connection table: not known clean → `DISCARD ALL` → reuse, or close on failure (about 40 lines) |
| **Chunk 11 inherits** | The widest automatic sharing, and with it the exposure to every classifier mistake: the coalescer turns a wrong "shareable" into wrong data for many callers | The same interface (a key and a flag). Fewer statements shared automatically; headline benchmark 2's hot-key read fits the shape |
| **Where the remaining risk sits** | In the classifier, open-ended, and of the worst kind. It grows as chunks 7 and 11 start to rely on it | In performance, where chunk 14 measures it: reset cost and the share rate. One bounded correctness item: default-mode leaks through a function whose call shows no tainting word, closed by strict mode |

Under (c1) nothing moves until the dependency question is ruled on, and the gates and CI
need cgo. Under (d2) the flow is as (b), with the automatic mode added to chunk 11's scope.

Chunks 8, 9, 10, 12 and 13 are the same under every option. Chunk 12's retry acts only
when nothing reached the database, so it never consults the classifier.

## What Nick is asked to rule

1. Which option. My recommendation is (b), staged with (d1).
2. If (b): accept the changes to decided things. Steering answer 9 becomes "rejected
   when visible, reset when not, never pinned". D10's `auto` mode covers the small shape
   only. D8's reset becomes a default conditional action.
3. If (a): a fixed budget of one more round, and whether the reset is added as a safety net.
4. In either case: whether 5a lands now. Nothing argues against it.
