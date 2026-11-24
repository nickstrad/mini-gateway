#!/usr/bin/env python3
"""Source of the mini-gateway C4 set. Edit the tables in this file, then run:

    python3 docs/c4/gen.py

It writes docs/c4/model.json (a c4-sdk bundle, schemaVersion 1), one Markdown page per
diagram (Mermaid picture, element table, numbered relationship table), the Mermaid
sources under docs/c4/diagrams/, and the diagram table in docs/c4/README.md between its
markers. Standard library only. Validation steps are in docs/c4/README.md.

Everything here describes the TARGET architecture of PLAN.md (status "planned").
"""
import json
import re
from pathlib import Path

HERE = Path(__file__).resolve().parent
REVISION = "target-phase1-2026-11-24"
SRC = ["plan"]

E = {}      # id -> element
R = {}      # id -> relationship
D = []      # diagrams, in page order
IMPL = set()  # relationship ids that mean "implements"


def el(id, kind, name, desc, tech=None, parent=None, tags=(), **extra):
    e = {"id": id, "kind": kind, "name": name, "description": desc, "status": "planned",
         "tags": list(tags), "plannedNotes": [], "docIds": [], "sourceIds": SRC}
    if parent:
        e["parentId"] = parent
    if tech is not None:
        e["technology"] = tech
    e.update(extra)
    assert id not in E, id
    E[id] = e


def rel(id, src, dst, label, tech=None, kind=None):
    r = {"id": id, "sourceId": src, "targetId": dst, "label": label, "status": "planned",
         "plannedNotes": [], "docIds": [], "sourceIds": SRC}
    if tech:
        r["technology"] = tech
    if kind == "implementation":
        # c4-sdk 0.1.0 allows the "implementation" relationship kind between code elements only,
        # so the model records it in the technology text and the pictures draw it dashed.
        IMPL.add(id)
        r["technology"] = (tech + ", " if tech else "") + "interface implementation"
    assert id not in R, id
    assert src in E and dst in E, (id, src, dst)
    R[id] = r


def person(id, name, desc):
    el(id, "person", name, desc)


def system(id, name, desc, tech=None, tags=()):
    el(id, "softwareSystem", name, desc, tech=tech, tags=tags)


def container(id, name, desc, tech="Go package", tags=()):
    el(id, "container", name, desc, tech=tech, parent="gw", tags=tags, containerType="application")
    # A neighbour twin, used only inside other containers' component views (c4-sdk 0.1.0
    # accepts a container in a component view only when the element is tagged external).
    el("n-" + id[2:], "container", name, "Neighbour in a component view: stands for the container `%s`." % id,
       tech=tech, parent="gw", tags=("external", "neighbour"), containerType="application")


def comp(parent, id, name, desc, tech="Go", tags=()):
    el(id, "component", name, desc, tech=tech, parent=parent, tags=tags)


# --------------------------------------------------------------------------- people, systems
person("app", "Client application",
       "Any program that speaks the Postgres wire protocol. Its startup user name selects its tenant.")
person("operator", "Operator",
       "Runs proxy instances, writes the static fleet config, reads health and stats, drains an instance with SIGTERM.")
person("developer", "Developer or reader",
       "Runs simulator scenarios from a seed, replays a failure from its printed REPLAY line, runs make test / chaos / bench.")
system("gw", "mini-gateway",
       "A proxy that speaks the Postgres wire protocol (simple-query subset) in front of one Postgres: a hard-capped "
       "connection pool lent per transaction, per-tenant admission with two CoDel modes, read coalescing, timer-free "
       "batching, an off-by-default retry policy, snapshot observability. Runs as N independent instances.",
       tech="Go; one external package (pgproto3)")
system("pg", "Postgres",
       "One ordinary Postgres server. Sees at most the fleet's connection budget, all logged in as one role.",
       tech="PostgreSQL 17", tags=("external",))
system("telemetry", "Metrics and log readers",
       "A Prometheus scraper, an OpenTelemetry Collector, CSV files, log readers. They read exporters and merge "
       "across instances; no component knows about them.", tags=("external",))
system("pgproto3", "pgproto3 (from pgx/v5)",
       "Go package github.com/jackc/pgx/v5/pgproto3: Postgres wire message structs with Encode and Decode. "
       "The one external dependency, used for message encoding only.", tech="Go library", tags=("external",))

# --------------------------------------------------------------------------- containers
container("c-shell", "Shell and binary",
          "cmd/mini-gateway and internal/shell. Listeners, one reader and one writer goroutine per connection, bounded "
          "mailboxes, a deadline on every socket operation, the admin listener on its own port, signal handling. "
          "Makes no decisions: it posts events to the engine and performs the actions the engine returns.",
          tech="Go, goroutines, TCP")
container("c-engine", "Engine",
          "internal/proxy. One single-threaded, sans-I/O state machine per instance: events in, actions out. Holds the "
          "client and database connection tables and the request pipeline with its stage seams. The simulator and the "
          "shell drive this same code.", tech="Go, pure state machine")
container("c-pgwire", "Wire layer",
          "internal/pgwire. Everything that knows Postgres: framing, both session state machines, the query-cycle "
          "tracker, the statement classifier, the error contract. The only product package that imports pgproto3.",
          tech="Go, pure state machines, pgproto3")
container("c-work", "Work vocabulary",
          "internal/work. Small plain types the cores are written against: tenant, unit of work and its class, share "
          "key, batchable flag, outcome. Carries no Postgres type. This is the extension seam for a second protocol.",
          tech="Go types")
container("c-adm", "Admission",
          "internal/admission. Rate gate, per-tenant queues with two selectable queue rules, adaptive LIFO, round-robin "
          "dispatcher with shares that bind under saturation, adaptive concurrency limit.", tech="Go, pure core")
container("c-pool", "Pool",
          "internal/pool. Hard-capped database connections, creation limiter, leases per transaction, the one breaker, "
          "the dirty-connection table.", tech="Go, pure core")
container("c-coal", "Coalescer",
          "internal/coalesce. Identical provably-read-only statements share one database read. Never writes.",
          tech="Go, pure core")
container("c-batch", "Batcher",
          "internal/batch. Timer-free: when a connection frees up, eligible queued single statements go out in one "
          "write. Off by default.", tech="Go, pure core")
container("c-retry", "Retry policy",
          "internal/retry. A policy interface. Off by default; the other policy retries once, only when nothing reached "
          "the database, within a 10% budget.", tech="Go, pure core")
container("c-obs", "Observability",
          "internal/obs. Snapshot conventions, integer histogram, collector, fleet merge, health, and the exporters.",
          tech="Go, standard library")
container("c-config", "Configuration",
          "internal/config. The config file, the static fleet block (instance count N; fleet budgets divided by N), "
          "and load-time validation that fails closed.", tech="Go")
container("c-sim", "Simulator harness",
          "internal/sim, internal/sim/simnet, fakepg, workload, simtest, cmd/mgsim. A deterministic single-threaded "
          "world: virtual clock, simulated byte streams, a fake Postgres, simulated clients, faults, replay. Test code; "
          "built before any proxy code.", tech="Go test harness", tags=("test",))
