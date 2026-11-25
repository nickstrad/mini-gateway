#!/usr/bin/env python3
"""Source of the mini-gateway C4 set. Edit the tables in this file, then run:

    python3 docs/c4/gen.py

It writes docs/c4/model.json (a c4-sdk bundle, schemaVersion 1), one Markdown page per
diagram (Mermaid picture, element table, numbered relationship table), the Mermaid
sources under docs/c4/diagrams/, and the diagram table in docs/c4/README.md between its
markers. Standard library only. Validation steps are in docs/c4/README.md.

The tables describe the TARGET architecture of PLAN.md. What is already built is said in ONE
place, the AS BUILT block just below; at a chunk landing that block is the only edit
(how: docs/knowledge/c4-as-built.md).
"""
import json
import re
from pathlib import Path

# =========================================================================== AS BUILT
# The only part of this file to edit at a chunk landing. Statuses are the c4-sdk's:
# "existing" (drawn solid) for what is listed in BUILT, "planned" (drawn dashed) for the rest.
#
# LANDED    the last chunk that has landed (PLAN.md section 13). Chunk 0 built no element.
# BUILT     element id -> the chunk that built it. List an element only when ALL of its
#           description is true of the code. Never list a neighbour twin (id prefix "n-"):
#           it follows its container. A container is listed only when every one of its
#           components is.
# PARTIAL   element id -> one plain sentence saying what exists so far. The element stays
#           "planned" and the sentence becomes its plannedNotes entry. A container with a
#           built component must be here (or in BUILT); gen.py refuses otherwise.
# REL_BUILT relationship id -> chunk number, or None. Leave it empty unless the rule is
#           wrong for one arrow. The rule: a relationship is built when both its ends are.
#           A chunk number forces "built", None forces "planned".
LANDED = 3

BUILT = {
    # chunk 1 — simulator kernel (diagram 11)
    "developer": 1,   # can run mgsim, the sweep and the fuzz scripts
    "s-loop": 1,      # internal/sim: sim.go, queue.go, errors.go
    "s-clock": 1,     # internal/sim: time.go, Timer in sim.go
    "s-streams": 1,   # internal/sim: stream.go
    "s-trace": 1,     # internal/sim: trace.go
    "s-faults": 1,    # internal/sim: fault.go
    "s-inv": 1,       # internal/sim: RegisterInvariant, RegisterQuiesce in sim.go
    "s-net": 1,       # internal/sim/simnet
    "s-simtest": 1,   # internal/sim/simtest
    "s-toy": 1,       # internal/sim/simtoy
    "s-cli": 1,       # cmd/mgsim (chunk 2 added world.go: the world scenarios and their result table)
    # chunk 2 — simulated world (diagram 11)
    "s-world": 2,     # internal/sim/world; tier R tests in tierr_test.go, run by scripts/tier-r.sh
    "s-randdist": 2,  # internal/randdist
    "pgproto3": 2,    # in go.mod (github.com/jackc/pgx/v5); imported by fakepg/proto.go and testclient/msg.go
    # chunk 1 — measurement half of internal/obs (diagram 12)
    "o-hist": 1,      # internal/obs: histogram.go
    "o-conserve": 1,  # internal/obs: snapshot.go
    "o-obstest": 1,   # internal/obs/obstest (chunk 3 added conformance.go and golden.go)
    # chunk 3 — observability spine (diagram 12)
    "o-contract": 3,  # internal/obs: Snapshotter in snapshot.go (chunk 1); the import rules of chunk 3 in tools/lint-rules/imports.txt
    "o-registry": 3,  # internal/obs: metric.go
    "o-instance": 3,  # internal/obs: instance.go
    "o-detail": 3,    # internal/obs: detail.go, Config.Detail and the emitter in collector.go
    "o-collector": 3, # internal/obs: collector.go, self.go
    "o-adapter": 3,   # internal/obs: Adapter in collector.go; the conformance test in obstest/conformance.go
    "o-merge": 3,     # internal/obs: merge.go
    "o-prom": 3,      # internal/obs/promtext (promtext.go; the checker and decoder in check.go)
    "o-json": 3,      # internal/obs/jsonstats
    "o-csv": 3,       # internal/obs/csvflush (schema.go, writer.go, reader.go)
    "o-log": 3,       # internal/obs/logline
    "o-simsrc": 3,    # internal/obs/simsrc
    "o-catalog": 3,   # internal/obs/catalog; scripts/gen-docs.sh, scripts/gen-docs.d/c03-csv-schema.sh; docs/csv-schema.md
    "o-simrun": 3,    # internal/obs/simrun
    "o-assert": 3,    # scripts/csv-assert.sh, test/analysis (views.sql, guard.sql, five assertion files); make csv-assert
    "csvfiles": 3,    # written by internal/obs/csvflush; described in docs/csv-schema.md
    "duckdb": 3,      # the command-line tool, pinned to 1.5.x in scripts/csv-assert.sh
}

PARTIAL = {
    "c-sim": "Built in chunks 1 and 2: the kernel (internal/sim), simnet, simtest, the toy protocol (simtoy), cmd/mgsim, "
             "the fake Postgres, the simulated clients, the test client, the scenario world with its five direct "
             "baselines (clients straight at the fake, no proxy) and the fidelity runner, and scripts/tier-r.sh. "
             "Not built: SCRAM login and the TLS faults in the fake and the clients (chunk 4). No engine exists yet, so "
             "the harness drives the fake Postgres and the toy protocol, and nothing implements the engine's transport seam.",
    "s-fakepg": "Built in chunk 2 (internal/sim/fakepg): all of it with trust login, and the 24 database-side fault kinds "
                "that need no TLS. Not built: SCRAM-SHA-256 login, and the two TLS faults (answers N to a TLS request; "
                "a certificate that fails verification) (chunk 4).",
    "s-workload": "Built in chunk 2 (internal/sim/workload): all of it, with the client-side faults that need no TLS "
                  "(half a message, stops reading, disconnects, reconnect burst, cancel, aggressive retry). Not built: "
                  "the four TLS client faults (stalls after a TLS request, fails the handshake, plaintext after a TLS "
                  "request, a second TLS request) (chunk 4).",
    "s-testclient": "Built in chunk 2 (internal/testclient): the framer, the codec and the session machine, with trust "
                    "login. Not built: SCRAM-SHA-256 login (chunk 4).",
    "c-obs": "Built in chunks 1 and 3: everything except the OTLP push adapter (chunk 15) and the health rules that "
             "read other components (chunk 6 on). No engine or shell exists yet, so only the simulator's scenario "
             "harness (internal/obs/simrun) creates a collector, and nothing serves the outputs over HTTP.",
    "o-health": "Built in chunk 3 (internal/obs: health.go): the three states, the rule table (overall state is the "
                "worst rule), the collector rule and the JSON body. Not built: the rules for shedding, an open "
                "breaker, the limit's floor, draining and a database outage, and any rule that gives failed "
                "(chunk 6 on).",
}

