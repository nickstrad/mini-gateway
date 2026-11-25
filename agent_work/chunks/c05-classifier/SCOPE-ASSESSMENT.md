# Chunk 5 (statement classifier) — scope assessment for Nick

Written by the Fable orchestrator on 2026-11-25, at Nick's request
(`/root/briefs/zgateway-c05-stop-scope.md`, relayed by Twin). Chunk 5 is **parked**: the
arbitration was stopped, nothing is being repaired, nothing lands until Nick rules.
The code is on branch `work/c05-classifier` (tip `d39ecfb`), unchanged by this assessment.

**Short answer.** Nick's worry is right for one half of the chunk and not for the other.
About a third of the chunk (the work vocabulary, the error contract, the reply builders —
about 660 of 2,150 lines) has nothing to do with SQL syntax and came through two reviews
with no finding. The other two thirds decide things from SQL text, and there the evidence
says the task as planned has no natural end: every review round found new ways to spell
something the classifier must catch. My recommendation is option (b): keep reading SQL text,
but arrange every decision so that a reading mistake costs performance, never correctness —
and get the correctness from a mechanism (reset the connection) instead of from the reading.

Facts marked "from memory" were not re-checked in this run.

---

## 1. What the classifier has to decide, and what each decision really needs

| # | Decision | Who uses it | What goes wrong on a mistake | Needs SQL syntax? |
| --- | --- | --- | --- | --- |
| 1 | Is this an extended-protocol message? | Chunk 6 (refuse with `0A000`) | — | **No.** It is the message's type byte. |
| 2 | When does the transaction end (when may the connection be lent to someone else)? | Pool, admission | A connection lent while still in another client's transaction | **No.** The database says so in every reply (the status byte, decision D4). The classifier's "may leave a transaction open" flag is advice only; nothing correct depends on it. |
| 3 | Does this statement change **session state** (settings, prepared statements, temp tables, `LISTEN`, session advisory locks, holdable cursors)? | Reject list (plan section 9.4) | One tenant's session state reaches the next client of that connection. **This is the dangerous one.** | As planned: yes — a deny-list over SQL text. A deny-list over an open language is the part with no end. A smaller mechanism exists (section 4, option b). |
| 4 | Is this statement **provably read-only**? | Error contract: after a lost connection, tell the client `08006` "safe to retry" instead of `08007` "a write may have been applied" | Telling a client a retry is safe when a write may have happened | Yes, but only as an allow-list (unknown → "may have written"), which fails safe. The proxy's own retry never uses it (it retries only when nothing reached the database). So its value is small. |
| 5 | Is this statement **shareable** (coalescable)? | Coalescer (chunk 11) | One client receives another statement's result: wrong data | Yes, as an allow-list. Or no syntax at all, if the client marks shareable statements itself (the plan's `hinted` mode already exists). |
| 6 | What to answer when refusing (codes, end markers) | Every chunk | — | **No.** A table and byte builders. |

So two of six decisions need no SQL at all, one (6) is a table, two (4, 5) are allow-lists
that can be made as narrow as we like, and one (3) is the real problem.

## 2. The real surface

The proxy speaks the simple-query protocol only, but that narrows the *messages*, not the
*language*: a simple `Query` message carries arbitrary SQL text, so all of PostgreSQL's
syntax can arrive. The surface has two layers:

- **The lexical layer** (where strings, comments and statements begin and end) is small and
  fully specified — a few dozen rules in Postgres's scanner: plain strings, `E'…'` strings,
  `U&'…'`, dollar quotes, bit and hex strings, quoted identifiers, nested comments, operator
  splitting, and string continuation across a newline. It is finite. It can be got right,
  but only by porting the scanner rule by rule and checking against a real server, not by
  writing a lexer from memory.