container("c-e2e", "End-to-end suite and load generator",
          "test/e2e and cmd/loadgen. Each scenario is written once and runs on the simulator (tier S) and on the real "
          "binary against a real Postgres (tier R). The load generator is open-loop.", tech="Go test code", tags=("test",))

# --------------------------------------------------------------------------- context relationships
rel("r-app-gw", "app", "gw", "Connects, logs in, sends statements and transactions", "Postgres wire protocol v3, simple query, TCP")
rel("r-op-gw", "operator", "gw", "Configures the fleet; reads health and stats; drains", "Config file, HTTP admin port, SIGTERM")
rel("r-gw-pg", "gw", "pg", "Lends pooled connections per transaction; at most the configured budget", "Postgres wire protocol v3, TCP")
rel("r-tel-gw", "telemetry", "gw", "Scrapes metrics and stats from every instance and merges them", "HTTP: Prometheus text, JSON")
rel("r-gw-tel", "gw", "telemetry", "Pushes metrics; writes CSV and log lines", "OTLP/HTTP JSON, files")
rel("r-dev-gw", "developer", "gw", "Runs simulator scenarios and the test, chaos and benchmark commands", "go test, make, cmd/mgsim")
rel("r-gw-lib", "gw", "pgproto3", "Encodes and decodes wire messages", "Go import, wire layer only")

# --------------------------------------------------------------------------- container view A (request path)
rel("c1", "app", "c-shell", "Connects, logs in, sends statements", "Postgres wire protocol, TCP")
rel("c2", "operator", "c-shell", "Reads health, stats and metrics; sends SIGTERM to drain", "HTTP admin port, signal")
rel("c3", "c-shell", "c-engine", "Posts events (bytes arrived, connection opened or closed, clock) and performs the returned actions", "Go, bounded mailboxes")
rel("c4", "c-engine", "c-pgwire", "Turns bytes into work units and outcomes; builds replies the proxy makes itself", "Go, in-process")
rel("c5", "c-engine", "c-adm", "Gate, enqueue, pick the next request, report completions", "Go, in-process")
rel("c6", "c-engine", "c-coal", "Join or start a shared read; fan the reply out", "Go, in-process")
rel("c7", "c-engine", "c-pool", "Lend and release connections; open and close decisions; breaker state", "Go, in-process")
rel("c8", "c-engine", "c-batch", "Take a batch for a free connection; split the replies", "Go, in-process")
rel("c9", "c-engine", "c-retry", "Ask whether a failure may be retried", "Go, in-process")
rel("c10", "c-shell", "pg", "Opens the pooled connections and moves their bytes", "Postgres wire protocol, TCP")
rel("c11", "c-engine", "c-obs", "Publishes one instance snapshot per interval", "Go, plain structs")
rel("c12", "c-shell", "c-obs", "Admin listener serves exporters and health from the last published snapshot", "Go, in-process")
rel("c13", "telemetry", "c-shell", "Scrapes Prometheus text and JSON stats", "HTTP admin port")
rel("c14", "c-obs", "telemetry", "Pushes OTLP metrics (last build chunk); writes CSV and log lines", "OTLP/HTTP JSON, files")
rel("c15", "c-shell", "c-config", "Loads and validates the config and the fleet block at start", "Go, in-process")

# --------------------------------------------------------------------------- container view B (drivers and seams)
rel("s1", "c-sim", "c-engine", "Drives the same engine on its event loop, over simulated byte streams", "Go, transport seam")
rel("s2", "c-pgwire", "pgproto3", "The codec boundary: message structs encoded and decoded here and nowhere else in product code", "Go import")
rel("s3", "c-pgwire", "c-work", "Produces work units and outcomes", "Go types")
rel("s4", "c-adm", "c-work", "Written against these types only", "Go types")
rel("s5", "c-pool", "c-work", "Written against these types only", "Go types")
rel("s6", "c-coal", "c-work", "Written against these types only", "Go types")
rel("s7", "c-batch", "c-work", "Written against these types only", "Go types")
rel("s8", "c-retry", "c-work", "Written against these types only", "Go types")
rel("s9", "c-sim", "pgproto3", "The fake Postgres and the simulated clients speak real wire bytes", "Go import, test code")
rel("s10", "c-e2e", "c-sim", "Tier S: runs each scenario on the simulator", "Go test")
rel("s11", "c-e2e", "c-shell", "Tier R: runs the real binary and drives it with test clients and open-loop load", "Process, Postgres wire protocol")
rel("s12", "c-e2e", "pg", "Tier R: starts a real Postgres 17 container and checks its state", "Docker, Postgres wire protocol")
rel("s13", "developer", "c-sim", "Runs a scenario from a seed; replays a failure", "cmd/mgsim, go test")
rel("s14", "developer", "c-e2e", "make test, make chaos, make bench", "make")
rel("s15", "c-sim", "c-work", "Synthetic workloads drive the cores directly, with no Postgres code", "Go types, test code")

# --------------------------------------------------------------------------- engine components
P = "c-engine"
comp(P, "e-transport", "Transport seam",
     "Dialer and byte-stream connection events and actions: open, bytes in, bytes out, closed. Knows no protocol. "
     "Two implementations: TCP in the shell, simulated streams in the harness.", tech="Go interface")
comp(P, "e-sessions", "Client session table",
     "One entry per client connection: its wire state machine, tenant, transaction state, the single request it may "
     "have inside the proxy, and its bounded reply buffer.")
comp(P, "e-backends", "Database connection table",
     "One entry per database connection: its wire state machine, the query-cycle tracker, and who holds it.")
comp(P, "e-pipeline", "Request pipeline",
     "The fixed order every request takes, each stage a small interface: classify, reject list, draining, breaker, "
     "rate gate, coalesce, queue, lease, forward, release. Statements inside an open transaction skip from classify "
     "straight to forward on the connection the client already holds.")
comp(P, "e-relay", "Reply relay",
     "Streams database messages to the client (or to every waiter of a shared read) through bounded buffers, with "
     "back pressure in both directions. Never sends a short reply that looks complete.")
comp(P, "e-fail", "Failure classifier",
     "For every failure records whether any byte reached the database, and picks the error-contract row.")
comp(P, "e-clock", "Deadline table",
     "Every wait in the timeout budget, as deadlines on the injected clock; returns the next wake-up time as an action.")
comp(P, "e-drain", "Drain controller",
     "Sequences shutdown: stop admitting, finish transactions in progress within a bound, close idle connections, exit.")
comp(P, "e-publish", "Snapshot publisher",
     "Collects every component's Snapshot() between events and hands the copy to the collector.")