REL_BUILT = {
    # Arrows that are wholly true of the code although one end is only partly built (PARTIAL):
    # the fake Postgres, the clients and the test client lack only their SCRAM and TLS parts.
    "m7": 2,    # workload dials and reads over simnet
    "m8": 2,    # fakepg listens on simnet
    "m10": 2,   # fakepg registers its fault kinds (fakepg/fault.go)
    "m11": 2,   # workload registers its fault kinds (workload/fault.go)
    "m12": 2,   # workload streams: arrivals, keys, mix, retry (workload/client.go)
    "m13": 2,   # fakepg streams: fakepg/service/<conn>, fakepg/keys
    "m14": 2,   # fakepg/proto.go imports pgproto3
    "m15": 2,   # testclient/msg.go imports pgproto3
    "m29": 2,   # workload/client.go and probe.go use testclient
    "m32": 2,   # world/driver.go starts fakepg
    "m33": 2,   # world/driver.go starts the workload
    "m34": 2,   # world/fidelity.go reads testclient messages
    "m36": 2,   # world/tierr_test.go, run by scripts/tier-r.sh (Postgres itself stays planned: nothing sits in front of it yet)
    "m38": 2,   # fakepg trace kinds and state writer
    "m39": 2,   # workload trace kinds and state writer
    "m40": 2,   # fakepg invariant and quiesce check (fakepg/server.go)
    "m41": 2,   # workload invariant and quiesce check (workload/workload.go)
    # Container arrows of the partly built simulator harness that are wholly true of the code:
    "s9": 2,    # the pgproto3 requirement entered go.mod in chunk 2
    "s13": 1,   # cmd/mgsim and go test run scenarios from a seed since chunk 1
    "s16": 2,   # scripts/tier-r.sh
    # Diagram 12 arrows that are wholly true of the code although one end is only partly built:
    "o4": 3,    # Collector.Health evaluates a HealthTable (obs/health.go)
    "o22": 2,   # simtoy, fakepg and workload tests call obstest.RequirePlain, MergeMoved, NeverMoved
    "o34": 3,   # simsrc maps sim.Snapshot, simnet.Snapshot, fakepg.Snapshot, workload.Snapshot
    "o40": 3,   # simrun wraps world.SimDriver and calls world.Drive
    "o50": 2,   # obs.Gauge and obs.Conserve in fakepg/snapshot.go and workload/snapshot.go
    "o51": 2,   # obs.Histogram in workload/workload.go; obs.HistogramSnapshot in world/check.go
}
# =========================================================================== end of AS BUILT

HERE = Path(__file__).resolve().parent
REVISION = "target-approved-2026-11-24-rulings+built-through-chunk-%d" % LANDED
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
       "connection pool lent per transaction, per-tenant admission with two CoDel modes, read coalescing, an "
       "off-by-default retry policy, snapshot observability. Terminates client TLS; logs clients in with "
       "SCRAM-SHA-256. Runs as N independent instances.",
       tech="Go; one external package (pgproto3)")
system("pg", "Postgres",
       "One ordinary Postgres server. Sees at most the fleet's connection budget, all logged in as one role.",
       tech="PostgreSQL 17", tags=("external",))
system("telemetry", "Metrics and log readers",
       "Everything that reads what the adapters produce: a Prometheus fed through an OpenTelemetry Collector or by "
       "direct scrape, flushed CSV files read by tests and DuckDB, log readers. They merge across instances; no "
       "component knows about them. The observability component view shows them one by one.", tags=("external",))
system("csvfiles", "Flushed CSV files",
       "One directory per run on local disk: run.csv, metrics.csv, histograms.csv, schema version 1 (PLAN.md section "
       "7.7; columns in docs/csv-schema.md). Same columns in every flush; every tenant has its own rows; run.csv "
       "is written last, so a run without it did not finish. Nothing has to be running to read them.",
       tech="CSV files", tags=("external",))
system("duckdb", "DuckDB (analysis tool)",
       "The DuckDB command-line tool (version family 1.5.x), run after a run by scripts/csv-assert.sh and by hand. "
       "Reads the flushed files with read_csv and runs SQL over them. Never imported, linked or started by the proxy.",
       tech="DuckDB CLI, SQL", tags=("external",))
system("otelcol", "OpenTelemetry Collector",
       "Receives the proxy's OTLP/HTTP push, batches it and exposes the metrics on a Prometheus endpoint. Dev setup: "
       "docker-compose in deploy/obs-dev (chunk 15).", tech="otlp receiver, prometheus exporter", tags=("external",))
system("prom", "Prometheus",
       "End of the production path. Scrapes the Collector; sees low-cardinality labels and the bounded tenant label "
       "only. Fleet totals are sums over the instance label.", tech="Prometheus", tags=("external",))
system("pgproto3", "pgproto3 (from pgx/v5)",
       "Go package github.com/jackc/pgx/v5/pgproto3: Postgres wire message structs with Encode and Decode. "
       "The one external dependency, used for message encoding only.", tech="Go library", tags=("external",))

# --------------------------------------------------------------------------- containers
container("c-shell", "Shell and binary",
          "cmd/mini-gateway and internal/shell. Listeners, one reader and one writer goroutine per connection, bounded "
          "mailboxes, a deadline on every socket operation, TLS on both legs with crypto/tls (it answers a client's "
          "TLS request, upgrades the socket and buffers nothing across the upgrade), the admin listener on its own "
          "port, signal handling. "
          "Makes no decisions: it posts events to the engine and performs the actions the engine returns.",
          tech="Go, goroutines, TCP, crypto/tls")
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
          "key, outcome. Carries no Postgres type. This is the extension seam for a second protocol.",
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
container("c-retry", "Retry policy",
          "internal/retry. A policy interface. Off by default; the other policy retries once, only when nothing reached "
          "the database, within a 10% budget.", tech="Go, pure core")
container("c-obs", "Observability",
          "internal/obs and its sub-packages; test/analysis; scripts/csv-assert.sh. Snapshot conventions (the one raw "
          "export), integer histogram, metric registry, collector, the adapter interface and its adapters (Prometheus "
          "text, JSON, CSV flush, log line, OTLP push), fleet merge, health, and the SQL assertions over flushed files.",
          tech="Go, standard library")
container("c-config", "Configuration",
          "internal/config. The config file, the static fleet block (instance count N; fleet budgets divided by N), "
          "and load-time validation that fails closed.", tech="Go")
container("c-sim", "Simulator harness",
          "internal/sim and its sub-packages simnet, fakepg, workload, world, simtest and simtoy; internal/testclient; "
          "internal/randdist; cmd/mgsim; scripts/tier-r.sh. A deterministic single-threaded world: virtual clock, "
          "simulated byte streams, a fake Postgres, simulated clients, scenarios with a driver interface, faults, "
          "replay. Its fake Postgres is checked against a real Postgres 17. Test code; built before any proxy code.",
          tech="Go test harness", tags=("test",))
