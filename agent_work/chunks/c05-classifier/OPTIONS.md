# Chunk 5 (statement classifier) — options memo

Written by Fable on 2026-11-25 (mission `/root/briefs/zgateway-c05-options-mission.md`).
Analysis only. Chunk 5 stays **parked** by Nick's ruling of 2026-11-25; this memo changes no
code, no plan text and nothing on `work/c05-classifier`. Inputs: [CANVAS.md](CANVAS.md),
[SCOPE-ASSESSMENT.md](SCOPE-ASSESSMENT.md), [EXPLAINER.md](EXPLAINER.md).

**The bar.** Each option must name the mechanism by which session state cannot cross tenants
*without depending on how completely anything reads SQL*. An option that cannot do that fails,
and its residual hole is named.

**The five constraints**, numbered K1–K5 below: (K1) Postgres wire protocol in front of one
vanilla Postgres; (K2) transaction pooling; (K3) one shared database role; (K4) tenant =
startup user name; (K5) no external core dependency except pgproto3.

Evidence labels: **[canvas]** is a finding in CANVAS.md (its own labels apply). **[mine]** is
my reasoning in this memo: not run, not in the canvas.

## Summary

| # | Option | Bar | Constraints | Residual hole |
| --- | --- | --- | --- | --- |
| A | `DISCARD ALL` after every lease | Meets, with a fixed residue | All five | What `DISCARD ALL` leaves (four items, listed under A) |
| B | `DISCARD ALL` only when the connection changes tenant | Meets, same residue | All five | As A; state also crosses clients of the *same* tenant |
| C | Close the connection when it changes tenant (tenant-keyed connections) | **Meets fully** | All five; strains decision D6's shared pool | None across tenants |
| D | Canvas-refined (b)+(d1) with the conditional "known clean" reset | **Fails** | All five | A session change inside a function whose call text shows no listed word |
| E | Revoke privileges + trust the server's command tags | **Fails** | All five | Any function with a session effect that nobody revoked |
| F | A server that refuses (the read/write splitters' backstop) | **Does not exist** for session state | — | Vanilla Postgres has no mode that refuses session changes |
| G | Pinning, role per tenant, server extension, real parser | Varies | Each breaks one | See the table under G |

Only A, B and C take reading out of the trust base. Everything else either reads or leans
on a list.

## A. Reset after every lease

**Summary.** When a lease ends, the proxy sends `DISCARD ALL` before the connection can be
lent again. No statement is ever examined to decide this.

**Mechanism.** The reset is unconditional, so nothing a client writes can influence whether
it runs. The rules that make it hold:

- `DISCARD ALL` goes as its own `Query` message after `ReadyForQuery` with status `I`. It is
  refused inside a transaction block, and a multi-statement message is one [canvas].
- A connection is lendable only after the proxy has seen the `DISCARD ALL` tag and then
  status `I` in reply to its own message. Anything else (error, timeout, other status) closes
  the connection. This check reads the server's reply to a fixed statement the proxy wrote.
- A lease that ends in any state other than `I` (client gone mid-transaction) closes the
  connection, or gets `ROLLBACK` as a separate message first.
- Use `DISCARD ALL` itself. Every hand-picked reset list in the canvas leaves something out.

Odyssey ships this as its default and is the only reset design in the canvas with no
session leak found [canvas].

**Cost and effort.** About 80–120 product lines in chunks 6 and 7 (the assessment's number
for the reset action, timeout row, counter and config key). The session half of the
classifier is no longer needed for safety, so the "known clean" test is not built. Run-time
cost: one more database round trip per lease, after the client has its reply. Measured at
0.044 ms on a clean session over a Unix socket, against 0.056 ms for `SELECT 1` [canvas].
Not measured: a network link, where the round trip dominates and the connection is busy for
that long; and a session that actually has something to discard.

**What it gives up.**

- Pool throughput, by one round trip per lease. Chunk 14 would have to put a number on it.
- The plan's "rejected, not pinned" promise (steering answer 9) for disguised session
  features: they work until the transaction ends and then vanish without an error. A
  first-keyword refusal with `0A000` can stay as a courtesy; no safety rests on it.
- `DISCARD ALL` includes `DISCARD PLANS`. Functions that keep cached plans (plpgsql) would
  re-plan on every lease [mine]. The canvas timed only a clean session.
- Decision D8 changes from "optional switch" to "always".

**Verdict: meets the bar, with a residue that does not depend on reading.** The residue is
what `DISCARD ALL` does not reset [canvas]:

1. The random seed. The previous tenant chooses the next tenant's `random()` sequence.
   Closable: the proxy follows the reset with its own `setseed` [mine, untested].
2. The *existence* of a custom setting name (`SET myapp.x`). The value is cleared; the next
   tenant can tell the name was used. Not closable short of a new connection.
3. In-memory state of loaded libraries, extensions, PL/Perl and PL/Python [canvas, inferred
   from the missing hook]. Closable only by a deployment rule, or by a new connection.
4. A replication-origin session. Privileged roles only.

So "never" is true for the nine things `DISCARD ALL` covers and false for these four. All
five constraints respected.

## B. Reset only when the connection changes tenant

**Summary.** Each pooled connection remembers the tenant that last used it. `DISCARD ALL`
runs only before it is lent to a different tenant.

**Mechanism.** The tenant is a protocol fact (the startup user name), so the decision to
reset compares two names the proxy already holds. No SQL is read. Reset rules as in A.

**Cost and effort.** The reset action of A, plus a last-tenant tag per connection and a
pool rule that prefers a connection last used by the same tenant. That preference touches
the fairness and LIFO logic of chunks 7 to 9 [canvas: "touches the pool's fairness logic"].
The reset now sits on the *checkout* path for a tenant switch, so the incoming client waits
for it, unless the pool resets eagerly when it can see the next waiter. Saves the round
trip whenever consecutive leases belong to one tenant; saves nothing under evenly mixed
tenants.

**What it gives up.** Session state crosses clients of the same tenant, as in PgBouncer's
transaction mode: a disguised `SET` by one client is live for the tenant's next client.
That is nondeterministic behaviour inside a tenant, and it moves further from steering
answer 9 than A does.

**Verdict: meets the bar** (cross-*tenant* isolation is unconditional), with A's four-item
residue. All five constraints respected. It is an optimisation of A, to be chosen on
chunk 14's numbers, not on its own merits.

## C. Tenant-keyed connections: close on tenant switch

**Summary.** A connection belongs to one tenant for its whole life. It is never handed to
another tenant; capacity moves between tenants by closing a connection and opening a new one.

**Mechanism.** Structural: no backend process ever serves two tenants, so there is no
session for state to cross in. This is the pool-keying answer of Supavisor (tenant and
user) and PgBouncer (database and user) [canvas], adapted to one shared role: every
connection still logs in as the same role, and the key is the proxy's tenant.

**Cost and effort.** Two shapes:

- *Static partition:* each tenant gets a fixed share of the hard cap. Simple, but idle
  shares are stranded, and it stops working when tenants outnumber connections.
- *Reassign by reconnect:* a shared cap, with close-and-reopen when a connection must change
  tenant. Each switch costs a backend start and a SCRAM login to Postgres. The canvas did
  not measure this; it is certainly orders above the 0.044 ms reset [mine].

Either way the pool, the admission queues and the fairness design are rebuilt around
per-tenant sets of connections. That is a plan change well beyond chunk 5.

**What it gives up.** Decision D6's stated benefit: one role is "what makes them
interchangeable". Under C connections are not interchangeable across tenants, so the
fan-in the project exists to demonstrate shrinks to fan-in within a tenant, and per-tenant
fairness becomes quota arithmetic. Within a tenant, state still crosses clients, as in B.

**Verdict: meets the bar fully.** It is the only option here with no cross-tenant residue
at all, including the seed and extension state. It respects the five constraints as
written, and I flag that it works against the premise behind K2 and K3 (transaction
pooling over one interchangeable pool). The canvas already recorded "never share" as "not
available to us under decision D6"; I agree for static partition. Reconnect-on-switch is
available, at a price nobody has measured.

**Note on A, B and C together.** They are one code point with three policies: at a lease
boundary, do `DISCARD ALL` always (A), `DISCARD ALL` on tenant change (B), or close on
tenant change (C). A config key could select among them without a redesign, provided the
pool is written to tolerate a connection being replaced at release [mine].

## D. Canvas-refined (b)+(d1), with the conditional reset

**Summary.** The standing recommendation on file: reset unless the lease is "known clean",
where clean means every server `CommandComplete` tag is on a short list, no unexpected
`ParameterStatus` arrived, and the text contains none of a list of tainting substrings.
Sharing by a tiny recognised shape or a client hint. The uncontested third lands first (5a).

**Mechanism.** Two detectors decide whether the mechanism runs. The tags are exact for
top-level statements, since the server builds them from its own parse. The substring list
is for what tags cannot see: a change made inside an expression is tagged `SELECT` [canvas].

**Cost and effort.** About 400 syntax lines replacing about 1,500, plus the reset action.

**What it gives up.** As A for disguised features, less the round trip on clean leases.

**Verdict: fails the bar in its conditional form.** Whether the reset runs depends on a
tag list plus a substring list over client text. That is "we read the SQL well enough",
narrowed, not removed. Residual hole: `SELECT f()` where `f` does `SET`, `PREPARE`,
`LISTEN`, takes a session advisory lock or creates a temp table, and the call text contains
no listed word. All four were run and persisted [canvas]. `ParameterStatus` narrows it
only for reported settings, and on PostgreSQL 17 that list does not include `search_path`
[canvas]. No project in the canvas runs this fail-closed conditional form; the three that
reset on a detector all leak [canvas].

With the reset made unconditional (the canvas's own item 1), D's session safety **is
option A**, and the known-clean detector has no safety role left. What D then adds to A
does not concern session state: the 5a staging, the courtesy refusal, and the sharing
recogniser. Those stand or fall separately (see "Outside this memo's bar"). All five
constraints respected.

So (b)+(d1) earns its place only as "A, plus sequencing". The conditional reset, which was
its distinguishing feature, is the part that fails.

## E. Privilege hardening plus server tags

**Summary.** Remove the hidden paths in the database: `REVOKE TEMPORARY`, revoke `EXECUTE`
on `set_config`, the session advisory-lock functions and `setseed`, and give tenants no
right to create functions. What remains is top-level `SET`, `PREPARE`, `LISTEN`,
`DECLARE`, which the server's tags report exactly. Reset when such a tag appears.

**Mechanism.** The server enforces the revokes; the server writes the tags. The proxy
reads no client SQL.

**Cost and effort.** A start-up privilege check and the tag rule, perhaps 100 lines, plus
a deployment requirement on every database the proxy fronts.

**What it gives up.** Zero-configuration deployment against an ordinary database.

**Verdict: fails the bar.** It replaces a deny-list over SQL text with a deny-list over
the catalog, and that list has the same shape of problem. Residual hole: any function the
shared role may still execute that leaves session state and that nobody revoked. One
example outside the canvas's revoke list: `nextval`, after which `lastval()` returns the
previous tenant's value on the same connection [mine, not run; `DISCARD SEQUENCES` exists
for this]. Every extension function and every function the database owner wrote is another
candidate. No privilege exists for `SET`, `PREPARE`, `LISTEN` or `DECLARE … WITH HOLD`
[canvas], so an existing function that runs one of them is invisible to the tags.
Constraints respected. Useful as defence in depth behind A, not as the fix.

## F. A server that refuses: the splitters' backstop

**Summary.** The canvas found that every surviving read/write splitter sits in front of a
standby that refuses writes, so a misread costs an error and never a wrong result. The
question is whether session state has an equivalent.

**Verdict: it does not, in vanilla Postgres.** A read-only transaction allows `SET`,
`set_config`, `PREPARE`, `LISTEN`, `pg_advisory_lock` and `DECLARE … WITH HOLD` [canvas,
run]; the server treats them as backend-local and therefore harmless. No setting, role
attribute or privilege makes a backend refuse session changes. The backstop pattern
therefore does not apply to this problem at all. It applies to the *read-only* decision,
where the one-server form (`BEGIN READ ONLY` around the client's text) is escapable unless
the text is sent through an extended-protocol `Parse`, which is untested [canvas]. A real
standby would break K1.

## G. Shapes that dissolve the problem by breaking a constraint

| Shape | Seen in | Bar | Breaks | Cost to the premise |
| --- | --- | --- | --- | --- |
| One connection per client, or pin for the session | ReadySet, Pgpool-II, RDS Proxy | Meets when unconditional; pin-on-detect reads SQL and fails | K2 | No transaction pooling, so no proxy worth building |
| A database role per tenant, pools keyed by role | PgBouncer | Meets (it is C, enforced by the database too) | K3 | Decision D6; already recorded as a post-v1 seam |
| The server reports its own session state | YugabyteDB patch, Odyssey's private extension | Fails: the server's flag list is still a list, and YugabyteDB documents leaks | K1 | A C extension or patched server to own |
| A real parser (libpg_query) | Pgpool-II, PgDog, Supavisor | Fails by definition: it reads better, and five projects with real grammars all had list holes | K5 | A second dependency: cgo, or WASM with a 3.2 s first call |
| Track and replay settings | PgBouncer, PgDog, Vitess | Fails: covers reported settings only | none | A feature, not a safety mechanism |
| Continue the deny-list (assessment option a) | ProxySQL | Fails: safety is the reading | none | Open-ended |

## Outside this memo's bar

- **Durable state is shared by design.** Under D6 Postgres sees one role, so tables,
  functions and (from memory, not run) `ALTER ROLE … SET` on the shared role itself are
  common to all tenants. No option here changes that; the bar concerns session state only.
- **Sharing (coalescing) is a separate reading problem.** It is an allow-list that fails
  safe, and hinted-only sharing (assessment option d2) removes reading from it entirely.
  None of A, B or C decides it, and it does not need to be decided with them.
- **The test oracle.** Whatever is chosen, the canvas's catalog query
  (`canvas/experiments/postgres-server/dirty-check.sql`) checks the result on a real server
  after each lease, independent of any rule.

## Recommendation

Ranked:

1. **A, reset after every lease.** Smallest change, precedent in Odyssey's default, all
   five constraints intact, and nothing a client writes can switch it off. Its residue is
   four named items, one of them closable with a reseed.
2. **B, reset on tenant change,** only if chunk 14 shows A's round trip costs too much.
   It is the optimisation that keeps the bar; "known clean" is the one that loses it.
3. **C by reconnect,** only if the residue under A (setting names, extension state) is
   judged unacceptable. It is the one complete answer and the most expensive.
4. **E** as optional hardening behind A. **D's conditional reset, F and G:** no.

**If forced to pick one: A.** Build the release path so that "reset" and "close" are
interchangeable actions there, which leaves B and C open as policies. Take 5a from (d1) as
sequencing, since it carries no risk either way. Do not build the known-clean detector.

This differs from the recommendation on file in one respect: the canvas kept "known clean"
as an optimisation to switch on after measurement. By this memo's bar that switch
reintroduces reading into the trust base, and B is the optimisation to reach for instead.

Choosing A would change decision D8 and steering answer 9, so it is Nick's ruling, and it
is not requested here: chunk 5 remains parked.