rel("e1", "n-shell", "e-transport", "Implements it with TCP sockets; posts events through bounded mailboxes", "Go", "implementation")
rel("e2", "n-sim", "e-transport", "Implements it with simulated byte streams on the event loop", "Go", "implementation")
rel("e3", "e-transport", "e-sessions", "Client bytes and connection events", "Go")
rel("e4", "e-transport", "e-backends", "Database bytes and connection events", "Go")
rel("e5", "e-sessions", "n-pgwire", "Client session machine; statement classifier", "Go")
rel("e6", "e-backends", "n-pgwire", "Database session machine; query-cycle tracker", "Go")
rel("e7", "e-sessions", "e-pipeline", "One work unit at a time per client", "Go")
rel("e8", "e-pipeline", "n-adm", "Gate, queue, dispatch, limit", "Go")
rel("e9", "e-pipeline", "n-coal", "Join or lead a shared read", "Go")
rel("e10", "e-pipeline", "n-pool", "Lend, release, breaker state, dirty connections", "Go")
rel("e11", "e-pipeline", "n-batch", "Batch for a free connection", "Go")
rel("e12", "e-pipeline", "n-retry", "May this failure be retried", "Go")
rel("e13", "e-pipeline", "e-backends", "Forwards the statement on the lent connection", "Go")
rel("e14", "e-backends", "e-relay", "Reply messages and the end-of-reply status byte", "Go")
rel("e15", "e-relay", "e-sessions", "Reply bytes for each recipient", "Go")
rel("e16", "e-pipeline", "e-fail", "Every refusal and failure", "Go")
rel("e17", "e-fail", "n-pgwire", "Builds the error reply for its contract row", "Go")
rel("e18", "e-clock", "e-pipeline", "A deadline was reached", "Go")
rel("e19", "e-drain", "e-pipeline", "Sets the draining input", "Go")
rel("e20", "e-publish", "n-obs", "Hands over the instance snapshot", "Go, plain structs")
rel("e21", "e-pipeline", "n-work", "Passes work units and outcomes between stages", "Go types")

# --------------------------------------------------------------------------- wire layer components
P = "c-pgwire"
comp(P, "w-framer", "Framer",
     "Ours. Cuts a byte stream into messages (type byte, 4-byte length, body) whatever the fragmentation, down to "
     "one byte at a time; enforces a maximum message size.")
comp(P, "w-codec", "Codec boundary",
     "The only product code that touches pgproto3: decodes a framed body into a message struct and encodes structs "
     "to bytes. pgproto3's own blocking reader is not used.", tech="Go, pgproto3")
comp(P, "w-client", "Client session machine",
     "Client-facing. Answers TLS and GSS requests with no, reads the startup message, finds the tenant by user, runs "
     "SCRAM-SHA-256 (server side) or trust, sends the parameter set and proxy-issued key data, recognises cancel "
     "requests, takes in one query at a time, handles terminate.")
comp(P, "w-backend", "Database session machine",
     "Database-facing. Startup, SCRAM-SHA-256 (client side), collects server parameters and key data, reaches ready.")
comp(P, "w-cycle", "Query-cycle tracker",
     "Follows a reply to its ReadyForQuery, reports the transaction status byte (idle, in transaction, failed), "
     "flags an unsolicited ParameterStatus (the connection is now dirty) and any protocol surprise.")
comp(P, "w-scram", "SCRAM-SHA-256 functions",
     "Pure functions on the standard library. Shared with the simulator's fake Postgres and test clients.",
     tech="Go, crypto/pbkdf2, hmac, sha256")
comp(P, "w-lexer", "SQL lexer",
     "Tokens only: strings, escape strings, dollar quotes, comments, identifiers; splits a message into statements. "
     "Not a parser.")
comp(P, "w-classifier", "Statement classifier",
     "Turns a statement into a work unit: rejected (session-level feature), transaction control, provably read-only, "
     "shareable with its key, batchable. Conservative: what it cannot tokenise is never shared or batched.")
comp(P, "w-errors", "Error contract and reply builders",
     "One table: each refusal and failure, its SQLSTATE, whether anything reached the database, whether a write may "
     "have applied. Builds the ErrorResponse and the correct end marker. docs/error-contract.md is generated from it.")
rel("w1", "n-engine", "w-client", "Client bytes in; events and bytes out", "Go")
rel("w2", "n-engine", "w-backend", "Database bytes in; events and bytes out", "Go")
rel("w3", "n-engine", "w-cycle", "Reply bytes in; status and message boundaries out", "Go")
rel("w4", "n-engine", "w-classifier", "Statement text in; work unit out", "Go")
rel("w5", "n-engine", "w-errors", "Contract row in; reply bytes out", "Go")
rel("w6", "w-client", "w-codec", "Decode and encode messages", "Go")
rel("w7", "w-backend", "w-codec", "Decode and encode messages", "Go")
rel("w8", "w-cycle", "w-codec", "Decode message headers and status", "Go")
rel("w9", "w-errors", "w-codec", "Encode ErrorResponse and ReadyForQuery", "Go")
rel("w10", "w-codec", "w-framer", "Framed message bodies", "Go")
rel("w11", "w-codec", "pgproto3", "Message structs: Encode, Decode", "Go import")
rel("w12", "w-client", "w-scram", "Server side of the exchange", "Go")
rel("w13", "w-backend", "w-scram", "Client side of the exchange", "Go")
rel("w14", "w-classifier", "w-lexer", "Tokens and statement boundaries", "Go")
rel("w15", "w-classifier", "n-work", "Produces work units", "Go types")
rel("w16", "w-cycle", "n-work", "Produces outcomes", "Go types")

# --------------------------------------------------------------------------- admission components
P = "c-adm"
comp(P, "a-gate", "Rate gate (GCRA)",
     "Optional outer gate, off by default: a per-tenant rate and burst in integer nanoseconds. Refuses before anything "
     "is queued. Rates are divided by the instance count.")
comp(P, "a-tenants", "Tenant table",
     "State for tenants that are active now: queue, in-flight count, share, gate state. Created on first use, removed "
     "when all-zero, so memory follows active tenants.")
comp(P, "a-queues", "Per-tenant queues",
     "A fixed-capacity ring per tenant. Age is measured on the oldest item; expired items are swept from the head and "
     "answered at once; overflow sheds the head. Serves newest-first while the rule reports overload (adaptive LIFO).")
comp(P, "a-rule", "QueueRule",
     "The interface both shedding rules implement; chosen by config.", tech="Go interface")
comp(P, "a-rule-timeout", "Timeout rule",
     "Mode 1, from \"Fail at Scale\": one last-empty timestamp per queue; allowed wait is the short target while a "
     "standing queue exists, otherwise the interval.")
comp(P, "a-rule-law", "Control-law rule",
     "Mode 2, from the CoDel paper and RFC 8289: sojourn above target for an interval starts dropping; next drop after "
     "interval / sqrt(count), in integer arithmetic.")
comp(P, "a-arms", "Comparison arms",
     "Simulator only: plain FIFO and folly's minimum-delay detector, for the head-to-head table.", tags=("test",))
comp(P, "a-dispatch", "Dispatcher",
     "Chooses whose request runs next: round robin across tenants with waiting work; a tenant's share binds only when "
     "the limit is reached; otherwise work-conserving.")
comp(P, "a-limit", "Adaptive limit (AIMD)",
     "One integer limit on concurrent leases. Cut by a tenth on a slow statement, a timeout or a connection failure; "
     "raised by one when busy. Bounded by a floor, the pool size, a maximum step and a minimum interval. Reads "
     "database round-trip time, never queue wait.")
comp(P, "a-snap", "Admission snapshot",
     "Snapshot(): per tenant and total — depth, oldest-item age, queue-wait histogram, rule state, LIFO switches, "
     "sheds by reason, in flight, share, limit and its changes, gate refusals.")