container("c-e2e", "End-to-end suite and load generator",
          "test/e2e and cmd/loadgen. Each scenario is written once and runs on the simulator (tier S) and on the real "
          "binary against a real Postgres (tier R). The load generator is open-loop.", tech="Go test code", tags=("test",))

# --------------------------------------------------------------------------- context relationships
rel("r-app-gw", "app", "gw", "Connects, logs in, sends statements and transactions", "Postgres wire protocol v3, simple query; TCP, TLS when the client asks for it")
rel("r-op-gw", "operator", "gw", "Configures the fleet; reads health and stats; drains", "Config file, HTTP admin port, SIGTERM")
rel("r-gw-pg", "gw", "pg", "Lends pooled connections per transaction; at most the configured budget", "Postgres wire protocol v3; TCP, TLS by config")
rel("r-tel-gw", "telemetry", "gw", "Scrapes metrics and stats from every instance and merges them", "HTTP: Prometheus text, JSON")
rel("r-gw-tel", "gw", "telemetry", "Pushes metrics to a Collector that feeds Prometheus; flushes CSV files; writes log lines", "OTLP/HTTP JSON, files")
rel("r-dev-gw", "developer", "gw", "Runs simulator scenarios and the test, chaos and benchmark commands", "go test, make, cmd/mgsim")
rel("r-gw-lib", "gw", "pgproto3", "Encodes and decodes wire messages", "Go import, wire layer only")

# --------------------------------------------------------------------------- container view A (request path)
rel("c1", "app", "c-shell", "Connects, logs in, sends statements", "Postgres wire protocol; TCP, TLS when the client asks for it")
rel("c2", "operator", "c-shell", "Reads health, stats and metrics; sends SIGTERM to drain", "HTTP admin port, signal")
rel("c3", "c-shell", "c-engine", "Posts events (bytes arrived, connection opened or closed, clock) and performs the returned actions", "Go, bounded mailboxes")
rel("c4", "c-engine", "c-pgwire", "Turns bytes into work units and outcomes; builds replies the proxy makes itself", "Go, in-process")
rel("c5", "c-engine", "c-adm", "Gate, enqueue, pick the next request, report completions", "Go, in-process")
rel("c6", "c-engine", "c-coal", "Join or start a shared read; fan the reply out", "Go, in-process")
rel("c7", "c-engine", "c-pool", "Lend and release connections; open and close decisions; breaker state", "Go, in-process")
rel("c9", "c-engine", "c-retry", "Ask whether a failure may be retried", "Go, in-process")
rel("c10", "c-shell", "pg", "Opens the pooled connections and moves their bytes", "Postgres wire protocol; TCP, TLS by config")
rel("c11", "c-engine", "c-obs", "Publishes one instance snapshot per interval", "Go, plain structs")
rel("c12", "c-shell", "c-obs", "Admin listener serves exporters and health from the last published snapshot", "Go, in-process")
rel("c13", "telemetry", "c-shell", "Scrapes Prometheus text and JSON stats", "HTTP admin port")
rel("c14", "c-obs", "telemetry", "Pushes OTLP metrics to a Collector that feeds Prometheus (last build chunk); flushes CSV files; writes log lines", "OTLP/HTTP JSON, files")
rel("c15", "c-shell", "c-config", "Loads and validates the config and the fleet block at start", "Go, in-process")

# --------------------------------------------------------------------------- container view B (drivers and seams)
rel("s1", "c-sim", "c-engine", "Drives the same engine on its event loop, over simulated byte streams", "Go, transport seam")
rel("s2", "c-pgwire", "pgproto3", "The codec boundary: message structs encoded and decoded here and nowhere else in product code", "Go import")
rel("s3", "c-pgwire", "c-work", "Produces work units and outcomes", "Go types")
rel("s4", "c-adm", "c-work", "Written against these types only", "Go types")
rel("s5", "c-pool", "c-work", "Written against these types only", "Go types")
rel("s6", "c-coal", "c-work", "Written against these types only", "Go types")
rel("s8", "c-retry", "c-work", "Written against these types only", "Go types")
rel("s9", "c-sim", "pgproto3", "The fake Postgres and the test client encode and decode real wire messages, in one file each", "Go import, test code")
rel("s10", "c-e2e", "c-sim", "Tier S: runs each scenario on the simulator", "Go test")
rel("s11", "c-e2e", "c-shell", "Tier R: runs the real binary and drives it with test clients and open-loop load", "Process, Postgres wire protocol")
rel("s12", "c-e2e", "pg", "Tier R: starts a real Postgres 17 container and checks its state", "Docker, Postgres wire protocol")
rel("s13", "developer", "c-sim", "Runs a scenario from a seed; replays a failure", "cmd/mgsim, go test")
rel("s14", "developer", "c-e2e", "make test, make chaos, make bench", "make")
rel("s16", "c-sim", "pg", "Tier R of the simulated world: the fidelity script and three direct baselines run against a real Postgres 17, to show the fake is faithful", "scripts/tier-r.sh, Docker, TCP")
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
     "Client-facing. Runs the exchange before startup: answers a TLS request yes or no by config and asks the shell to "
     "upgrade the connection, answers a GSS encryption request no. Reads the startup message, finds the tenant by "
     "user, runs SCRAM-SHA-256 (server side; trust only as a named test setting), sends the parameter set and proxy-issued key data, recognises cancel "
     "requests, takes in one query at a time, handles terminate.")
comp(P, "w-backend", "Database session machine",
     "Database-facing. Asks for TLS first when the config requires it, then startup, SCRAM-SHA-256 (client side), collects server parameters and key data, reaches ready.")
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
     "shareable with its key. Conservative: what it cannot tokenise is never shared.")
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
     "internal/sim. One thread, one heap ordered by (time, sequence). An event is a closure, or an opaque handler "
     "with an integer code. Step, run until a time, run until quiet. A step budget, typed stall errors (budget "
     "spent, time frozen, heap full) and a panic guard, each carrying the REPLAY line.")
comp(P, "s-clock", "Virtual clock and timers",
     "internal/sim. 64-bit nanoseconds that saturate instead of overflowing. Cancellable timers: a cancel removes "
     "the event from the heap. A timer marked background does not count in the quiesce check. Time moves only "
     "when the loop moves it.")
comp(P, "s-streams", "Named random streams",
     "internal/sim. One generator per (seed, label), so a new draw in one place does not shift any other.")
comp(P, "s-net", "Simulated byte streams",
     "internal/sim/simnet. Listeners and ordered byte connections with chosen fragmentation (down to one byte), "
     "latency per direction, a bounded send window (back pressure), half-close and reset. Its faults: reset, "
     "stall, half-open, half-close, chunk, refuse, dial stall, accept-then-silence, and clear. Its own snapshot.")
comp(P, "s-fakepg", "Fake Postgres",
     "internal/sim/fakepg. Speaks the simple-query subset in real wire bytes with its own framer; login by trust or "
     "SCRAM-SHA-256; a max_connections limit with Postgres's refusal; transaction status; a small keyed table with "
     "write, statement and transaction logs; service-time models and a capacity limit; cancel requests; every "
     "database-side fault; its own snapshot.",
     tech="Go, pgproto3")