- **The grammar layer** (what a statement *does*) is the huge one: several hundred statement
  forms, and — worse for a deny-list — many ways to reach the same session state. Session
  settings alone can be changed by `SET` in a dozen forms, `RESET`, `set_config()`,
  `UPDATE pg_settings`, a `search_path` string that names `pg_temp`, and by any function or
  view that does one of these out of sight.

How the chunk's 2,152 product lines split (branch tip, after the repair round):

| Part | Lines | Depends on SQL syntax? | Review-2 findings |
| --- | --- | --- | --- |
| `internal/work` (the vocabulary the pool, queues and coalescer are written against) | 215 | No | none |
| Error table, generated `docs/error-contract.md`, reply builders, discard-until-Sync helper | 369 | No | none |
| Classifier counters | 74 | No | none |
| **Lexer** (`lex.go`) | 424 | Yes — lexical layer | 1 blocker |
| **Classifier** (`classify.go`) | 853 | Yes — grammar layer | 1 major (and a bypass that rides on the blocker) |
| **Rule tables** (16 reject reasons, about 150 allow-listed functions) and their generated document | 217 | Yes | — |

Tests: 2,827 lines plus a corpus of 562 statements; a differential test against Postgres 17.

## 3. Where the implementation stands, and what the findings say

It covers a lot: every form on the plan's reject list, the transaction-scoped forms that are
allowed, a function allow-list, 562 corpus statements, a generator that composes spellings,
and checks against a real Postgres 17 (374 forwarded statements leave session state alone,
163 "read-only" statements run inside `BEGIN READ ONLY`).

The trouble is the trend, not the coverage. Holes found so far, by round:

| Round | Found by | Holes in "what does this statement do" |
| --- | --- | --- |
| Build | the builder's own Postgres checks | 3 (`SET local.x`, `UPDATE pg_settings`, `SET LOCAL standard_conforming_strings`) |
| Review 1 + attack 1 | Neon, Astra | 6 (schema-qualified calls, attribute-notation calls, `search_path` naming `pg_temp` in a string, quoted `EXPLAIN` option, `'now'::timestamp`, operator splitting) |
| Review 2 | Neon | 2 (string continuation across a newline; temporal range and array types) |

Eleven holes in three rounds, and the count per round is not falling to zero. Each was a
real way to make the proxy share a non-shareable read or let session state through.

What the two open findings mean:

- **The review-2 blocker (string continuation)** is a local bug with a non-local lesson. It
  is one known rule of Postgres's scanner that the hand-written lexer missed. Fixing it is
  about 40 lines. But the lexer is the foundation of every decision: when it loses its place
  in a string, the classifier can be shown one statement while Postgres executes another.
  So **a lexer mistake is not a missed case, it defeats every rule at once.** The lexical
  layer is finite and can be closed (port the scanner's rule list, then differential-fuzz
  the statement count against a real server), but as built, correctness hangs on it.
- **The review-2 major (temporal types)** and most of the earlier holes are the grammar
  layer: a list of names that should have been longer. This is the open-ended part. There
  is no document to port; the list is "everything in PostgreSQL that can do X", and
  PostgreSQL keeps growing. This is evidence that a *deny-list read from syntax* scales
  badly — not that this builder did it badly.

The plan already admits the approach cannot be complete: "a function or view can hide
session changes" is a stated limit of section 9.4. So today a great deal of effort goes into
catching every *visible* spelling of something that can be hidden anyway.

## 4. Options

Sizes are product lines, rough.

### (a) Continue as planned: fix the two findings, arbitrate, land

- **Size:** about 100–150 lines now.
- **Risk:** the next reader finds hole twelve. Nothing bounds the tail, and the cost of a
  missed hole is the worst kind this proxy has (session state crossing tenants, or a shared
  read that should not have been shared). Each further round costs a build/review cycle.
- **What it keeps:** clients always get an explicit error for every visible session feature;
  the widest set of statements is coalescable without client changes.

### (b) Narrow the classifier's job: mistakes may cost speed, never correctness — **recommended**

Three changes; each replaces "read the SQL correctly" with something that cannot fail open.