rel("a1", "n-engine", "a-gate", "May this request enter", "Go")
rel("a2", "n-engine", "a-queues", "Enqueue; sweep expired", "Go")
rel("a3", "n-engine", "a-dispatch", "Capacity freed: who runs next", "Go")
rel("a4", "n-engine", "a-limit", "Statement time and outcome of each completion", "Go")
rel("a5", "n-engine", "a-snap", "Snapshot()", "Go")
rel("a6", "a-queues", "a-rule", "Allowed wait; overloaded or not", "Go")
rel("a7", "a-rule-timeout", "a-rule", "Implements", "Go", "implementation")
rel("a8", "a-rule-law", "a-rule", "Implements", "Go", "implementation")
rel("a9", "a-arms", "a-rule", "Implement (simulator only)", "Go", "implementation")
rel("a10", "a-dispatch", "a-queues", "Pops the chosen tenant's next request", "Go")
rel("a11", "a-dispatch", "a-limit", "Reads the current limit", "Go")
rel("a12", "a-dispatch", "a-tenants", "In-flight counts and shares", "Go")
rel("a13", "a-gate", "a-tenants", "Per-tenant gate state", "Go")
rel("a14", "a-queues", "a-tenants", "Per-tenant queue", "Go")
rel("a15", "a-dispatch", "n-work", "Work units and outcomes", "Go types")

# --------------------------------------------------------------------------- pool components
P = "c-pool"
comp(P, "p-acct", "Connection accounting",
     "Every database connection is connecting, idle, lent or closing. Invariant: open + connecting never exceeds the "
     "pool size, which is the fleet budget divided by the instance count.")
comp(P, "p-create", "Creation limiter",
     "Caps concurrent connects and the rate of new connections, so a storm or a cold start cannot flood the database.")
comp(P, "p-lease", "Lease table",
     "Lends a connection for one transaction: held while the status byte says in-transaction or failed, released on "
     "idle. Enforces the idle-in-transaction deadline.")
comp(P, "p-breaker", "Database-down breaker",
     "The one circuit breaker. Counts connection-class failures only, never SQL errors. Open: requests are refused at "
     "once and no dial is made. Half-open: one trial connect.")
comp(P, "p-dirty", "Dirty-connection table",
     "What to do with a connection whose client vanished or that surprised us: cancel, wait a bounded time, roll back, "
     "then reuse only if it is idle and clean; otherwise close. Destroy on doubt.")
comp(P, "p-reaper", "Idle and lifetime reaper",
     "Closes idle and old connections, with jitter so they do not all expire together; checks a long-idle connection "
     "before reuse.")
comp(P, "p-cancel", "Cancel map",
     "Maps proxy-issued key data to the connection a client currently holds, so a client's cancel request reaches its "
     "own statement and no one else's. Caps concurrent cancels.")
comp(P, "p-snap", "Pool snapshot",
     "Snapshot(): open, idle, lent, connecting, creation rate, closes by reason, exhaustion events, breaker state and "
     "transitions, dirty-table outcomes, lease-time histogram.")
rel("p1", "n-engine", "p-lease", "Lend to a work unit; release on outcome", "Go")
rel("p2", "n-engine", "p-breaker", "Reports connect and connection failures; asks whether to fail fast", "Go")
rel("p3", "n-engine", "p-dirty", "Client vanished, deadline passed, or protocol surprise", "Go")
rel("p4", "n-engine", "p-cancel", "Client cancel request", "Go")
rel("p5", "n-engine", "p-snap", "Snapshot()", "Go")
rel("p6", "p-lease", "p-acct", "Idle to lent and back", "Go")
rel("p7", "p-acct", "p-create", "May another connection be opened now", "Go")
rel("p8", "p-create", "p-breaker", "Is the database marked down", "Go")
rel("p9", "p-dirty", "p-acct", "Reuse or close", "Go")
rel("p10", "p-reaper", "p-acct", "Close idle and old connections", "Go")
rel("p11", "p-cancel", "p-lease", "Finds the lent connection", "Go")
rel("p12", "p-lease", "n-work", "Work units and outcomes (released clean, still held, dirty)", "Go types")

# --------------------------------------------------------------------------- coalescer components
P = "c-coal"
comp(P, "f-join", "Join rule",
     "Decides whether a shareable request leads a new read or joins one. Strict (default): never join a read already "
     "running on the database — join the next one, which starts when the running one ends. Relaxed: join the running read.")
comp(P, "f-table", "Flight table",
     "In-flight and pending shared reads, keyed by the exact statement text. At most one read per key is on the "
     "database at a time. Entries are removed on completion.")
comp(P, "f-waiters", "Waiter set",
     "The callers of one flight, counted. Capped. A caller that leaves gets its own error and does not disturb the rest.")
comp(P, "f-deadline", "Flight deadline",
     "The flight runs under its own deadline, not any caller's. The database work is cancelled only when the last "
     "waiter has left.")
comp(P, "f-reply", "Shared reply buffer",
     "The reply's message bytes, stored once and capped; each recipient gets them with its own end marker. Errors are "
     "shared too.")
comp(P, "f-neg", "Do-not-share list",
     "Keys whose reply was too large to share. Bounded and expiring.")
comp(P, "f-snap", "Coalescer snapshot",
     "Snapshot(): flights, hits, waiters per flight, cancellations, overflows by cap, shared errors, table size.")
rel("f1", "n-engine", "f-join", "A shareable work unit arrived", "Go")
rel("f2", "n-engine", "f-waiters", "A waiter left", "Go")
rel("f3", "n-engine", "f-reply", "Reply bytes; reply complete or failed", "Go")
rel("f4", "n-engine", "f-snap", "Snapshot()", "Go")
rel("f5", "f-join", "f-table", "Look up or create the flight", "Go")
rel("f6", "f-join", "f-neg", "Is this key barred from sharing", "Go")
rel("f7", "f-table", "f-waiters", "Adds the caller", "Go")
rel("f8", "f-waiters", "f-deadline", "Last waiter gone: cancel", "Go")
rel("f9", "f-reply", "f-waiters", "Fans the reply out", "Go")
rel("f10", "f-reply", "f-neg", "Reply over the cap", "Go")
rel("f11", "f-join", "n-work", "Reads the share key of a work unit", "Go types")

# --------------------------------------------------------------------------- batcher components
P = "c-batch"
comp(P, "b-take", "Take rule",
     "When a connection frees up, takes what is already queued and eligible, up to a count and byte cap. One request "
     "with nothing else waiting goes alone at once. There is no timer.")
comp(P, "b-elig", "Eligibility check",
     "A request may be batched only if the classifier proved it is a single statement that is not transaction control, "
     "and its client is outside a transaction.")
comp(P, "b-split", "Reply splitter",
     "Splits the replies in order at the end-of-reply markers and matches each to its requester.")
comp(P, "b-poison", "Poison rule",
     "If any end marker reports a status other than idle, the connection is closed and the remaining members get the "
     "may-have-been-applied error.")
comp(P, "b-snap", "Batcher snapshot",
     "Snapshot(): batch-size histogram, batched share, in-batch wait histogram, poison events, ineligible reasons.")