comp(P, "s-workload", "Clients and tenants",
     "internal/sim/workload. Simulated clients grouped in tenants: open-loop arrivals (constant or Poisson) and "
     "closed-loop think time, latency measured from the intended send time, uniform, Zipf or fixed keys, "
     "transactions of several statements, retries with backoff, every client-side fault, its own snapshot. A "
     "client sends one request at a time; later arrivals wait in its bounded backlog. Also a hand-driven client "
     "(Probe) for tests.")
comp(P, "s-testclient", "Test client",
     "internal/testclient. A Postgres client with no I/O: bytes in, messages out. Its own framer, a message codec, "
     "and a session machine (phase, status byte, key data, parameters) with login by trust or SCRAM-SHA-256. Used "
     "by the simulated clients on simulated streams and by the tier R tests on TCP.", tech="Go, pgproto3")
comp(P, "s-randdist", "Float generators",
     "internal/randdist. The only two functions that use floating point: exponential gaps for Poisson arrivals, "
     "and a Zipf generator. Kept outside internal/sim, where float syntax is forbidden.")
comp(P, "s-world", "Scenario world",
     "internal/sim/world. The scenario type; the driver interface (start, connect, inject, run, observe, stop) and "
     "its simulator implementation; five direct baselines with no proxy (500 connections, reconnect burst, 1,000 "
     "identical reads, open-loop overload, mixed transactions); the write-log and transaction checks; the "
     "fidelity runner, which plays one script against the fake and compares the transcript with Postgres 17's, "
     "recorded for CI or live in tier R.")
comp(P, "s-faults", "Fault schedule and shrinker",
     "internal/sim. Timed faults, each recorded in the trace when it is applied. A package registers the fault "
     "kinds it can apply. A schedule can be decoded from bytes for the fuzzer, and a delete-one-fault shrinker "
     "cuts a failing schedule down. Faults are injected only from outside the code under test.")
comp(P, "s-trace", "Decision trace and digests",
     "internal/sim. Any actor appends a record (time, kind, four integers) in order; the last records are kept in "
     "a ring for failure reports. Two digests: one of the trace, one of final state as written by registered "
     "state writers. Same seed, same digests.")
comp(P, "s-inv", "Invariants and quiesce check",
     "internal/sim. A package registers its checks. Invariants run after every event (or every Nth); the quiesce "
     "checks run at the end of a run: nothing in use, nothing in flight, no timer left. A failed check fails "
     "the run.")
comp(P, "s-simtest", "Seeds, replay, sweep",
     "internal/sim/simtest. The seed flag and variables, pinned seeds, the sweep over seeds, the run-twice check, "
     "the failure report with the trace tail and the REPLAY line. scripts/sweep.sh and scripts/fuzz.sh run the "
     "scenario tests and fuzz targets through it.")
comp(P, "s-cli", "cmd/mgsim",
     "Lists the named scenarios (toy and world); runs one from a seed outside go test, prints its digests (and the "
     "trace on request; for a world scenario also its result table) and exits 1 with the REPLAY line when it fails.",
     tech="Go CLI")
comp(P, "s-toy", "Toy echo protocol",
     "internal/sim/simtoy. A line-echo client and server with a planted-bug switch, and eleven named scenarios "
     "that cover every network fault. Used only to test the harness itself.")
rel("m1", "developer", "s-cli", "Runs a scenario with a seed", "Command line")
rel("m2", "s-cli", "s-toy", "Looks up a toy scenario by name and runs it", "Go")
rel("m3", "s-simtest", "s-loop", "Runs a scenario twice from one seed", "Go")
rel("m4", "s-loop", "s-clock", "Advances time to the next event", "Go")
rel("m5", "s-net", "s-loop", "Schedules every delivery and notification as its own event", "Go")
rel("m6", "s-net", "n-engine", "Implements the engine's transport seam: bytes in, bytes out, opened, closed", "Go", "implementation")
rel("m7", "s-workload", "s-net", "Opens client connections; sends and reads", "Go")
rel("m8", "s-fakepg", "s-net", "Accepts database connections; replies", "Go")
rel("m9", "s-faults", "s-net", "Applies the network faults at their instants", "Go")
rel("m10", "s-faults", "s-fakepg", "Applies the database fault kinds the fake registered: refuse, silence, slow, reset, garbage, kill, freeze, restart and more", "Go")
rel("m11", "s-faults", "s-workload", "Applies the client fault kinds the workload registered: half a message, stop reading, disconnect, reconnect burst, cancel", "Go")
rel("m12", "s-workload", "s-streams", "Arrivals, keys, request mix and retries, one stream each per client", "Go")
rel("m13", "s-fakepg", "s-streams", "Service-time jitter, one stream per connection; cancel keys", "Go")
rel("m14", "s-fakepg", "pgproto3", "Wire messages", "Go import")
rel("m15", "s-testclient", "pgproto3", "Wire messages", "Go import")
rel("m16", "s-fakepg", "n-pgwire", "Reuses the SCRAM functions", "Go")
rel("m29", "s-workload", "s-testclient", "Every client frames, decodes and tracks its session with it", "Go")
rel("m30", "s-workload", "s-randdist", "Poisson gaps and Zipf keys", "Go")
rel("m31", "s-testclient", "n-pgwire", "Reuses the SCRAM functions", "Go")
rel("m32", "s-world", "s-fakepg", "Starts the fake for a scenario; reads its snapshot and logs for the checks", "Go")
rel("m33", "s-world", "s-workload", "Starts the scenario's clients; reads their snapshot; the fidelity runner drives a Probe", "Go")
rel("m34", "s-world", "s-testclient", "Turns the messages of a fidelity exchange into transcript tokens", "Go")
rel("m35", "s-world", "s-simtest", "Its scenario tests run the pinned seeds and the sweep, every seed twice", "Go test")
rel("m36", "s-world", "pg", "Tier R: plays the fidelity script and three baselines over TCP; on request rewrites the recorded transcript that CI replays", "scripts/tier-r.sh, Docker, go test")
rel("m37", "s-cli", "s-world", "Looks up a world scenario by name, runs it and prints its result table", "Go")
rel("m38", "s-fakepg", "s-trace", "Appends accepts, statements, writes, cancels and fired faults; writes its state for the state digest", "Go")
rel("m39", "s-workload", "s-trace", "Appends arrivals, sends, completions and retries; writes its state for the state digest", "Go")
rel("m40", "s-fakepg", "s-inv", "Registers its conservation laws, the connection limit and its quiesce check", "Go")
rel("m41", "s-workload", "s-inv", "Registers request conservation per tenant and its quiesce check", "Go")
rel("m42", "s-world", "s-faults", "Scenario fault schedules; random schedules decoded from fuzz input", "Go")
rel("m17", "n-engine", "s-trace", "Appends each decision", "Go")
rel("m18", "s-inv", "n-engine", "Reads snapshots at every event and at the end", "Go")
rel("m19", "s-simtest", "s-trace", "Compares the digests of the two runs; prints the trace tail on failure", "Go")
rel("m20", "s-toy", "s-net", "Echo over simulated streams, for the meta-tests", "Go")
rel("m21", "s-net", "s-streams", "Latency and fragment sizes, one stream per connection and direction", "Go")
rel("m22", "s-net", "s-trace", "Appends dials, writes, deliveries, resets and closes; writes its state for the state digest", "Go")
rel("m23", "s-net", "s-inv", "Registers byte conservation and its quiesce check", "Go")
rel("m24", "s-loop", "s-inv", "Runs the registered checks after every event", "Go")
rel("m25", "s-toy", "s-faults", "Scenario fault schedules; schedules decoded from fuzz input; shrinks a failing one", "Go")
rel("m26", "s-toy", "s-simtest", "Its scenario tests sweep seeds and run every seed twice", "Go test")
rel("m27", "developer", "s-simtest", "Runs seed sweeps and fuzz targets", "scripts/sweep.sh, scripts/fuzz.sh, go test")
rel("m28", "s-cli", "s-trace", "Prints the digests and, on request, every trace record", "Go")