1. **Session state: guarantee by reset, not by reading.** When a lease ends, the connection
   is reused as-is only if the lease is *known clean*; otherwise the proxy sends
   `DISCARD ALL` (Postgres's own "forget everything about this session") before the
   connection goes back to the pool, or closes it if that fails. "Known clean" is decided
   in the safe direction and needs no lexer: every statement's first keyword is on a short
   allow-list (`SELECT`, `INSERT`, `UPDATE`, `DELETE`, `MERGE`, `VALUES`, `WITH`, `TABLE`,
   `SHOW`, `EXPLAIN`, the transaction-control words), and the raw text contains none of a
   short list of tainting words as a plain substring (`set_config`, `advisory`, `pg_temp`,
   `temp`, `prepare`, `listen`, …). A substring match over-reports (a string containing the
   word "temp" costs one reset) and cannot be fooled by quoting tricks, because it does not
   parse. The reject list stays as a **best-effort courtesy** on the first keyword — `SET`,
   `PREPARE`, `LISTEN`, `COPY` and the rest still get `0A000` — but no guarantee rests on
   it. A config switch "reset after every lease" gives the strict version; its cost is
   measured in chunk 14 (the plan already lists `DISCARD ALL` on release as a measured
   switch, decision D8).
   What changes for a client: a session feature that slips past the courtesy check works
   for the rest of its transaction and is then gone, instead of being refused. That is how
   PgBouncer's transaction mode behaves (from memory: it does not parse SQL at all).
   What improves: session state hidden inside a function — today's stated limit — is now
   covered whenever the statement text shows a tainting word, and always in strict mode.
2. **Sharing (coalescing): a tiny positive recogniser, or the client's hint.** A statement
   is shareable automatically only if it fits a deliberately small shape that needs no real
   lexer: one statement beginning with `SELECT`, made only of letters, digits, spaces and
   `_ , . * = < > ( ) '`, with no backslash, dollar sign, double quote, comment, semicolon
   or line break, and every called name on the short allow-list. Inside that alphabet the
   string rules that broke the lexer cannot occur (no escapes, no continuation, no dollar
   quotes). Everything else is shareable only when the client marks it (`/* mg:share */`,
   the plan's `hinted` mode). The benchmark's hot-key reads (`SELECT v FROM kv WHERE k =
   '…'`) fit the shape.
3. **Read-only-provable = shareable.** No second proof. Any other statement that fails
   after forwarding is reported as "a write may have been applied" (`08007`).
4. The "may leave a transaction open" flag is dropped (decision 2 never needed it).

- **Size:** the 1,494 syntax lines shrink to roughly 400 (keyword scan, taint list, the
  small recogniser, tables). Add about 80–120 lines in chunks 6 and 7 for reset-on-release
  (one action, one timeout row, one counter, one config key). The 660 contract lines stay
  as they are. The 562-statement corpus is kept as a test, with expectations restated as
  "never unsafe" instead of "exactly classified".
- **Risk:** a reset round trip on leases that are not known clean (sub-millisecond on a
  local link; measured, and visible as a counter); fewer statements shared without a hint;
  the list of tainting words is still a list — but a miss now needs a session change hidden
  behind a function *and* a text that shows no tainting word, which is narrower than
  today's stated limit, and strict mode closes it entirely.
- **Needs Nick's ruling** because it changes two decided things: steering answer 9
  ("session-level features are rejected, not pinned" — they would still be rejected when
  visible, and reset when not) and what `auto` coalescing covers (decision D10 stays
  "provably a read", with a much smaller provable set).

### (c) A different mechanism for the same decisions

- **(c1) A real parser** (the PostgreSQL parser packaged for Go, `pg_query_go`): exact
  statement structure, about 300 lines of ours. But it is a C library through cgo and a
  second external dependency — against the "one named exception" of steering answer 9 — and
  a parse tree still cannot say whether a function has side effects (that is in the
  database's catalog, not the text). It fixes the lexical layer and not the grammar layer.
  Not recommended.
- **(c2) Let Postgres enforce read-only**: send a shareable read as `BEGIN READ ONLY; …;
  COMMIT` in one message, so the database itself refuses a write. Sound for decision 4, one
  round trip, about 150 lines (the two extra replies must be stripped). It does not help
  decision 3, and for decision 5 it does not catch reads whose result differs per call
  (`now()`, `random()`). Worth keeping as a later strengthening of (b), not as the fix.

### (d) Descope or defer parts

- **(d1) Land the uncontested third now** (`internal/work`, the error table, the reply
  builders, the extended-protocol refusal, the generated error contract) as "chunk 5a", so
  chunk 6 can be built; decide the rest separately. About 660 lines, already reviewed.
  This unblocks the chain whatever is chosen for the rest, and combines with (a) or (b).
- **(d2) Defer automatic sharing to chunk 11** and ship `hinted` only until then; defer
  "read-only-provable" altogether. This is (b) without the small recogniser: about 250
  syntax lines. Cost: the hot-key benchmark needs a hinted client.

### Recommendation

**(b), staged with (d1).** Reasons: (1) decision 3 is where a mistake hurts most, and it is
the one decision a deny-list cannot finish — a reset is a mechanism whose correctness does
not depend on reading SQL; (2) the two allow-list decisions keep their value at a tenth of
the reading; (3) it removes the lexer from the trust base, which is what the review-2
blocker showed to be the single point of failure; (4) it matches what the project says it
is — a pooler in the PgBouncer tradition — and makes its stated limit smaller, not larger.
If Nick prefers to keep the plan's promise of an explicit error for every visible session
feature, (a) is workable but should be given a fixed budget (one more round) and the reset
of (b) added anyway as the safety net.

## 5. What each option does to the chunks that depend on the classifier

| Dependent | (a) continue | (b) narrow (+ d1) | (c1) real parser | (d2) defer sharing |
| --- | --- | --- | --- | --- |
| **Chunk 6, walking skeleton** | Blocked until chunk 5 passes arbitration. No design change. | Unblocked as soon as 5a lands. Gains one action — reset on release when the lease is not known clean — with a timeout row and a counter (about 80 lines). Its "reject list end to end" scenario becomes "session state never crosses clients", checked exactly in the fake Postgres, which already tracks session settings. | Blocked until the dependency question is settled; build needs cgo in CI and the gates. | As (b). |
| **Chunk 7, pool** | None. | The dirty-connection table gets one more row: not known clean → `DISCARD ALL` → reuse, or close on failure (about 40 lines). The check-before-reuse and suspect rules from the *Release It!* note are unaffected. | None. | As (b). |
| **Chunk 11, coalescer** | None; widest automatic sharing. | Same interface (a share key and a flag). Fewer statements shared automatically; the rest need the hint. Both join modes and all scenarios unchanged; headline benchmark 2 fits the small shape. | None. | Starts hinted-only; the automatic mode becomes part of chunk 11's own scope. |
| **Chunk 12, retry** | None. | None (the retry acts only when nothing reached the database). | None. | None. |
| **Error contract (section 9.1)** | None. | The row "read-only statement, connection lost → `08006`, safe to retry" applies to fewer statements; the rest get `08007`. | None. | That row applies only to hinted statements. |
| **Schedule** | One more arbitration cycle at least; open-ended if more holes appear. | One Opus build for the narrowed classifier (smaller than the repair rounds so far), reviewed and attacked as usual; 5a can land at once. | A dependency ruling, then a rebuild. | As (b), slightly smaller. |

## What is waiting

Chunk 4 (wire sessions) is independent and continues. Chunk 6 and everything after it need
at least the contract third of chunk 5 (`internal/work` and the error table), so the build
chain stops after chunk 4 until Nick rules. Rulings needed: which option; and, if (b), the
two plan changes named under it.