rel("b1", "n-engine", "b-take", "A connection is free and requests are waiting", "Go")
rel("b2", "n-engine", "b-split", "Reply bytes of a batch", "Go")
rel("b3", "n-engine", "b-snap", "Snapshot()", "Go")
rel("b4", "b-take", "b-elig", "Is this request eligible", "Go")
rel("b5", "b-split", "b-poison", "Status byte of each end marker", "Go")
rel("b6", "b-elig", "n-work", "Reads the batchable flag of a work unit", "Go types")

# --------------------------------------------------------------------------- retry components
P = "c-retry"
comp(P, "r-policy", "Policy",
     "The interface the engine asks: given this failure — did anything reach the database, is the breaker open, how "
     "much of the deadline is left — may it be retried.", tech="Go interface")
comp(P, "r-off", "Off",
     "The default. Never retries.")
comp(P, "r-once", "PreForwardOnce",
     "Retries at most once, only when nothing reached the database, only within budget, never while the breaker is "
     "open, never holding a queue slot or a connection while it waits.")
comp(P, "r-budget", "Retry budget",
     "An integer bucket: each first attempt deposits a tenth of a token, each retry takes one; capped, so credit "
     "expires. Retries stay at or below 10% of requests.")
comp(P, "r-snap", "Retry snapshot",
     "Snapshot(): attempts, retries, budget level, deposits, withdrawals, exhaustions, refusals by reason.")
rel("t1", "n-engine", "r-policy", "May this failure be retried", "Go")
rel("t2", "n-engine", "r-budget", "Deposit on each first attempt", "Go")
rel("t3", "n-engine", "r-snap", "Snapshot()", "Go")
rel("t4", "r-off", "r-policy", "Implements", "Go", "implementation")
rel("t5", "r-once", "r-policy", "Implements", "Go", "implementation")
rel("t6", "r-once", "r-budget", "Withdraw one token per retry", "Go")
rel("t7", "r-policy", "n-work", "Reads the outcome's reached-the-database fact", "Go types")

# --------------------------------------------------------------------------- simulator components
P = "c-sim"
comp(P, "s-loop", "Event loop",
     "One thread, one heap ordered by (time, sequence), opaque events. Step budget, stall errors, panic guard.")
comp(P, "s-clock", "Virtual clock and timers",
     "64-bit nanoseconds, cancellable timers. Time moves only when the loop moves it.")
comp(P, "s-streams", "Named random streams",
     "One generator per (seed, label), so a new draw in one place does not shift any other.")
comp(P, "s-net", "Simulated byte streams",
     "internal/sim/simnet. Ordered byte connections with chosen fragmentation (down to one byte), latency per "
     "direction, a bounded send window, half-close, reset, stall, half-open.")
comp(P, "s-fakepg", "Fake Postgres",
     "internal/sim/fakepg. Speaks the simple-query subset in real wire bytes; connection limit; transaction status; a "
     "small keyed table with a write log; service-time models; every database-side fault; its own snapshot.",
     tech="Go, pgproto3")
comp(P, "s-workload", "Clients and tenants",
     "internal/sim/workload. Simulated clients: tenants, open-loop arrivals measured from intended send time, Zipf "
     "keys, transactions, disconnects, cancels, aggressive retries.", tech="Go, pgproto3")
comp(P, "s-faults", "Fault schedule",
     "Timed faults recorded in the trace; decodable from bytes for the fuzzer; a delete-one-fault shrinker. Faults are "
     "injected only from outside the proxy code.")
comp(P, "s-trace", "Decision trace and digests",
     "Every decision (accepted, coalesced, shed, timed out) appended in order; separate digests for the trace and for "
     "final state. Same seed, same digests.")
comp(P, "s-inv", "Invariants",
     "Checked at every event: connection cap, conservation of requests. Checked at the end: quiesce (nothing in use, "
     "nothing queued, no flight, no timer).")
comp(P, "s-simtest", "Seeds, replay, sweep",
     "internal/sim/simtest. Seed flags and pinned seeds, the run-twice check, sweeps, the REPLAY line printed on every "
     "failure.")
comp(P, "s-cli", "cmd/mgsim",
     "Runs a named scenario from a seed outside go test and prints the digests.", tech="Go CLI")
comp(P, "s-toy", "Toy echo protocol",
     "A tiny protocol with a planted-bug switch, used only to test the harness itself.")
rel("m1", "developer", "s-cli", "Runs a scenario with a seed", "Command line")
rel("m2", "s-cli", "s-simtest", "Run, verify, report", "Go")
rel("m3", "s-simtest", "s-loop", "Runs the scenario twice and compares digests", "Go")
rel("m4", "s-loop", "s-clock", "Advances time to the next event", "Go")
rel("m5", "s-loop", "s-net", "Delivers byte and connection events", "Go")
rel("m6", "s-net", "n-engine", "Implements the engine's transport seam: bytes in, bytes out, opened, closed", "Go", "implementation")
rel("m7", "s-workload", "s-net", "Opens client connections; sends and reads", "Go")
rel("m8", "s-fakepg", "s-net", "Accepts database connections; replies", "Go")
rel("m9", "s-faults", "s-net", "Stall, reset, fragment, half-open", "Go")
rel("m10", "s-faults", "s-fakepg", "Slow, kill, freeze, refuse, garbage", "Go")
rel("m11", "s-faults", "s-workload", "Disconnect, stop reading, retry storm", "Go")
rel("m12", "s-workload", "s-streams", "Arrivals, keys, tenants", "Go")
rel("m13", "s-fakepg", "s-streams", "Service times", "Go")
rel("m14", "s-fakepg", "pgproto3", "Wire messages", "Go import")
rel("m15", "s-workload", "pgproto3", "Wire messages", "Go import")
rel("m16", "s-fakepg", "n-pgwire", "Reuses the SCRAM functions", "Go")
rel("m17", "n-engine", "s-trace", "Appends each decision", "Go")
rel("m18", "s-inv", "n-engine", "Reads snapshots at every event and at the end", "Go")
rel("m19", "s-simtest", "s-trace", "Compares digests across runs", "Go")
rel("m20", "s-toy", "s-net", "Echo over simulated streams, for the meta-tests", "Go")

# --------------------------------------------------------------------------- observability components
P = "c-obs"
comp(P, "o-contract", "Snapshot conventions",
     "What every component's Snapshot() returns: a plain struct of monotonic counters and current gauges. No "
     "wall-clock time, no map order, no telemetry imports.")
comp(P, "o-hist", "Integer histogram",
     "Fixed array, fixed relative error, no allocation on record; record, merge, percentile. Out-of-range values are "
     "clamped and counted.")
comp(P, "o-collector", "Collector",
     "Assembles one instance snapshot from the component snapshots, with instance id, build and config fingerprint. "
     "Keeps the last published copy; readers never wait on the engine.")
comp(P, "o-merge", "Fleet merge",
     "Merges instance snapshots: counters and gauges sum, histograms merge, ratios are recomputed, never averaged.")
comp(P, "o-health", "Health",
     "Three states: ok; degraded while shedding, while the breaker is open, at the limit's floor, or draining; failed "
     "only when this instance cannot work. A database outage is degraded.")