# --------------------------------------------------------------------------- observability components
P = "c-obs"
comp(P, "o-contract", "Snapshot conventions",
     "The one common raw export. What every component's Snapshot() returns: a plain struct of monotonic counters and "
     "current gauges. No wall-clock time, no map order, no telemetry imports. Nothing else leaves a component; an "
     "import rule keeps components and the simulator from importing any adapter.")
comp(P, "o-hist", "Integer histogram",
     "Fixed array, fixed relative error, no allocation on record; record, merge, percentile. Out-of-range values are "
     "clamped and counted.")
comp(P, "o-conserve", "Conservation check and gauge",
     "A check that a total equals the sum of its parts (for example written = delivered + discarded + in flight); "
     "its error names each term and which side is higher. A gauge type that keeps a value and its peak.")
comp(P, "o-obstest", "Snapshot and adapter test helpers",
     "internal/obs/obstest. Test plumbing: checks that a snapshot is a plain struct of integers (no time, no map), "
     "and that every counter in it moved in at least one test. Also the conformance test every adapter must pass "
     "(values, labels, determinism, no kept reference, errors counted), its shared fixture, and the golden-file "
     "helper.", tags=("test",))
comp(P, "o-registry", "Metric registry",
     "Every exported metric is declared first: component, name, kind (counter, gauge, histogram), which of the four "
     "labels it has (tenant, outcome, reason, mode), unit, merge rule (sum, or max for a high-water mark), and "
     "whether it is a headline metric. Refuses a bad or repeated name and names that would clash in Prometheus.")
comp(P, "o-instance", "Instance snapshot",
     "What the collector publishes and all an adapter gets: instance id, build, config fingerprint, publish "
     "sequence number, and sorted lists of points (integer values) and histogram points. No time, no map.")
comp(P, "o-detail", "Detail gate",
     "The obs.detail setting. Off: the collector folds every tenant into one and drops histogram points, and a "
     "detail histogram holds no buckets (a record is one nil check). On: per-tenant rows and full histograms.")
comp(P, "o-collector", "Collector",
     "Components register as sources: their metric declarations, their Snapshot and a mapping from it to points. "
     "At each publish it assembles one instance snapshot (with instance id, build and config fingerprint), refusing "
     "an undeclared, mistyped, duplicate or missing metric, and hands it to every attached adapter. Keeps the last "
     "published snapshot and gives out copies. Counts its own work (publishes, emit errors, export errors per "
     "adapter, CSV flushes and drops) as component obs. The only path from a component snapshot to an adapter.")
comp(P, "o-adapter", "Adapter interface",
     "The one interface every output form implements: a name, and take the published instance snapshot and produce "
     "output. A failed export is counted by the collector and never reaches the publisher. One conformance test for "
     "all adapters. Components cannot see past it, so none knows about CSV, DuckDB, OTel or Prometheus.")
comp(P, "o-merge", "Fleet merge",
     "Merges instance snapshots: each metric by its declared rule (sum, or max for a high-water mark), histograms "
     "bucket by bucket. Refuses an undeclared metric, a kind or unit clash and an integer overflow. Ratios are "
     "never stored, so they are recomputed from the merged counts, never averaged.")
comp(P, "o-health", "Health",
     "Three states, the worst over a table of named rules, with a reason per rule and a JSON body. The collector "
     "rule: degraded before the first publish, and while the latest publish had metric errors or an adapter's export "
     "failed. Other rules: degraded while shedding, while the breaker is open, at the limit's floor, or draining; "
     "failed only when this instance cannot work. A database outage is degraded.")
comp(P, "o-prom", "Prometheus text exporter",
     "internal/obs/promtext. Names mg_<component>_<metric>, low-cardinality labels and a bounded tenant label: "
     "configured tenants, or the first active ones, up to a cap; quiet tenants lose their slot; the rest are summed "
     "as other. Histograms are re-bucketed onto a short bound list. Holds the exposition-grammar checker the tests use.")
comp(P, "o-json", "JSON stats exporter",
     "internal/obs/jsonstats. The full snapshot, including every active tenant; it decodes back to an equal snapshot.")
comp(P, "o-csv", "CSV flush adapter",
     "internal/obs/csvflush. Flushes snapshots to disk at each publish and at the end of a run, in a stable versioned "
     "schema with unbounded per-tenant rows. The testing path: tests assert on the files with no Collector or "
     "Prometheus running. Bounded size; drop-and-count at the cap, on a write error and on a full queue. Writes in "
     "the caller (simulator and tests) or through a bounded queue that never blocks the caller (real process). "
     "Holds the reader and the round-trip check: the final flush read back equals the snapshot.")
comp(P, "o-log", "Log-line exporter",
     "internal/obs/logline. One structured line per publish: sequence number, instance, build, config fingerprint "
     "and the headline metrics. At most 1,024 bytes.")
comp(P, "o-otlp", "OTLP/HTTP push exporter",
     "The final adapter and the proxy's end of the production path. Hand-written on the standard library, no SDK. "
     "Cumulative sums, gauges, short-list histogram buckets, bounded tenant label. Bounded queue, timeout, "
     "drop-and-count when the Collector is down. Lands in the last build chunk and touches no component.")
comp(P, "o-simsrc", "Simulator source mappings",
     "internal/obs/simsrc. The metric declarations and the snapshot-to-points mappings of the four simulator "
     "components that exist: the kernel, simnet, the fake Postgres and the workload. It lives here because the "
     "simulator packages are frozen; a later component writes the same two functions in its own package.",
     tags=("test",))
comp(P, "o-catalog", "Metric catalog and schema document",
     "internal/obs/catalog. The registry of every source the product registers, and the generator of "
     "docs/csv-schema.md (columns of the three files and every metric). Its test fails when the committed document "
     "differs from the code; scripts/gen-docs.sh regenerates or checks it.")