comp(P, "o-prom", "Prometheus text exporter",
     "Low-cardinality labels and a bounded tenant label (configured tenants up to a cap, the rest as other).")
comp(P, "o-json", "JSON stats exporter",
     "The full snapshot, including every active tenant.")
comp(P, "o-csv", "CSV writer",
     "Evidence tables for scenarios and benchmarks.")
comp(P, "o-log", "Log-line exporter",
     "One structured line per interval.")
comp(P, "o-otlp", "OTLP/HTTP push exporter",
     "Hand-written on the standard library, no SDK. Bounded queue, timeout, drop-and-count when the collector is down. "
     "Lands in the last build chunk and touches no component.")
rel("o1", "n-engine", "o-collector", "Hands over every component's snapshot at each publish", "Go, plain structs")
rel("o2", "o-collector", "o-contract", "Assembles the instance snapshot", "Go")
rel("o3", "o-contract", "o-hist", "Histograms inside snapshots", "Go")
rel("o4", "o-collector", "o-health", "Derives the health state and its reasons", "Go")
rel("o5", "n-shell", "o-prom", "Admin listener serves /metrics", "HTTP")
rel("o6", "n-shell", "o-json", "Admin listener serves /stats", "HTTP")
rel("o7", "n-shell", "o-health", "Admin listener serves /health", "HTTP")
rel("o8", "o-prom", "o-collector", "Reads the last published snapshot", "Go")
rel("o9", "o-json", "o-collector", "Reads the last published snapshot", "Go")
rel("o10", "o-csv", "o-collector", "Reads the last published snapshot", "Go")
rel("o11", "o-log", "o-collector", "Reads the last published snapshot", "Go")
rel("o12", "o-otlp", "o-collector", "Reads the last published snapshot", "Go")
rel("o13", "o-otlp", "telemetry", "Pushes metrics", "OTLP/HTTP JSON")
rel("o14", "o-merge", "o-contract", "Merges snapshots from several instances", "Go")

# --------------------------------------------------------------------------- deployment (N = 3)
ENV = "fleet-of-3"
el("d-clients", "deploymentNode", "Application hosts", "Clients of every tenant. They spread their connections over "
   "the instances themselves, or through any TCP balancer; we build none.", tech="Any", environment=ENV)
el("d-tier", "deploymentNode", "Proxy tier, N = 3", "Three independent mini-gateway processes with the same static "
   "config: fleet.instances = 3, so each takes a third of every fleet budget. No instance knows about the others.",
   tech="Linux hosts or containers", environment=ENV)
el("d-db", "deploymentNode", "Database host", "One Postgres. Its connection limit must cover the fleet budget plus a "
   "reserve; config validation checks it.", tech="Linux host or container", environment=ENV)
el("d-ops", "deploymentNode", "Operations", "Where metrics are scraped or pushed to, and merged across instances.",
   tech="Any", environment=ENV)
el("i-spread", "infrastructureNode", "Client connections", "500 clients here; each connects to one instance.",
   tech="Postgres wire protocol", parent="d-clients")
for k in (1, 2, 3):
    el("i-gw%d" % k, "instance", "mini-gateway instance %d" % k,
       "Own engine, pool (fleet budget / 3), queues, flights, limit, retry budget and snapshots. Drains on SIGTERM.",
       parent="d-tier", instanceOf="c-shell", environment=ENV)
el("i-pg", "infrastructureNode", "Postgres 17", "Sees at most the fleet connection budget (for example 3 x 32 = 96), "
   "all as one role.", tech="PostgreSQL", parent="d-db")
el("i-tel", "infrastructureNode", "Scraper or collector", "Reads each instance's exporters; sums counters, merges "
   "histograms, recomputes ratios.", tech="Prometheus, OpenTelemetry Collector", parent="d-ops")
for k in (1, 2, 3):
    rel("x%d" % k, "i-spread", "i-gw%d" % k, "About a third of the client connections", "Postgres wire protocol, TCP")
for k in (1, 2, 3):
    rel("x%d" % (k + 3), "i-gw%d" % k, "i-pg", "At most budget / 3 pooled connections; dials at most rate / 3", "Postgres wire protocol, TCP")
for k in (1, 2, 3):
    rel("x%d" % (k + 6), "i-tel", "i-gw%d" % k, "Scrapes this instance's snapshot", "HTTP admin port")


# --------------------------------------------------------------------------- diagrams
def diagram(id, kind, title, file, shows, intro, nodes, rels, scope=None, parent=None, direction="TB",
            detail=None, boundary=None, groups=None, env=None):
    D.append(dict(id=id, kind=kind, title=title, file=file, shows=shows, intro=intro, nodes=nodes, rels=rels,
                  scope=scope, parent=parent, direction=direction, detail=detail or {}, boundary=boundary,
                  groups=groups, env=env))


diagram("context", "context", "System context", "01-system-context", scope="gw",
        shows="mini-gateway, its clients, its operator, the one Postgres behind it, the readers of its metrics, and its one external library",
        intro="Clients speak the Postgres wire protocol to mini-gateway as if it were the database. The proxy holds the "
              "only connections to Postgres. Metrics leave through exporters; nothing inside the proxy depends on who reads them.",
        nodes=["app", "operator", "developer", "gw", "pg", "telemetry", "pgproto3"],
        rels=["r-app-gw", "r-op-gw", "r-dev-gw", "r-gw-pg", "r-tel-gw", "r-gw-tel", "r-gw-lib"],
        detail={"gw": "container"})

diagram("container", "container", "Containers: the request path", "02-container-request-path", scope="gw", parent="context",
        shows="The Go packages inside one mini-gateway process and how a request passes through them",
        intro="Containers are Go packages inside one process. The shell does all socket I/O and makes no decisions. The "
              "engine is one single-threaded state machine that calls the wire layer and the five pure cores. "
              "Observability hangs off the side: the engine publishes snapshots, the admin listener serves them.",
        nodes=["app", "operator", "c-shell", "c-engine", "c-pgwire", "c-adm", "c-coal", "c-pool", "c-batch", "c-retry",
               "c-obs", "c-config", "pg", "telemetry"],
        rels=["c%d" % i for i in range(1, 16)], boundary="mini-gateway process",
        detail={"c-engine": "component-engine", "c-pgwire": "component-wire", "c-adm": "component-admission",
                "c-pool": "component-pool", "c-coal": "component-coalescer", "c-batch": "component-batcher",
                "c-retry": "component-retry", "c-obs": "component-observability"})

diagram("container-seams", "container", "Containers: drivers and seams", "03-container-drivers-and-seams", scope="gw", parent="context",
        shows="Who drives the engine (the shell or the simulator), where the Postgres codec boundary is, and the work vocabulary the cores are written against",
        intro="The verdict on the backend interface, drawn. There is no generic backend interface. There are three seams: "
              "the engine's byte transport (implemented by the shell with TCP and by the simulator), the wire layer "
              "(the only product package that imports pgproto3), and the work vocabulary (the only thing the cores see). "
              "A second protocol would be a sibling of the wire layer producing the same vocabulary.",
        nodes=["developer", "c-sim", "c-e2e", "c-shell", "c-engine", "c-pgwire", "c-work", "c-adm", "c-pool", "c-coal",
               "c-batch", "c-retry", "pgproto3", "pg"],
        rels=["c3", "s1", "c4", "s2", "s3", "s4", "s5", "s6", "s7", "s8", "s9", "s10", "s11", "s12", "s13", "s14", "s15", "c10"],
        boundary="mini-gateway repository",
        detail={"c-sim": "component-simulator", "c-engine": "component-engine", "c-pgwire": "component-wire"})

diagram("deployment", "deployment", "Deployment: three instances, one database", "04-deployment-fleet", parent="container",
        shows="The supported multi-instance deployment: static fleet config, per-instance enforcement, no run-time coordination",
        intro="Each instance enforces its own limits. The config declares the instance count and divides the fleet "
              "budgets. What multiplies, weakens or survives at N instances is the table in PLAN.md section 8.2; the "
              "weakened rows are measured in three-instance scenarios.",
        nodes=["d-clients", "i-spread", "d-tier", "i-gw1", "i-gw2", "i-gw3", "d-db", "i-pg", "d-ops", "i-tel"],
        rels=["x%d" % i for i in range(1, 10)], env=ENV, direction="LR",
        groups=[("d-clients", ["i-spread"]), ("d-tier", ["i-gw1", "i-gw2", "i-gw3"]), ("d-db", ["i-pg"]), ("d-ops", ["i-tel"])])


def component_diagram(id, title, file, scope, shows, intro, extra_nodes, rels):
    own = [e["id"] for e in E.values() if e["kind"] == "component" and e.get("parentId") == scope]
    diagram(id, "component", title, file, scope=scope, parent="container", shows=shows, intro=intro,
            nodes=own + extra_nodes, rels=rels, boundary=E[scope]["name"])


component_diagram("component-engine", "Components: engine", "05-component-engine", "c-engine",
                  "internal/proxy: the transport seam, the two connection tables, the request pipeline and its stage seams, the reply relay",
                  "The engine is where the seams meet. The transport seam below it has two implementations. The pipeline "
                  "above calls each core through a small stage interface, so a core can be replaced by a pass-through "
                  "(which is how each scenario's ablation run is made).",
                  ["n-shell", "n-sim", "n-pgwire", "n-adm", "n-coal", "n-pool", "n-batch", "n-retry", "n-obs", "n-work"],
                  ["e%d" % i for i in range(1, 22)])
component_diagram("component-wire", "Components: wire layer", "06-component-wire-layer", "c-pgwire",
                  "internal/pgwire: the pgproto3 codec boundary, our framer, both session machines, the classifier, the error contract",
                  "pgproto3 gives message structs and nothing else. The framer, the session machines, SCRAM, transaction "
                  "tracking, classification and error mapping are ours. Everything here is a pure state machine that "
                  "accepts bytes in any fragments.",
                  ["n-engine", "n-work", "pgproto3"], ["w%d" % i for i in range(1, 17)])
component_diagram("component-admission", "Components: admission", "07-component-admission", "c-adm",
                  "internal/admission: the rate gate, per-tenant queues, both CoDel modes behind one interface, the dispatcher, the adaptive limit",
                  "Order of decisions: gate, then queue, then dispatch under the limit. The two queue rules are equal, "
                  "selectable implementations of one interface. Two control loops read two different signals: the queue "
                  "rule reads queue wait, the adaptive limit reads database round-trip time.",
                  ["n-engine", "n-work"], ["a%d" % i for i in range(1, 16)])
component_diagram("component-pool", "Components: pool", "08-component-pool", "c-pool",
                  "internal/pool: the hard cap, the creation limiter, leases per transaction, the breaker, the dirty-connection table",
                  "A request never waits inside the pool: the dispatcher releases it only when a lease is available. "
                  "The pool's job is the cap, the creation rate, and never lending a connection that is not idle and clean.",
                  ["n-engine", "n-work"], ["p%d" % i for i in range(1, 13)])
component_diagram("component-coalescer", "Components: coalescer", "09-component-coalescer", "c-coal",
                  "internal/coalesce: the join rule, the flight table, waiters, the flight's own deadline, the shared reply",
                  "Followers take no queue slot and no connection. The leader passes through admission like any request.",
                  ["n-engine", "n-work"], ["f%d" % i for i in range(1, 12)])
component_diagram("component-batcher", "Components: batcher", "10-component-batcher", "c-batch",
                  "internal/batch: the take rule, eligibility, reply splitting, the poison rule",
                  "Batching here means writing several independent single-statement requests to one free connection in "
                  "one write. It exists only where the classifier can prove it is safe, and it is off by default.",
                  ["n-engine", "n-work"], ["b%d" % i for i in range(1, 7)])
component_diagram("component-retry", "Components: retry policy", "11-component-retry", "c-retry",
                  "internal/retry: the policy interface, its two policies, the budget",
                  "The default policy is Off. Failures after anything reached the database are never retried under any policy.",
                  ["n-engine", "n-work"], ["t%d" % i for i in range(1, 8)])
component_diagram("component-simulator", "Components: simulator harness", "12-component-simulator", "c-sim",
                  "internal/sim and its sub-packages: the loop, the clock, simulated byte streams, the fake Postgres, simulated clients, faults, the trace, invariants, replay",
                  "The harness owns the world around the engine. It implements the engine's transport seam, so the engine "
                  "under test is the engine that ships. Several engines can run on one loop, which is how the "
                  "three-instance scenarios are made.",
                  ["developer", "n-engine", "n-pgwire", "pgproto3"], ["m%d" % i for i in range(1, 21)])
component_diagram("component-observability", "Components: observability", "13-component-observability", "c-obs",
                  "internal/obs: snapshot conventions, the histogram, the collector, health, the exporters, the fleet merge",
                  "One direction of dependency: components fill plain structs; the collector assembles them; exporters "
                  "read only the collector. Adding an exporter touches no component.",
                  ["n-engine", "n-shell", "telemetry"], ["o%d" % i for i in range(1, 15)])


# --------------------------------------------------------------------------- output
def legend(kind, node_ids):
    entries = []
    kinds = {E[n]["kind"] for n in node_ids}
    ext = any("external" in E[n]["tags"] for n in node_ids)
    if "person" in kinds:
        entries.append({"id": "person", "label": "Person or client", "description": "A user or client of the system",
                        "swatch": {"shape": "person", "tone": "default", "stroke": "solid"}})
    if kind == "context":
        entries.append({"id": "accent", "label": "System in focus", "description": "The scope of this view",
                        "swatch": {"shape": "box", "tone": "accent", "stroke": "solid"}})
    if kind == "deployment":
        entries.append({"id": "boundary", "label": "Deployment node", "description": "A host or group of hosts",
                        "swatch": {"shape": "boundary", "tone": "default", "stroke": "solid"}})
    entries.append({"id": "box", "label": "Element", "description": "A system, container, component or instance",
                    "swatch": {"shape": "box", "tone": "default", "stroke": "solid"}})
    if ext:
        entries.append({"id": "external", "label": "External or neighbouring element",
                        "description": "Outside the scope of this view",
                        "swatch": {"shape": "box", "tone": "external", "stroke": "solid"}})
    entries.append({"id": "planned", "label": "Planned", "description": "Target architecture: not built yet",
                    "swatch": {"shape": "box", "tone": "default", "stroke": "dashed"}})
    entries.append({"id": "edge", "label": "Relationship", "description": "The arrow points to the supplier",
                    "swatch": {"shape": "line", "tone": "default", "stroke": "solid", "arrow": "forward"}})
    return {"title": "Key", "entries": entries}