comp(P, "o-simrun", "Scenario flush harness",
     "internal/obs/simrun. Runs a simulator world scenario with a collector that publishes on the virtual clock and "
     "with the CSV, JSON, Prometheus and log adapters attached, then checks every output against the final "
     "snapshot. Its tests run the five direct baselines twice per seed and compare the files byte for byte.",
     tags=("test",))
comp(P, "o-assert", "SQL assertions and their runner",
     "scripts/csv-assert.sh and test/analysis. The runner finds run directories, checks them with guard.sql (schema "
     "version, no empty required field, one run id per directory, no empty run), loads views.sql and runs each "
     "assertion file; a returned row is a violation. Fails closed and never skips. The first assertion files check "
     "the direct baselines' counts.", tech="Bash, SQL", tags=("test",))
rel("o1", "n-engine", "o-collector", "Registers each component as a source and publishes once per interval", "Go, plain structs")
rel("o2", "o-collector", "o-contract", "Calls every source's Snapshot and mapping at each publish", "Go")
rel("o3", "o-contract", "o-hist", "Histograms inside snapshots", "Go")
rel("o4", "o-collector", "o-health", "Evaluates the rule table on its last snapshot", "Go")
rel("o5", "n-shell", "o-prom", "Admin listener serves /metrics", "HTTP")
rel("o6", "n-shell", "o-json", "Admin listener serves /stats", "HTTP")
rel("o7", "n-shell", "o-health", "Admin listener serves /health", "HTTP")
rel("o8", "o-prom", "o-adapter", "Implements the adapter interface", "Go", "implementation")
rel("o9", "o-json", "o-adapter", "Implements the adapter interface", "Go", "implementation")
rel("o10", "o-csv", "o-adapter", "Implements the adapter interface", "Go", "implementation")
rel("o11", "o-log", "o-adapter", "Implements the adapter interface", "Go", "implementation")
rel("o12", "o-otlp", "o-adapter", "Implements the adapter interface", "Go", "implementation")
rel("o13", "o-otlp", "otelcol", "Pushes metrics every interval", "OTLP/HTTP JSON")
rel("o14", "o-merge", "o-instance", "Merges the snapshots of several instances into one", "Go")
rel("o15", "o-adapter", "o-collector", "Is attached to it and receives each published snapshot; all an adapter can read", "Go")
rel("o16", "o-csv", "csvfiles", "Flushes rows at each publish and at the end of a run", "Files, schema version 1")
rel("o17", "duckdb", "csvfiles", "Reads the files directly", "read_csv, SQL")
rel("o18", "n-e2e", "o-csv", "Turns the flush on for every run; checks the final flush equals the snapshot", "Go test")
rel("o19", "n-e2e", "o-assert", "Runs the SQL assertions after a run", "scripts/csv-assert.sh")
rel("o20", "prom", "otelcol", "Scrapes the Collector's Prometheus endpoint", "HTTP")
rel("o21", "prom", "n-shell", "Can scrape /metrics directly instead (no Collector)", "HTTP admin port")
rel("o22", "n-sim", "o-obstest", "Scenario tests check that snapshots are plain and that every counter moves", "Go test")
rel("o23", "o-obstest", "o-contract", "Checks a snapshot against the conventions", "Go test")
rel("o24", "o-contract", "o-conserve", "Counters in a snapshot are checked for conservation; gauges keep their peak", "Go")
rel("o25", "o-collector", "o-instance", "Assembles, sorts and publishes one; keeps the last", "Go")
rel("o26", "o-collector", "o-registry", "Checks every emitted point against its declaration", "Go")
rel("o27", "o-collector", "o-detail", "Folds tenants and drops histogram points when detail is off", "Go")
rel("o28", "o-detail", "o-hist", "A detail histogram wraps one when detail is on", "Go")
rel("o29", "o-instance", "o-hist", "Histogram points carry its bucket lists", "Go")
rel("o31", "o-prom", "o-registry", "Reads each metric's kind, unit and help text", "Go")
rel("o32", "o-log", "o-registry", "Reads which metrics are headline", "Go")
rel("o33", "o-merge", "o-registry", "Reads each metric's merge rule", "Go")
rel("o34", "o-simsrc", "n-sim", "Reads the snapshots of the kernel, simnet, the fake Postgres and the workload", "Go")
rel("o35", "o-simsrc", "o-collector", "Registers one source per simulator component, with its metric declarations", "Go")
rel("o37", "o-catalog", "o-simsrc", "Lists every metric it declares", "Go")
rel("o38", "o-catalog", "o-csv", "Reads the column lists of the three files", "Go")
rel("o39", "o-catalog", "o-registry", "Builds the full registry, the collector's own metrics included", "Go")
rel("o40", "o-simrun", "n-sim", "Runs a world scenario through the simulator driver; publishes from a timer on the virtual clock", "Go")
rel("o41", "o-simrun", "o-collector", "Creates it, attaches the four adapters, publishes every 100 ms of virtual time and at the end", "Go")
rel("o42", "o-simrun", "o-simsrc", "Takes the sources of the scenario's world", "Go")
rel("o43", "o-simrun", "o-csv", "Checks the final flush read back equals the final snapshot", "Go")
rel("o44", "o-simrun", "o-prom", "Checks the text with the grammar checker", "Go")
rel("o45", "o-simrun", "o-json", "Checks the document decodes to the final snapshot", "Go")
rel("o46", "o-simrun", "o-assert", "Its tests run the runner over the baseline runs and over planted bad runs", "Go test")
rel("o47", "o-assert", "duckdb", "Runs the guard, the views and each assertion file", "DuckDB CLI")
rel("o48", "o-assert", "csvfiles", "Finds the run directories and checks the three headers", "Bash")
rel("o50", "n-sim", "o-conserve", "The fake Postgres and the workload keep gauges with peaks and check conservation", "Go")
rel("o51", "n-sim", "o-hist", "The workload records latencies in it; the world checks read its snapshots", "Go")

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
              "engine is one single-threaded state machine that calls the wire layer and the four pure cores. "
              "Observability hangs off the side: the engine publishes snapshots, the admin listener serves them.",
        nodes=["app", "operator", "c-shell", "c-engine", "c-pgwire", "c-adm", "c-coal", "c-pool", "c-retry",
               "c-obs", "c-config", "pg", "telemetry"],
        rels=["c%d" % i for i in range(1, 16) if i != 8], boundary="mini-gateway process",
        detail={"c-engine": "component-engine", "c-pgwire": "component-wire", "c-adm": "component-admission",
                "c-pool": "component-pool", "c-coal": "component-coalescer",
                "c-retry": "component-retry", "c-obs": "component-observability"})