def model():
    diagrams = []
    for d in D:
        nodes = []
        for n in d["nodes"]:
            node = {"elementId": n}
            if n in d["detail"]:
                node["detail"] = {"diagramId": d["detail"][n]}
            nodes.append(node)
        dj = {"id": d["id"], "kind": d["kind"], "title": d["title"], "status": "planned", "nodes": nodes,
              "edges": [{"relationshipId": r} for r in d["rels"]], "legend": legend(d["kind"], d["nodes"]),
              "docIds": [], "sourceIds": SRC, "layout": {"mode": "auto", "direction": d["direction"]}}
        if d["scope"]:
            dj["scopeId"] = d["scope"]
        if d["parent"]:
            dj["parent"] = {"diagramId": d["parent"]}
        if d["env"]:
            dj["environment"] = d["env"]
        diagrams.append(dj)
    return {"schemaVersion": 1, "id": "mini-gateway", "title": "mini-gateway — target architecture",
            "revision": REVISION, "defaultDiagramId": "context",
            "model": {"elements": list(E.values()), "relationships": list(R.values())},
            "diagrams": diagrams, "documents": [],
            "sources": [{"id": "plan", "title": "mini-gateway PLAN.md (Phase 1 proposal)",
                         "url": "https://github.com/nickstrad/mini-gateway/blob/main/PLAN.md",
                         "notes": "Target architecture. Every element is planned; nothing is built yet."}]}


def mid(x):
    return re.sub(r"[^A-Za-z0-9]", "_", x).upper()


def mlabel(e):
    name = e["name"].replace('"', "'")
    sub = ""
    m = re.match(r"((?:cmd|internal|test)/[\w/.-]+)", e["description"])
    if m and e["kind"] == "container":
        sub = "<br/>" + m.group(1).rstrip(".")
    return name + sub


def mnode(n):
    e = E[n]
    lab = mlabel(e)
    if e["kind"] == "person":
        return '%s(["%s"])' % (mid(n), lab)
    if n == "pg" or n == "i-pg":
        return '%s[("%s")]' % (mid(n), lab)
    if "external" in e["tags"] and "neighbour" not in e["tags"]:
        return '%s[["%s"]]' % (mid(n), lab)
    return '%s["%s"]' % (mid(n), lab)


def mermaid(d):
    out = ["flowchart %s" % d["direction"]]
    inside = []
    if d["kind"] == "deployment":
        grouped = set()
        for g, members in d["groups"]:
            out.append('  subgraph %s["%s"]' % (mid(g), E[g]["name"]))
            for m in members:
                out.append("    " + mnode(m))
                grouped.add(m)
            out.append("  end")
    else:
        for n in d["nodes"]:
            e = E[n]
            if d["boundary"] and e.get("parentId") in (d["scope"],) and "neighbour" not in e["tags"]:
                inside.append(n)
        for n in d["nodes"]:
            if n not in inside:
                out.append("  " + mnode(n))
        if inside:
            out.append('  subgraph SCOPE["%s"]' % d["boundary"])
            for n in inside:
                out.append("    " + mnode(n))
            out.append("  end")
    for i, r in enumerate(d["rels"], 1):
        x = R[r]
        arrow = "-.->" if r in IMPL else "-->"
        out.append("  %s %s|%d| %s" % (mid(x["sourceId"]), arrow, i, mid(x["targetId"])))
    return "\n".join(out) + "\n"


LEVEL = {"context": "Level 1 — System context", "container": "Level 2 — Containers",
         "component": "Level 3 — Components", "deployment": "Deployment"}


def page(d, n):
    mm = mermaid(d)
    (HERE / "diagrams").mkdir(exist_ok=True)
    (HERE / "diagrams" / (d["file"] + ".mmd")).write_text(mm)
    L = ["# C4 %s: %s" % (LEVEL[d["kind"]].split(" — ")[0] if d["kind"] != "deployment" else "Deployment", d["title"]), ""]
    L += ["**Target architecture (planned). Nothing in this diagram is built yet.** Model: diagram `%s` in "
          "[model.json](model.json). Source of this page: [gen.py](gen.py)." % d["id"], "", d["intro"], ""]
    if any(r in IMPL for r in d["rels"]):
        L += ["A dashed arrow means \"implements\"; a solid arrow means \"uses\" and points to the supplier.", ""]
    L += ["```mermaid", mm.rstrip("\n"), "```", ""]
    L += ["## Elements", "", "| Element | Kind | What it is |", "| --- | --- | --- |"]
    for nid in d["nodes"]:
        e = E[nid]
        kind = e["kind"]
        if "neighbour" in e["tags"]:
            kind = "neighbouring container"
            desc = E["c-" + nid[2:]]["description"]
        else:
            desc = e["description"]
            if "external" in e["tags"]:
                kind += ", external"
            if "test" in e["tags"]:
                kind += ", test code"
        L.append("| %s | %s | %s |" % (e["name"], kind, desc.replace("|", "\\|")))
    L += ["", "## Relationships", "", "| # | From | To | What | How |", "| --- | --- | --- | --- | --- |"]
    for i, r in enumerate(d["rels"], 1):
        x = R[r]
        L.append("| %d | %s | %s | %s | %s |" % (i, E[x["sourceId"]]["name"], E[x["targetId"]]["name"],
                                                x["label"].replace("|", "\\|"), x.get("technology", "")))
    L.append("")
    (HERE / (d["file"] + ".md")).write_text("\n".join(L))


def main():
    for d in D:
        for n in d["nodes"]:
            assert n in E, (d["id"], n)
        ids = set(d["nodes"])
        for r in d["rels"]:
            assert R[r]["sourceId"] in ids and R[r]["targetId"] in ids, (d["id"], r)
    used = {r for d in D for r in d["rels"]}
    assert used == set(R), sorted(set(R) - used)
    (HERE / "model.json").write_text(json.dumps(model(), indent=1, ensure_ascii=False) + "\n")
    rows = ["| # | Level | File | Shows |", "| --- | --- | --- | --- |"]
    for n, d in enumerate(D, 1):
        page(d, n)
        rows.append("| %d | %s | [%s.md](%s.md) | %s |" % (n, LEVEL[d["kind"]].split(" — ")[-1], d["file"], d["file"], d["shows"]))
    readme = HERE / "README.md"
    text = readme.read_text()
    a, b = "<!-- diagram-table:start -->", "<!-- diagram-table:end -->"
    text = text[:text.index(a) + len(a)] + "\n" + "\n".join(rows) + "\n" + text[text.index(b):]
    readme.write_text(text)
    print("%d elements, %d relationships, %d diagrams" % (len(E), len(R), len(D)))


if __name__ == "__main__":
    main()