diagram("container-seams", "container", "Containers: drivers and seams", "03-container-drivers-and-seams", scope="gw", parent="context",
        shows="Who drives the engine (the shell or the simulator), where the Postgres codec boundary is, and the work vocabulary the cores are written against",
        intro="The verdict on the backend interface, drawn. There is no generic backend interface. There are three seams: "
              "the engine's byte transport (implemented by the shell with TCP and by the simulator), the wire layer "
              "(the only product package that imports pgproto3), and the work vocabulary (the only thing the cores see). "
              "A second protocol would be a sibling of the wire layer producing the same vocabulary.",
        nodes=["developer", "c-sim", "c-e2e", "c-shell", "c-engine", "c-pgwire", "c-work", "c-adm", "c-pool", "c-coal",
               "c-retry", "pgproto3", "pg"],
        rels=["c3", "s1", "c4", "s2", "s3", "s4", "s5", "s6", "s8", "s9", "s10", "s11", "s12", "s13", "s14", "s15", "c10", "s16"],
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
                  ["n-shell", "n-sim", "n-pgwire", "n-adm", "n-coal", "n-pool", "n-retry", "n-obs", "n-work"],
                  ["e%d" % i for i in range(1, 22) if i != 11])
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
component_diagram("component-retry", "Components: retry policy", "10-component-retry", "c-retry",
                  "internal/retry: the policy interface, its two policies, the budget",
                  "The default policy is Off. Failures after anything reached the database are never retried under any policy.",
                  ["n-engine", "n-work"], ["t%d" % i for i in range(1, 8)])
component_diagram("component-simulator", "Components: simulator harness", "11-component-simulator", "c-sim",
                  "internal/sim and its sub-packages, internal/testclient and internal/randdist: the loop, the clock, simulated byte streams, the fake Postgres, simulated clients, the test client, the scenario world with its baselines and fidelity runner, faults and the shrinker, the trace, invariants and the quiesce check, replay, the toy protocol",
                  "The harness owns the world around the engine. It implements the engine's transport seam, so the engine "
                  "under test is the engine that ships. Several engines can run on one loop, which is how the "
                  "three-instance scenarios are made. The kernel came first (chunk 1) and is proved on a toy echo "
                  "protocol. Chunk 2 added the world the proxy will stand in: the fake Postgres, the simulated clients "
                  "and scenarios that run the clients straight at the fake. No engine exists yet, so those direct "
                  "baselines are the control arms for later chunks.",
                  ["developer", "n-engine", "n-pgwire", "pgproto3", "pg"], ["m%d" % i for i in range(1, 43)])
component_diagram("component-observability", "Components: observability", "12-component-observability", "c-obs",
                  "internal/obs: the one raw export (snapshots), the metric registry, the collector, the adapter interface and its five adapters, and where each output goes: the CSV flush read by tests and DuckDB, and OTLP to a Collector to Prometheus",
                  "The hard rule: one common raw export, then the collector interface, then adapters. Components fill "
                  "plain structs; the collector assembles them; every output form is an adapter that reads only what the "
                  "collector publishes. No component knows about CSV, DuckDB, OTel or Prometheus, and adding an adapter "
                  "touches no component. Two paths leave the process. The testing path: the CSV flush adapter writes "
                  "files, and tests and DuckDB read them with nothing else running. The production path: the OTLP "
                  "adapter pushes to an OpenTelemetry Collector, which Prometheus scrapes. DuckDB is an analysis tool "
                  "over files, never a dependency of the proxy. Chunk 3 built the spine before the proxy's "
                  "components: so far the sources are the simulator's kernel, network, fake Postgres and workload, "
                  "and the scenario flush harness is what publishes.",
                  ["n-engine", "n-shell", "n-e2e", "n-sim", "csvfiles", "duckdb", "otelcol", "prom"],
                  ["o%d" % i for i in (1, 2, 25, 26, 27, 3, 24, 28, 29, 4, 15, 8, 9, 10, 11, 12, 31, 32, 5, 6, 7, 16, 18, 17, 19,
                                         13, 20, 21, 14, 33, 22, 23, 50, 51, 34, 35, 37, 38, 39, 40, 41, 42, 43, 44, 45, 46, 47, 48)])


# --------------------------------------------------------------------------- output
def apply_built():
    """Turns the AS BUILT block into statuses and plannedNotes. Fails on a claim that cannot be right."""
    assert not set(BUILT) & set(PARTIAL), sorted(set(BUILT) & set(PARTIAL))
    for i in list(BUILT) + list(PARTIAL):
        assert i in E, "AS BUILT names an unknown element: %s" % i
        assert "neighbour" not in E[i]["tags"], "list the container, not its twin: %s" % i
    for i, chunk in BUILT.items():
        assert isinstance(chunk, int) and 0 <= chunk <= LANDED, "BUILT[%s] is not a landed chunk" % i
        E[i]["status"] = "existing"
    for i, note in PARTIAL.items():
        E[i]["plannedNotes"] = [note]
    for i, e in E.items():
        if e["kind"] == "component" and i in BUILT:
            assert e["parentId"] in BUILT or e["parentId"] in PARTIAL, \
                "%s is built, so its container %s must be in BUILT or PARTIAL" % (i, e["parentId"])
        if e["kind"] == "container" and i in BUILT:
            missing = [c for c, x in E.items() if x["kind"] == "component" and x.get("parentId") == i and c not in BUILT]
            assert not missing, "%s is in BUILT but these components are not: %s" % (i, missing)
    for i, e in E.items():
        if "neighbour" in e["tags"]:
            real = E["c-" + i[2:]]
            e["status"], e["plannedNotes"] = real["status"], list(real["plannedNotes"])
    for i, v in REL_BUILT.items():
        assert i in R, "REL_BUILT names an unknown relationship: %s" % i
        assert v is None or (isinstance(v, int) and 0 <= v <= LANDED), "REL_BUILT[%s] is not a landed chunk" % i
    for i, r in R.items():
        built = REL_BUILT[i] is not None if i in REL_BUILT else built_el(r["sourceId"]) and built_el(r["targetId"])
        r["status"] = "existing" if built else "planned"


def built_el(i):
    return E[i]["status"] == "existing"


def built_rel(i):
    return R[i]["status"] == "existing"


def built_line():
    if LANDED == 0:
        return "Built so far: chunk 0, which built no part of these diagrams. Dashed parts are planned."
    return "Built so far: chunks 0–%d. Dashed parts are planned." % LANDED


def counts(d):
    return (sum(built_el(n) for n in d["nodes"]), len(d["nodes"]),
            sum(built_rel(r) for r in d["rels"]), len(d["rels"]))


def diagram_status(d):
    be, ne, br, nr = counts(d)
    if be + br == 0:
        return "planned"
    return "existing" if (be, br) == (ne, nr) else "mixed"


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
    if ext:
        entries.append({"id": "external", "label": "External or neighbouring element",
                        "description": "Outside the scope of this view",
                        "swatch": {"shape": "box", "tone": "external", "stroke": "solid"}})
    entries.append({"id": "built", "label": "Built", "description": "Solid outline: exists in the code",
                    "swatch": {"shape": "box", "tone": "default", "stroke": "solid"}})
    entries.append({"id": "planned", "label": "Planned", "description": "Dashed outline: target architecture, not built yet",
                    "swatch": {"shape": "box", "tone": "default", "stroke": "dashed"}})
    entries.append({"id": "edge", "label": "Relationship, built", "description": "Solid line. The arrow points to the supplier",
                    "swatch": {"shape": "line", "tone": "default", "stroke": "solid", "arrow": "forward"}})
    entries.append({"id": "edge-planned", "label": "Relationship, planned", "description": "Dashed line. The arrow points to the supplier",
                    "swatch": {"shape": "line", "tone": "default", "stroke": "dashed", "arrow": "forward"}})
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
        dj = {"id": d["id"], "kind": d["kind"], "title": d["title"], "status": diagram_status(d), "nodes": nodes,
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
            "sources": [{"id": "plan", "title": "mini-gateway PLAN.md (approved 2026-11-24)",
                         "url": "https://github.com/nickstrad/mini-gateway/blob/main/PLAN.md",
                         "notes": "Target architecture. " + built_line() + " Status existing means the element is in the "
                                  "code; status planned means it is not, or not all of it (see its plannedNotes)."}]}


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
    cls = ":::built" if built_el(n) else ":::planned"
    if e["kind"] == "person":
        return '%s(["%s"])%s' % (mid(n), lab, cls)
    if n == "pg" or n == "i-pg":
        return '%s[("%s")]%s' % (mid(n), lab, cls)
    if "external" in e["tags"] and "neighbour" not in e["tags"]:
        return '%s[["%s"]]%s' % (mid(n), lab, cls)
    return '%s["%s"]%s' % (mid(n), lab, cls)


DASH = "stroke-dasharray:6 4"


def mermaid(d):
    out = ["flowchart %s" % d["direction"]]
    inside = []
    dashed_groups = []
    if d["kind"] == "deployment":
        grouped = set()
        for g, members in d["groups"]:
            out.append('  subgraph %s["%s"]' % (mid(g), E[g]["name"]))
            for m in members:
                out.append("    " + mnode(m))
                grouped.add(m)
            out.append("  end")
            if not built_el(g):
                dashed_groups.append(mid(g))
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
            if not built_el(d["scope"]):
                dashed_groups.append("SCOPE")
    for i, r in enumerate(d["rels"], 1):
        x = R[r]
        arrow = "-->" if built_rel(r) else "-.->"
        label = "%d implements" % i if r in IMPL else "%d" % i
        out.append("  %s %s|%s| %s" % (mid(x["sourceId"]), arrow, label, mid(x["targetId"])))
    out.append('  subgraph KEY["Key"]')
    out.append('    KEY_BUILT["Built"]:::built')
    out.append('    KEY_PLANNED["Planned"]:::planned')
    out.append("  end")
    out.append("  classDef built fill:#d9ecd9,stroke:#2e6b34,stroke-width:2px,color:#1c1e21")
    out.append("  classDef planned stroke-width:1px,%s" % DASH)
    for g in dashed_groups:
        out.append("  style %s %s" % (g, DASH))
    return "\n".join(out) + "\n"


LEVEL = {"context": "Level 1 — System context", "container": "Level 2 — Containers",
         "component": "Level 3 — Components", "deployment": "Deployment"}


def page(d, n):
    mm = mermaid(d)
    (HERE / "diagrams").mkdir(exist_ok=True)
    (HERE / "diagrams" / (d["file"] + ".mmd")).write_text(mm)
    L = ["# C4 %s: %s" % (LEVEL[d["kind"]].split(" — ")[0] if d["kind"] != "deployment" else "Deployment", d["title"]), ""]
    be, ne, br, nr = counts(d)
    L += ["**%s** In this diagram %d of %d elements and %d of %d relationships are built. Model: diagram `%s` in "
          "[model.json](model.json). Source of this page: [gen.py](gen.py)." % (built_line(), be, ne, br, nr, d["id"]),
          "", d["intro"], ""]
    key = "A solid box or arrow is built; a dashed one is planned. An arrow points to the supplier."
    if any(r in IMPL for r in d["rels"]):
        key += " An arrow marked \"implements\" means the source implements the target's interface; every other arrow means \"uses\"."
    L += [key, ""]
    L += ["```mermaid", mm.rstrip("\n"), "```", ""]
    L += ["## Elements", "", "| Element | Kind | Status | What it is |", "| --- | --- | --- | --- |"]
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
        real = "c-" + nid[2:] if "neighbour" in e["tags"] else nid
        status = "built (chunk %d)" % BUILT[real] if real in BUILT else "planned"
        if e["plannedNotes"]:
            desc += " **So far:** " + " ".join(e["plannedNotes"])
        L.append("| %s | %s | %s | %s |" % (e["name"], kind, status, desc.replace("|", "\\|")))
    L += ["", "## Relationships", "", "| # | From | To | Status | What | How |", "| --- | --- | --- | --- | --- | --- |"]
    for i, r in enumerate(d["rels"], 1):
        x = R[r]
        L.append("| %d | %s | %s | %s | %s | %s |" % (i, E[x["sourceId"]]["name"], E[x["targetId"]]["name"],
                                                     "built" if built_rel(r) else "planned",
                                                     x["label"].replace("|", "\\|"), x.get("technology", "")))
    L.append("")
    (HERE / (d["file"] + ".md")).write_text("\n".join(L))


def main():
    apply_built()
    for d in D:
        for n in d["nodes"]:
            assert n in E, (d["id"], n)
        ids = set(d["nodes"])
        for r in d["rels"]:
            assert R[r]["sourceId"] in ids and R[r]["targetId"] in ids, (d["id"], r)
    used = {r for d in D for r in d["rels"]}
    assert used == set(R), sorted(set(R) - used)
    (HERE / "model.json").write_text(json.dumps(model(), indent=1, ensure_ascii=False) + "\n")
    rows = ["**%s** The Built column counts the elements of each diagram that exist in the code." % built_line(), "",
            "| # | Level | File | Built | Shows |", "| --- | --- | --- | --- | --- |"]
    for n, d in enumerate(D, 1):
        page(d, n)
        rows.append("| %d | %s | [%s.md](%s.md) | %d of %d | %s |" % (n, LEVEL[d["kind"]].split(" — ")[-1], d["file"], d["file"],
                                                                 counts(d)[0], counts(d)[1], d["shows"]))
    readme = HERE / "README.md"
    text = readme.read_text()
    a, b = "<!-- diagram-table:start -->", "<!-- diagram-table:end -->"
    text = text[:text.index(a) + len(a)] + "\n" + "\n".join(rows) + "\n" + text[text.index(b):]
    readme.write_text(text)
    print("%d elements (%d built), %d relationships (%d built), %d diagrams; %s"
          % (len(E), sum(built_el(i) for i in E), len(R), sum(built_rel(i) for i in R), len(D), built_line()))


if __name__ == "__main__":
    main()
