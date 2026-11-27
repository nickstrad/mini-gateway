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
LANDED = 6

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
    # chunks 2 and 4 — simulated world (diagram 11)
    "s-fakepg": 4,    # internal/sim/fakepg (chunk 2); chunk 4 added SCRAM, the TLS answer and the simulated handshake in auth.go
    "s-workload": 4,  # internal/sim/workload (chunk 2); chunk 4 added SCRAM, the TLS request and the four TLS faults in startup.go
    "s-testclient": 4,  # internal/testclient (chunk 2); chunk 4 added Msg.Data in msg.go, which the workload's SCRAM reads
    "s-simtls": 4,    # internal/sim/simtls
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
    "o-simsrc": 3,    # internal/obs/simsrc (chunk 4 added the fake's authentication counters and the clients' startup counters)
    "o-catalog": 3,   # internal/obs/catalog; scripts/gen-docs.sh, scripts/gen-docs.d/c03-csv-schema.sh; docs/csv-schema.md (chunk 5 added the classifier source, chunk 6 the engine and shell metrics)
    "o-simrun": 6,    # internal/obs/simrun (chunk 3); chunk 4 added wire.go (the wire scenarios), chunk 5 classifier.go (the classifier scenarios), chunk 6 custom.go (a run handed its sources)
    "o-assert": 6,    # scripts/csv-assert.sh, test/analysis (views.sql, guard.sql, 195 assertion files: 5 of chunk 3, 57 of chunk 4, 3 of chunk 5, 130 of chunk 6); make csv-assert; scripts/chaos.sh
    "o-wiresrc": 4,   # internal/obs/wiresrc
    "o-clssrc": 5,    # internal/obs/clssrc
    "csvfiles": 3,    # written by internal/obs/csvflush; described in docs/csv-schema.md
    "duckdb": 3,      # the command-line tool, pinned to 1.5.x in scripts/csv-assert.sh
    # chunk 4 — wire sessions (diagram 6)
    "w-framer": 4,    # internal/pgwire: frame.go
    "w-msgs": 4,      # internal/pgwire: frame_encode.go, params.go
    "w-client": 4,    # internal/pgwire: client.go, prestartup.go, client_tenants.go
    "w-backend": 4,   # internal/pgwire: backend.go
    "w-scram": 4,     # internal/pgwire: scram.go, scram_server.go, scram_client.go
    "w-cycle": 4,     # internal/pgwire: CycleTracker in cycle.go
    "w-front": 4,     # internal/pgwire: FrontendTracker in cycle.go
    "w-fatal": 4,     # internal/pgwire: fatal.go
    "w-snapshot": 4,  # internal/pgwire: snapshot_wire.go
    "w-harness": 4,   # internal/pgwire/pgwiretest (proxy.go, fidelity.go, fidelity_sim.go); recording in testdata/startup-postgres17.txt
    "w-world": 4,     # internal/pgwire/pgwiretest/wireworld; run by internal/pgwire/client_sim_test.go and internal/obs/simrun/wire_test.go
    # chunk 5 — statement classifier and the contracts (diagrams 3 and 6)
    "c-work": 5,      # internal/work: work.go
    "w-lexer": 5,     # internal/pgwire: lex.go
    "w-rejects": 5,   # internal/pgwire: rejectlist.go, rejectlist_doc.go; docs/reject-list.md
    "w-classifier": 5,  # internal/pgwire: classify.go, snapshot_classifier.go
    "w-share": 5,     # internal/pgwire: share.go, share_doc.go; docs/safe-shape.md; tier R test in share_tierr_test.go
    "w-errors": 5,    # internal/pgwire: errors.go, errors_doc.go, reply.go; docs/error-contract.md
    "w-clsworld": 5,  # internal/pgwire/pgwiretest/clsworld; run by internal/pgwire/classify_e2e_test.go and internal/obs/simrun/classifier_test.go
    # chunk 6 — walking skeleton (diagrams 1, 2, 3, 5 and 12)
    "app": 6,         # a client connects, logs in and runs statements through cmd/mini-gateway (test/e2e tier R: psql, pgbench-style runs)
    "operator": 6,    # the config file (internal/config), /healthz, /stats and /metrics (internal/shell/admin.go), SIGTERM (cmd/mini-gateway/main.go)
    "pg": 6,          # the proxy now stands in front of it: tier R of test/e2e runs the binary against Postgres 17
    "c-shell": 6,     # cmd/mini-gateway (main.go); internal/shell (shell.go, engine.go, conn.go, io.go, admin.go, stats.go)
    "e-transport": 6, # internal/proxy: api.go (events, actions, the read constants)
    "e-sessions": 6,  # internal/proxy: client.go
    "e-backends": 6,  # internal/proxy: dbconn.go
    "e-pipeline": 6,  # internal/proxy: startRequest, gates, enqueue, dispatch, lend, forward in lease.go
    "e-seams": 6,     # internal/proxy: seams.go
    "e-reset": 6,     # internal/proxy: replyDone, release, startReset, resetJudge, judgeReset in lease.go
    "e-fail": 6,      # internal/proxy: leaseFailed, statementLost, reachedFor, lostRow in lease.go; reply in client.go
    "e-clock": 6,     # internal/proxy: timers.go; fireTimers in engine.go
    "e-publish": 6,   # internal/proxy: snapshot.go; Snapshot and the EvPublish case in engine.go
    "e-proxysim": 6,  # internal/proxy/proxysim (proxysim.go, world.go)
    "o-enginesrc": 6, # internal/obs/enginesrc
}

PARTIAL = {
    "gw": "Built in chunk 6: a working proxy, the walking skeleton — cmd/mini-gateway terminates client TLS, logs "
          "clients in with SCRAM-SHA-256, lends one of a fixed set of database connections per transaction, resets it "
          "after every lease, queues waiting requests in one bounded first-come queue, and serves health, stats and "
          "metrics from snapshots. Not built: the pool with its breaker, per-tenant admission with the two CoDel "
          "modes, read coalescing, the retry policy, the OTLP push, and the fleet division and drain of N instances.",
    "c-engine": "Built in chunk 6 (internal/proxy, internal/proxy/proxysim): every component drawn here except two "
                "parts — the fan-out of a shared reply to its waiters (chunk 11) and the drain sequence (chunk 13). "
                "The stages that belong to cores not built yet run their stand-ins: a breaker that is always closed, "
                "a rate gate that always allows, no coalescing, one bounded first-come queue, a fixed set of "
                "connections, retry off.",
    "e-relay": "Built in chunk 6 (internal/proxy: relay, relayEvents and relayed in lease.go; writeClient and the "
               "pauses in client.go): all of it for the one client of a reply. Not built: the fan-out to every waiter "
               "of a shared read (chunk 11).",
    "e-drain": "Built in chunk 6 (internal/proxy: the EvDraining input, gates in lease.go, Shutdown in engine.go): "
               "while the draining input is on, new work from a client outside a transaction is refused with 53M05 "
               "and that client is closed after the reply (unit-tested; nothing in the binary sets it yet); and a plain stop that "
               "closes every client and connection. Not built: the sequence — finish transactions in progress within "
               "a bound, close idle connections, exit (chunk 13).",
    "c-config": "Built in chunk 6 (internal/config): the JSON config file with its defaults, validation that fails "
                "closed and names the key (the timeout nesting too), the client_tls and backend_tls blocks, the "
                "config fingerprint, and the fleet block parsed and checked (pool.connections must fit "
                "fleet.backend_connections / fleet.instances, which must divide evenly and fit the database's limit "
                "with a reserve). Not built: nothing takes its budget from the division yet (chunk 7 on; the fleet "
                "configuration is chunk 13).",
    "c-e2e": "Built in chunk 6 (test/e2e): the scenario harness — a scenario is written once as a Spec and runs in "
             "tier S and tier R; 21 tier S scenarios, 14 tier R tests, the canaries and ablations, the operation fuzz "
             "target FuzzEngineOps, and make chaos (scripts/chaos.sh). Not built: cmd/loadgen, the open-loop load "
             "generator (chunk 6a), and make bench.",
    "c-sim": "Built in chunks 1, 2 and 4: every component drawn here — the kernel (internal/sim), simnet, simtest, the toy "
             "protocol (simtoy), cmd/mgsim, the fake Postgres, the simulated clients, the test client, the simulated TLS "
             "handshake (simtls), the scenario world with its five direct baselines (clients straight at the fake, no "
             "proxy) and the fidelity runner, and scripts/tier-r.sh. Since chunk 6 the engine runs on it, through the "
             "engine's own test package internal/proxy/proxysim. Not built: no synthetic workload drives the cores "
             "(no core exists yet).",
    "c-pgwire": "Built in chunks 4 and 5 (internal/pgwire): every component drawn here except the codec boundary. Chunk 4: "
                "the framer, the hand-written message coding, both session machines, the SCRAM-SHA-256 functions, the "
                "two trackers, the startup FATAL replies, the wire snapshot, and the test packages pgwiretest and "
                "wireworld. Chunk 5: the SQL lexer, the reject list, the statement classifier, the safe-shape "
                "recogniser, the error table with its reply builders, and the test package clsworld. Chunk 6: the "
                "engine drives this code, and the client session machine takes every FATAL from the error table "
                "(three startup rows added). Not built: the "
                "codec boundary (no file in the package imports pgproto3; the reply builders code their messages by "
                "hand). Nothing produces outcomes yet.",
    "c-obs": "Built in chunks 1, 3, 4, 5 and 6: everything except the OTLP push adapter (chunk 15) and the health "
             "rules for shedding, an open breaker, the limit's floor and draining (chunk 7 on). Since chunk 6 the "
             "shell creates a collector in the real process, with the engine, wire and classifier sources, and "
             "serves the outputs over HTTP. The binary attaches the JSON, Prometheus and CSV adapters; the log-line "
             "adapter is attached only by the scenario flush harness.",
    "o-health": "Built in chunks 3 and 6. Chunk 3 (internal/obs: health.go): the three states, the rule table "
                "(overall state is the worst rule), the collector rule and the JSON body. Chunk 6: the rule database "
                "(internal/obs/enginesrc: degraded while no database connection is usable and the last connect "
                "failed) and the rule listener (internal/shell: failed while the client listener is down). Not "
                "built: the rules for shedding, an open breaker, the limit's floor and draining (chunk 7 on).",
}

REL_BUILT = {
    # Arrows that are wholly true of the code although one end is only partly built (PARTIAL).
    "m16": 4,   # fakepg/auth.go and backend.go import internal/pgwire (ScramServer, Verifier, AppendNegotiateProtocolVersion)
    "m43": 4,   # workload/startup.go imports internal/pgwire (ScramClient)
    # Container arrows of the partly built simulator harness that are wholly true of the code:
    "s9": 2,    # the pgproto3 requirement entered go.mod in chunk 2
    "s13": 1,   # cmd/mgsim and go test run scenarios from a seed since chunk 1
    "s16": 2,   # scripts/tier-r.sh
    # Diagram 6 arrows from the wire layer's test packages to the partly built simulator harness:
    "w33": 4,   # pgwiretest/proxy.go and fidelity_sim.go import sim, simnet, simtls, fakepg
    "w35": 4,   # wireworld/wireworld.go imports sim, simnet, fakepg, workload
    "w50": 5,   # clsworld/clsworld.go imports sim, simnet, fakepg, workload and testclient
    # The query-cycle tracker (chunk 4) does not import internal/work: nothing produces outcomes yet.
    "w16": None,
    # Diagram 12 arrows that are wholly true of the code although one end is only partly built:
    "o4": 3,    # Collector.Health evaluates a HealthTable (obs/health.go)
    "o22": 2,   # simtoy, fakepg and workload tests call obstest.RequirePlain, MergeMoved, NeverMoved
    "o34": 3,   # simsrc maps sim.Snapshot, simnet.Snapshot, fakepg.Snapshot and AuthSnapshot, workload.Snapshot
    "o40": 3,   # simrun wraps world.SimDriver and calls world.Drive
    "o50": 2,   # obs.Gauge and obs.Conserve in fakepg/snapshot.go and workload/snapshot.go
    "o51": 2,   # obs.Histogram in workload/workload.go; obs.HistogramSnapshot in world/check.go
    "o52": 4,   # wiresrc/wiresrc.go maps pgwire.WireSnapshot
    "o55": 4,   # simrun/wire.go builds and runs a wireworld.World; simrun/classifier.go a clsworld.World (chunk 5)
    "o57": 5,   # clssrc/clssrc.go maps pgwire.ClassifierSnapshot, ReplySnapshot and SyncDiscardSnapshot
    # Chunk 6. Context arrows of the partly built system that are wholly true of the binary:
    "r-app-gw": 6,  # test/e2e tier R: psql and scripted clients through cmd/mini-gateway, client TLS on
    "r-op-gw": 6,   # internal/config (the file, the fleet block), internal/shell/admin.go, signalContext in cmd/mini-gateway/main.go
    "r-gw-pg": 6,   # the fixed set of pool.connections, lent per transaction (internal/proxy/lease.go)
    "r-tel-gw": 6,  # /metrics and /stats on the admin port (internal/shell/admin.go)
    # Chunk 6. Container arrows with the partly built engine, wire layer, observability, config or suite at one end:
    "c3": 6,    # shell/engine.go: the mailbox, core.Step, execute
    "c4": 6,    # proxy/client.go, dbconn.go and lease.go import internal/pgwire
    "c11": 6,   # proxy/snapshot.go uses obs.Gauge, obs.Histogram and obs.Conserve; the EvPublish case in engine.go
    "c12": 6,   # shell/admin.go: wireObs, publish, the three handlers
    "c13": 6,   # shell/admin.go: /metrics and /stats
    "c15": 6,   # cmd/mini-gateway/main.go: config.Load, then File.Proxy and the TLS configs
    "s1": 6,    # proxysim/proxysim.go imports sim, simnet and simtls; world.go adds fakepg
    "s10": 6,   # test/e2e/simtier.go (RunSim), driver.go (ProxyDriver)
    "s11": 6,   # test/e2e/realtier.go and realenv.go start the binary; realclient.go drives it
    "s12": 6,   # test/e2e/realenv.go starts the container; tierr_test.go checks pg_stat_ssl and the dirty-check query
    "s17": 6,   # test/e2e/simtier.go and opfuzz.go build a proxysim.World; the canaries set proxy.Plant bits
    "s18": 6,   # proxy/seams.go, lease.go and client.go import internal/work
    # Chunk 6. Diagram 5 arrows that are wholly true although the relay, the drain controller or a neighbour is partly built:
    "e5": 6,    # client.go: pgwire.ClientSession, FrontendTracker, SyncDiscard
    "e6": 6,    # dbconn.go: pgwire.BackendSession, CycleTracker
    "e14": 6,   # lease.go: dbData hands reply bytes to relay
    "e15": 6,   # lease.go: relayed calls writeClient
    "e17": 6,   # client.go: reply builds the row's bytes with pgwire.ReplyBuilder
    "e19": 6,   # engine.go: EvDraining sets the input that gates reads
    "e20": 6,   # snapshot.go: obs.Gauge, obs.HistogramSnapshot, obs.Conserve
    "e22": 6,   # proxysim.go imports sim, simnet, simtls; world.go fakepg
    "e27": 6,   # lease.go: startRequest calls the Classifier seam, a *pgwire.Classifier
    "e29": 6,   # lease.go: pgwire.AppendQuery and the pgwire.Framer inside resetJudge
    # Chunk 6. Diagram 6 arrows from the partly built engine to built wire components:
    "w1": 6,    # proxy/client.go: startupData drives pgwire.ClientSession
    "w2": 6,    # proxy/dbconn.go: backendActs drives pgwire.BackendSession
    "w3": 6,    # proxy/lease.go: relay feeds pgwire.CycleTracker
    "w4": 6,    # proxy/lease.go: startRequest
    "w5": 6,    # proxy/client.go: reply; the SyncDiscard rule in front
    "w17": 6,   # proxy/client.go: front feeds pgwire.FrontendTracker
    "w53": 6,   # proxy/lease.go: resetJudge holds a pgwire.Framer
    "w54": 6,   # proxy/engine.go: one pgwire.WireStats handed to both session configs
    "w55": 6,   # proxy/lease.go: pgwire.AppendQuery
    # Chunk 6. Diagram 11 arrows from the partly built engine (its test package proxysim) and suite to built simulator components:
    "m6": 6,    # proxysim.go: accept, dial, readChunk, flush on simnet ends
    "m17": 6,   # proxysim.go: Trace(kindClose, ...) in shut; writeState
    "m18": 6,   # proxysim.go: RegisterInvariant, RegisterQuiesce, RegisterState in New
    "m46": 6,   # proxysim.go: clientHandshake, dbHandshake
    "m47": 6,   # proxysim/world.go: BuildWorld calls fakepg.New
    "m48": 6,   # test/e2e/driver.go: ProxyDriver embeds world.SimDriver
    "m49": 6,   # test/e2e/simtier.go turns on the fake's statement and write logs; checks.go reads them
    "m50": 6,   # test/e2e/opfuzz.go: DecodeOps returns []sim.Fault
    "m51": 6,   # test/e2e/simclient.go and realclient.go import internal/testclient
    # Chunk 6. Diagram 12 arrows with the partly built engine, suite or health element at one end:
    "o7": 6,    # shell/admin.go: serveHealth, Health (the listener rule)
    "o18": 6,   # test/e2e/simtier.go (simrun.Custom); tierr_test.go (csvflush.Read, RoundTrip)
    "o19": 6,   # scripts/chaos.sh runs csv-assert.sh over every scenario of both tiers
    "o61": 6,   # enginesrc.go: Map(proxy.Snapshot), Sources(*proxy.Engine)
    "o66": 6,   # enginesrc.go: HealthRules
    "o69": 6,   # test/e2e/simtier.go: simrun.Custom
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
       "Runs proxy instances, writes the config file with its static fleet block, reads health and stats, stops an "
       "instance with SIGTERM (a plain stop so far; a graceful drain from chunk 13).")
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
          "cmd/mini-gateway and internal/shell. The binary (-config, -check, -verifier) and the goroutine shell: the "
          "client listener, one reader and one writer goroutine per connection, one bounded mailbox into the engine's "
          "goroutine, a bounded write queue per connection, a deadline on every socket operation, TLS on both legs "
          "with crypto/tls (it reads exactly the 8 bytes of a client's first packet, upgrades the socket when the "
          "engine says so and buffers nothing across the upgrade), the admin listener on its own port, the collector "
          "with its JSON, Prometheus and CSV adapters, and signal handling: SIGTERM or SIGINT stops the process (a "
          "plain stop so far; the graceful drain is chunk 13). "
          "Makes no decisions: it posts events to the engine and performs the actions the engine returns.",
          tech="Go, goroutines, TCP, crypto/tls")
container("c-engine", "Engine",
          "internal/proxy. One single-threaded, sans-I/O state machine per instance: events in, actions out. Holds the "
          "client and database connection tables and the request pipeline with its stage seams. The simulator and the "
          "shell drive this same code; its test package proxysim is the simulator's driver.", tech="Go, pure state machine")
container("c-pgwire", "Wire layer",
          "internal/pgwire. Everything that knows Postgres: framing, both session state machines, the query-cycle "
          "tracker, the SQL lexer, the statement classifier with its courtesy reject list, the safe-shape recogniser that "
          "decides sharing, the error contract, the wire snapshot. The only product package allowed to import "
          "pgproto3. Its test packages pgwiretest, wireworld and clsworld run this code in the simulator.",
          tech="Go, pure state machines, pgproto3")
container("c-work", "Work vocabulary",
          "internal/work. Small plain types the cores are written against: tenant, unit of work with its class, reject "
          "reason and sharing flags, share key and its scope, outcome, refusal. Carries no Postgres type and imports "
          "only the standard library. This is the extension seam for a second protocol.",
          tech="Go types")
container("c-adm", "Admission",
          "internal/admission. Rate gate, per-tenant queues with two selectable queue rules, adaptive LIFO, round-robin "
          "dispatcher with shares that bind under saturation, adaptive concurrency limit.", tech="Go, pure core")
container("c-pool", "Pool",
          "internal/pool. Hard-capped database connections, creation limiter, leases per transaction, the one breaker, "
          "the dirty-connection table.", tech="Go, pure core")
container("c-coal", "Coalescer",
          "internal/coalesce. Identical statements share one database read: in auto mode those the wire layer marks as "
          "fitting the safe shape, in hinted mode those carrying the client's mark. Never writes.",
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
          "internal/config. The config file (JSON), the client_tls and backend_tls blocks, the static fleet block "
          "(instance count N; fleet budgets divided by N), load-time validation that fails closed and names the key, "
          "and the config fingerprint.", tech="Go")
container("c-sim", "Simulator harness",
          "internal/sim and its sub-packages simnet, simtls, fakepg, workload, world, simtest and simtoy; internal/testclient; "
          "internal/randdist; cmd/mgsim; scripts/tier-r.sh. A deterministic single-threaded world: virtual clock, "
          "simulated byte streams, a simulated TLS handshake, a fake Postgres, simulated clients, scenarios with a driver interface, faults, "
          "replay. Its fake Postgres is checked against a real Postgres 17. Test code; built before any proxy code.",
          tech="Go test harness", tags=("test",))
container("c-e2e", "End-to-end suite and load generator",
          "test/e2e and cmd/loadgen. Each scenario is written once and runs on the simulator (tier S) and on the real "
          "binary against a real Postgres (tier R), with the CSV flush on and SQL assertions over the files. Holds the "
          "engine's operation fuzz target and the canaries. The load generator is open-loop.", tech="Go test code", tags=("test",))

# --------------------------------------------------------------------------- context relationships
rel("r-app-gw", "app", "gw", "Connects, logs in, sends statements and transactions", "Postgres wire protocol v3, simple query; TCP, TLS when the client asks for it")
rel("r-op-gw", "operator", "gw", "Configures the instance and the fleet block; reads health and stats; stops an instance (a plain stop so far; a graceful drain from chunk 13)", "Config file, HTTP admin port, SIGTERM")
rel("r-gw-pg", "gw", "pg", "Lends pooled connections per transaction; at most the configured budget", "Postgres wire protocol v3; TCP, TLS by config")
rel("r-tel-gw", "telemetry", "gw", "Scrapes metrics and stats from every instance and merges them", "HTTP: Prometheus text, JSON")
rel("r-gw-tel", "gw", "telemetry", "Pushes metrics to a Collector that feeds Prometheus; flushes CSV files; writes log lines", "OTLP/HTTP JSON, files")
rel("r-dev-gw", "developer", "gw", "Runs simulator scenarios and the test, chaos and benchmark commands", "go test, make, cmd/mgsim")
rel("r-gw-lib", "gw", "pgproto3", "Encodes and decodes wire messages", "Go import, wire layer only")

# --------------------------------------------------------------------------- container view A (request path)
rel("c1", "app", "c-shell", "Connects, logs in, sends statements", "Postgres wire protocol; TCP, TLS when the client asks for it")
rel("c2", "operator", "c-shell", "Reads health, stats and metrics; sends SIGTERM to stop (a plain stop so far; a graceful drain from chunk 13)", "HTTP admin port, signal")
rel("c3", "c-shell", "c-engine", "Posts events (bytes arrived, bytes written, connection opened or closed, TLS result, clock) and performs the returned actions", "Go, one bounded mailbox")
rel("c4", "c-engine", "c-pgwire", "Runs the session machines and trackers over the bytes; turns each Query into a work unit; builds the replies the proxy makes itself", "Go, in-process")
rel("c5", "c-engine", "c-adm", "Gate, enqueue, pick the next request, report completions", "Go, in-process")
rel("c6", "c-engine", "c-coal", "Join or start a shared read; fan the reply out", "Go, in-process")
rel("c7", "c-engine", "c-pool", "Lend and release connections; open and close decisions; breaker state", "Go, in-process")
rel("c9", "c-engine", "c-retry", "Ask whether a failure may be retried", "Go, in-process")
rel("c10", "c-shell", "pg", "Opens the pooled connections and moves their bytes", "Postgres wire protocol; TCP, TLS by config")
rel("c11", "c-engine", "c-obs", "Keeps its counters as one plain snapshot (histograms, gauges, a conservation check); answers each publish event with a publish action", "Go, plain structs")
rel("c12", "c-shell", "c-obs", "Creates the collector and publishes once per interval; the admin listener serves exporters and health from the last published snapshot", "Go, in-process")
rel("c13", "telemetry", "c-shell", "Scrapes Prometheus text and JSON stats", "HTTP admin port")
rel("c14", "c-obs", "telemetry", "Pushes OTLP metrics to a Collector that feeds Prometheus (last build chunk); flushes CSV files; writes log lines", "OTLP/HTTP JSON, files")
rel("c15", "c-shell", "c-config", "Loads and validates the config and the fleet block at start", "Go, in-process")

# --------------------------------------------------------------------------- container view B (drivers and seams)
rel("s1", "c-engine", "c-sim", "The engine's test package proxysim runs the same engine on the simulator's event loop, over simulated byte streams", "Go, transport seam, test code")
rel("s2", "c-pgwire", "pgproto3", "The codec boundary: message structs encoded and decoded here and nowhere else in product code", "Go import")
rel("s3", "c-pgwire", "c-work", "Produces work units and outcomes", "Go types")
rel("s4", "c-adm", "c-work", "Written against these types only", "Go types")
rel("s5", "c-pool", "c-work", "Written against these types only", "Go types")
rel("s6", "c-coal", "c-work", "Written against these types only", "Go types")
rel("s8", "c-retry", "c-work", "Written against these types only", "Go types")
rel("s9", "c-sim", "pgproto3", "The fake Postgres and the test client encode and decode real wire messages, in one file each", "Go import, test code")
rel("s10", "c-e2e", "c-sim", "Tier S: runs each scenario on the simulator", "Go test")
rel("s11", "c-e2e", "c-shell", "Tier R: runs the real binary and drives it with test clients (open-loop load from chunk 6a)", "Process, Postgres wire protocol")
rel("s12", "c-e2e", "pg", "Tier R: starts a real Postgres 17 container and checks its state", "Docker, Postgres wire protocol")
rel("s13", "developer", "c-sim", "Runs a scenario from a seed; replays a failure", "cmd/mgsim, go test")
rel("s14", "developer", "c-e2e", "make test, make chaos, make bench", "make")
rel("s16", "c-sim", "pg", "Tier R of the simulated world: the fidelity script and three direct baselines run against a real Postgres 17, to show the fake is faithful", "scripts/tier-r.sh, Docker, TCP")
rel("s15", "c-sim", "c-work", "Synthetic workloads drive the cores directly, with no Postgres code", "Go types, test code")
rel("s17", "c-e2e", "c-engine", "Tier S: puts the engine between simulated clients and the fake Postgres with proxysim; the operation fuzz target and the canaries set the engine's test-only switches", "Go test")
rel("s18", "c-engine", "c-work", "Stage seams are written against these types; work units pass between stages", "Go types")

# --------------------------------------------------------------------------- engine components
P = "c-engine"
comp(P, "e-transport", "Transport seam",
     "The engine's contract with its driver, as plain events and actions (no Go interface). Events in: accepted, "
     "bytes arrived, bytes written, write refused, closed, dial failed, TLS result, timer, publish. Actions out: "
     "write, close, start TLS, pause and resume reading, open a database connection, wake at a time, publish. "
     "Knows no protocol; its read rule (exactly 8 bytes of a new client, one unprocessed read per connection) keeps "
     "bytes from crossing a TLS upgrade. Two implementations: TCP in the shell, simulated streams in proxysim.",
     tech="Go types: events and actions")
comp(P, "e-sessions", "Client session table",
     "One entry per client connection: its wire state machines, tenant, phase (startup, idle, queued, active, "
     "holding a connection), transaction status, the single request it may have inside the proxy, a bounded input "
     "buffer and the count of reply bytes not yet written. Caps the clients connected and the clients in startup.")
comp(P, "e-backends", "Database connection table",
     "One entry per database connection: its wire state machine, the query-cycle tracker, its phase (dialing, "
     "startup, idle, lent, resetting) and who holds it. So far it also keeps the fixed set full itself: opens the "
     "configured number at start, one dial at a time, and dials again after a loss, after a short delay when the "
     "connect failed (the pool decides this from chunk 7).")
comp(P, "e-pipeline", "Request pipeline",
     "The fixed order every request takes: classify, reject list, draining, breaker, rate gate, coalesce, queue, "
     "lease, forward, release. Each stage is counted as passed or refused, and each refusal is answered with its "
     "error-contract row. A queued request has a wait deadline. Statements inside an open transaction skip from "
     "the reject list straight to forward on the connection the client already holds.")
comp(P, "e-seams", "Stage seams and stand-ins",
     "The small interfaces the pipeline calls: classifier, breaker, rate gate, coalescer, queue, pool, release "
     "policy, retry policy. Also the implementations used until each core lands: a breaker that is always closed, a "
     "rate gate that always allows, no coalescing, one bounded first-come queue, a fixed set of connections, reset "
     "after every lease, retry off. The engine's config can replace any of them.", tech="Go interfaces")
comp(P, "e-reset", "Lease end and reset",
     "A lease ends when a reply ends with status idle; while the status is in-transaction or failed the client "
     "keeps its connection. At release the policy answers reset, lend as is, or close (close when the status is not "
     "idle or bytes are left unread). The reset is DISCARD ALL sent as the proxy's own Query: the connection is "
     "lent again only after exactly that tag and status idle, with nothing after them, inside the reset deadline. "
     "Anything else closes it, counted by reason. Nothing of the reset's reply reaches a client.")
comp(P, "e-relay", "Reply relay",
     "Streams database messages to the client (or to every waiter of a shared read) as they arrive, through a "
     "bounded buffer per client, with back pressure in both directions: reading the database connection is paused "
     "while the client is behind, reading the client while its request is in progress. ErrorResponse and "
     "ReadyForQuery are held whole, so a FATAL from the database becomes an error-contract reply. "
     "Never sends a short reply that looks complete.")
comp(P, "e-fail", "Failure classifier",
     "For every failure records whether the request may have reached the database (no only when the driver reports "
     "that its first write was refused with zero bytes accepted) and picks the error-contract row: 08006 for a safe "
     "read or when nothing reached, 08007 otherwise, FATAL 08006 when a transaction lost its connection, and a "
     "close with no message once part of the reply was sent. Hands the fact to the retry seam.")
comp(P, "e-clock", "Deadline table",
     "Every wait the engine has, as deadlines on the clock passed in with each event: one heap, and the next "
     "wake-up time returned as an action. Timers due at or before an event's time fire before the event is "
     "processed, so whatever arrives at the instant of its deadline is late.")
comp(P, "e-drain", "Drain controller",
     "Sequences shutdown: stop admitting, finish transactions in progress within a bound, close idle connections, exit.")
comp(P, "e-publish", "Snapshot publisher",
     "Snapshot(): the engine's counters as one plain struct — clients, requests by stage and outcome, replies by "
     "row, leases, resets, relay, connections, three histograms — with its conservation check, beside the wire, "
     "classifier and reply snapshots the engine holds. On a publish event the engine returns a publish action; its "
     "driver then has the collector read the snapshots, between events.")
comp(P, "e-proxysim", "Simulator driver",
     "internal/proxy/proxysim. The engine on the simulator: accepts and dials on simulated streams, keeps the same "
     "read rule as the shell, plays the simulated TLS handshake on both legs, reports writes and refused writes, "
     "and registers the engine's invariants, quiesce check and state with the simulator. Its World builds the "
     "simulator, the network, the fake Postgres and the engine for a scenario.", tags=("test",))
rel("e1", "n-shell", "e-transport", "Implements it with TCP sockets; posts events through one bounded mailbox", "Go", "implementation")
rel("e2", "e-proxysim", "e-transport", "Implements it with simulated byte streams on the simulator's event loop", "Go", "implementation")
rel("e22", "e-proxysim", "n-sim", "Runs on the simulator's loop, byte streams and simulated TLS handshake; starts the fake Postgres", "Go, test code")
rel("e3", "e-transport", "e-sessions", "Client bytes and connection events", "Go")
rel("e4", "e-transport", "e-backends", "Database bytes and connection events", "Go")
rel("e5", "e-sessions", "n-pgwire", "Client session machine; client query-phase tracker; the discard-until-Sync rule", "Go")
rel("e6", "e-backends", "n-pgwire", "Database session machine; query-cycle tracker", "Go")
rel("e7", "e-sessions", "e-pipeline", "One work unit at a time per client", "Go")
rel("e27", "e-pipeline", "n-pgwire", "Statement classifier: Query text in, work unit out", "Go")
rel("e23", "e-pipeline", "e-seams", "Calls each stage through its interface", "Go")
rel("e8", "e-pipeline", "n-adm", "Gate, queue, dispatch, limit", "Go")
rel("e9", "e-pipeline", "n-coal", "Join or lead a shared read", "Go")
rel("e10", "e-pipeline", "n-pool", "Lend, release, breaker state, dirty connections", "Go")
rel("e12", "e-pipeline", "n-retry", "May this failure be retried", "Go")
rel("e13", "e-pipeline", "e-backends", "Forwards the statement on the lent connection", "Go")
rel("e14", "e-backends", "e-relay", "Reply messages and the end-of-reply status byte", "Go")
rel("e15", "e-relay", "e-sessions", "Reply bytes for each recipient", "Go")
rel("e24", "e-relay", "e-reset", "The reply ended, with its status byte", "Go")
rel("e25", "e-reset", "e-backends", "Writes the reset on the connection; makes it idle or closes it", "Go")
rel("e26", "e-reset", "e-seams", "Asks the release policy; returns the connection to the pool seam", "Go")
rel("e29", "e-reset", "n-pgwire", "Encodes the reset's Query; frames its reply", "Go")
rel("e16", "e-pipeline", "e-fail", "Every refusal and failure", "Go")
rel("e17", "e-fail", "n-pgwire", "Builds the error reply for its contract row", "Go")
rel("e28", "e-fail", "e-seams", "Hands each failure, with its reached fact, to the retry policy", "Go")
rel("e18", "e-clock", "e-pipeline", "A deadline was reached", "Go")
rel("e19", "e-drain", "e-pipeline", "Sets the draining input", "Go")
rel("e20", "e-publish", "n-obs", "Uses the histogram, the gauge and the conservation check; the engine source mapping reads its snapshot", "Go, plain structs")
rel("e21", "e-pipeline", "n-work", "Passes work units and outcomes between stages", "Go types")

# --------------------------------------------------------------------------- wire layer components
P = "c-pgwire"
comp(P, "w-framer", "Framer",
     "Ours. Cuts a byte stream into messages (type byte, 4-byte length, body) whatever the fragmentation, down to "
     "one byte at a time; also the untyped packets before startup. Enforces a maximum message size and refuses an "
     "oversized message from its header alone. Can skip the bodies of chosen message types instead of buffering them.")
comp(P, "w-msgs", "Message coding",
     "Ours, written by hand, no pgproto3. Appends each message the session machines write (TLS and startup "
     "packets, authentication, parameter status, key data, ready, query, terminate, cancel) and parses the startup "
     "packet and its parameters.")
comp(P, "w-codec", "Codec boundary",
     "The only product code that may touch pgproto3: message structs encoded and decoded in one place. "
     "pgproto3's own blocking reader is not used. Nothing passes through it so far: the session machines, the "
     "trackers and the reply builders all use the hand-written message coding.", tech="Go, pgproto3")
comp(P, "w-client", "Client session machine",
     "Client-facing, startup only: events in, actions out. Runs the exchange before startup: answers a TLS request S "
     "or N by client_tls.mode and asks its driver to start TLS (at most one upgrade; a byte before the handshake "
     "result is a violation), answers a GSS encryption request N. Reads the startup message, refuses plaintext when "
     "TLS is required, negotiates protocol 3.x down to 3.0, finds the tenant by user, runs SCRAM-SHA-256 (server "
     "side; trust only as a named test setting; an unknown user gets the wrong-password reply), sends the parameter "
     "set and proxy-issued key data, recognises cancel requests. Every failed startup ends under one reason. "
     "Consumes no byte after ready.")
comp(P, "w-backend", "Database session machine",
     "Database-facing, startup only: events in, actions out. Sends a TLS request first when backend_tls.mode asks "
     "for one (an N answer is a connect failure; a byte after S before the handshake result is a violation), then "
     "startup, SCRAM-SHA-256 (client side, never -PLUS), collects server parameters and key data, reaches ready. "
     "Every failed startup ends in one class (TLS refused or failed, authentication, too many connections, server "
     "FATAL, violation, deadline, connection lost).")
comp(P, "w-cycle", "Query-cycle tracker",
     "Follows a reply to its ReadyForQuery, reports the transaction status byte (idle, in transaction, failed), "
     "flags an unsolicited ParameterStatus (the connection is now dirty) and any protocol surprise, including a "
     "stream that ends without its marker. Never buffers row data. Unknown but well-framed message types are "
     "reported and passed through.")
comp(P, "w-front", "Client query-phase tracker",
     "Reads the client's bytes after login. Reports each Query (body copied, bounded), Terminate, and every "
     "extended-protocol message as one to refuse; any other message type is a violation.")
comp(P, "w-scram", "SCRAM-SHA-256 functions",
     "Pure functions on the standard library: verifier, key derivation, nonce, the GS2 header rule, and the server "
     "and client sides of the exchange with bounded messages. Shared with the simulator's fake Postgres and "
     "simulated clients.",
     tech="Go, crypto/pbkdf2, hmac, sha256")
comp(P, "w-fatal", "Startup FATAL replies",
     "The replies the session machines make themselves during startup: an ErrorResponse with severity, SQLSTATE and "
     "message, the old-format error for a protocol 2 client, and NegotiateProtocolVersion. Also parses the severity "
     "and SQLSTATE of an ErrorResponse from the database. The client session machine names a row of the error "
     "table for every FATAL it writes and takes the code from that row; the encoders stay here.")
comp(P, "w-snapshot", "Wire snapshot",
     "The wire layer's counters as one plain struct, per side: startups begun, completed, in progress and failed by "
     "reason; pre-startup requests by kind and answer; TLS handshakes by version and failure; plaintext refused; "
     "authentication failures by cause; messages and bytes each way; violations; oversized messages; what the "
     "trackers saw. Its check: begun = completed + failed + in startup, on each side.")
comp(P, "w-lexer", "SQL lexer",
     "Tokens only: words, quoted and Unicode identifiers, strings (escape, Unicode and bit strings, and a string "
     "continued across a newline, joined as Postgres does), dollar quotes, numbers, parameters, operators, "
     "punctuation, comments; anything else is a bad token. Not a parser. Linear time.")
comp(P, "w-rejects", "Reject list",
     "The courtesy list as one table: 16 reasons, each with the feature, why it cannot work under transaction "
     "pooling and an example, plus the transaction-scoped forms that are allowed. docs/reject-list.md is generated "
     "from it. Not a safety mechanism: no guarantee rests on it.")
comp(P, "w-classifier", "Statement classifier",
     "Turns one Query message into a work unit. Lexes it, splits it on semicolons and rejects the whole message "
     "when any statement uses a reject-list feature (first reason wins); otherwise the unit starts a lease, or "
     "continues one inside a transaction. Never refuses a message for being untokenisable: it is marked opaque and "
     "forwarded. Sets safe-read from the safe-shape recogniser, shareable (safe-read, outside a transaction, not "
     "rejected), hinted (the text begins with /* mg:share */) and the share key (exact text, tenant by "
     "config). Counts by class and reason in its own snapshot, with a conservation check.")
comp(P, "w-share", "Safe-shape recogniser",
     "Decides what may be shared, from the raw bytes alone: it uses neither the lexer nor the classifier and "
     "imports nothing. A text fits when it is at most 4,096 bytes of a small alphabet, starts with SELECT, has no "
     "word on the stop list, no time word in a string, and calls only functions on the allow-list. Allocates "
     "nothing. docs/safe-shape.md is generated from its tables. Stated limit: it cannot see a view, operator or "
     "row-level security policy that hides a side effect.")
comp(P, "w-errors", "Error contract and reply builders",
     "One table of 23 rows: each refusal and failure, its reply form, its SQLSTATE, whether anything reached the "
     "database, whether a write may have applied, and what happens to the client connection. The reply builder "
     "appends the ErrorResponse and the correct end marker for a row and counts replies by row. The "
     "discard-until-Sync rule refuses extended-protocol messages. docs/error-contract.md is generated from the "
     "table. The engine sends the rows of the walking skeleton and the client session machine the startup rows; "
     "the rows of cores not built yet (breaker, rate gate and others) are not sent so far.")
comp(P, "w-harness", "Startup-only test proxy and startup fidelity runner",
     "internal/pgwire/pgwiretest. A proxy for tests on simulated streams: the two session machines back to back, "
     "one database connection per client, no pool; after login it relays queries and replies through both trackers. "
     "A database startup failure reaches the client as FATAL 08006. Also the startup fidelity runner: one script "
     "(TLS answers, SCRAM, protocol versions) played against the fake Postgres and compared with Postgres 17's "
     "transcript, recorded for CI or live in tier R.", tags=("test",))
comp(P, "w-world", "Wire scenarios",
     "internal/pgwire/pgwiretest/wireworld. 19 named scenarios with their exact checks: the test proxy between "
     "simulated clients and the fake Postgres, plaintext and TLS, one-byte delivery, and one startup fault each on "
     "the client side (TLS stall, bad hello, smuggled bytes, second TLS request, plaintext refused, wrong password, "
     "unknown user) or the database side (silence, connect stall, garbage, reset, connection limit, TLS refused, bad "
     "certificate, handshake stall). Each failure must be counted under its one reason within its deadline.",
     tags=("test",))
comp(P, "w-clsworld", "Classifier scenarios",
     "internal/pgwire/pgwiretest/clsworld. Two named scenarios and a small test gateway on simulated streams: it "
     "relays startup to the fake Postgres (trust login, no session machines), classifies each Query, answers a "
     "rejected one and every extended-protocol message itself, and forwards the rest. Simulated clients send every "
     "reject-list feature, plain and in padded spellings. Checks: each gets 0A000, no rejected statement reaches the fake, "
     "and the session's search_path is unchanged.", tags=("test",))
rel("w1", "n-engine", "w-client", "Client bytes and events in; actions out, until the client is ready", "Go")
rel("w2", "n-engine", "w-backend", "Database bytes and events in; actions out, until the connection is ready", "Go")
rel("w3", "n-engine", "w-cycle", "Reply bytes in; status and message boundaries out", "Go")
rel("w4", "n-engine", "w-classifier", "Statement text in; work unit out", "Go")
rel("w5", "n-engine", "w-errors", "Contract row in; reply bytes out", "Go")
rel("w17", "n-engine", "w-front", "Client bytes after login in; queries and terminate out", "Go")
rel("w53", "n-engine", "w-framer", "Frames the reply of the reset it sends after every lease", "Go")
rel("w54", "n-engine", "w-snapshot", "Keeps one set of wire counters for all its sessions and trackers", "Go")
rel("w55", "n-engine", "w-msgs", "Encodes each Query it forwards, and the reset", "Go")
rel("w9", "w-errors", "w-msgs", "Encodes ErrorResponse and ReadyForQuery by hand", "Go")
rel("w10", "w-codec", "w-framer", "Framed message bodies (no caller yet)", "Go")
rel("w11", "w-codec", "pgproto3", "Message structs: Encode, Decode", "Go import")
rel("w18", "w-client", "w-framer", "Cuts the client's startup packets and messages", "Go")
rel("w19", "w-backend", "w-framer", "Cuts the server's startup messages", "Go")
rel("w20", "w-cycle", "w-framer", "Message boundaries; row bodies skipped, not buffered", "Go")
rel("w21", "w-front", "w-framer", "Message boundaries; extended-protocol bodies skipped", "Go")
rel("w22", "w-client", "w-msgs", "Parses the startup packet; encodes its replies", "Go")
rel("w23", "w-backend", "w-msgs", "Encodes the TLS request, the startup message and the SASL responses", "Go")
rel("w24", "w-client", "w-fatal", "Builds each refusal it sends", "Go")
rel("w25", "w-backend", "w-fatal", "Reads the SQLSTATE of a server error during startup", "Go")
rel("w26", "w-cycle", "w-fatal", "Reads the severity and SQLSTATE of an ErrorResponse", "Go")
rel("w12", "w-client", "w-scram", "Server side of the exchange", "Go")
rel("w13", "w-backend", "w-scram", "Client side of the exchange", "Go")
rel("w27", "w-client", "w-snapshot", "Counts client startups, requests, handshakes and failures", "Go")
rel("w28", "w-backend", "w-snapshot", "Counts database startups, TLS answers and failures by class", "Go")
rel("w29", "w-cycle", "w-snapshot", "Counts replies, ready by status, errors and unsolicited messages", "Go")
rel("w30", "w-front", "w-snapshot", "Counts queries seen, terminates and extended messages refused", "Go")
rel("w14", "w-classifier", "w-lexer", "Tokens of the message; whether any was bad", "Go")
rel("w40", "w-classifier", "w-rejects", "The reason of each refusal; the lists of session lock functions and lexing settings", "Go")
rel("w41", "w-classifier", "w-share", "Raw text in; fits the safe shape or not", "Go")
rel("w15", "w-classifier", "n-work", "Produces work units with their reject reason and share key", "Go types")
rel("w42", "w-rejects", "n-work", "Names each rule by its reject reason", "Go types")
rel("w43", "w-errors", "n-work", "Maps a refusal to its row; records reached and write-applied as three-valued facts", "Go types")
rel("w44", "w-clsworld", "w-classifier", "Classifies every Query the clients send", "Go, test code")
rel("w45", "w-clsworld", "w-errors", "Builds each refusal; runs the discard-until-Sync rule on every client message", "Go, test code")
rel("w52", "w-clsworld", "w-rejects", "Takes every rule's example as a statement to send, plain and padded", "Go, test code")
rel("w46", "w-clsworld", "w-front", "Reads the clients' messages through it", "Go, test code")
rel("w47", "w-clsworld", "w-framer", "Cuts the startup packet and the database's replies, to follow ReadyForQuery", "Go, test code")
rel("w48", "w-clsworld", "w-msgs", "Encodes the queries it forwards", "Go, test code")
rel("w49", "w-clsworld", "w-snapshot", "Keeps wire counters; statements sent is its count of queries seen", "Go, test code")
rel("w50", "w-clsworld", "n-sim", "Starts the fake Postgres and the simulated clients on simulated streams", "Go, test code")
rel("w51", "w-share", "pg", "Tier R test: every corpus text that fits runs on a real Postgres 17 inside a read-only transaction, changes nothing and returns the same rows twice", "go test, scripts/tier-r.sh, Docker")
rel("w16", "w-cycle", "n-work", "Produces outcomes", "Go types")
rel("w31", "w-harness", "w-client", "Drives one per client connection, with the simulated handshake and a startup deadline", "Go, test code")
rel("w32", "w-harness", "w-backend", "Drives one per database connection, opened after the client is authenticated", "Go, test code")
rel("w37", "w-harness", "w-cycle", "Relays each reply through it", "Go, test code")
rel("w38", "w-harness", "w-front", "Relays each client message through it", "Go, test code")
rel("w39", "w-harness", "w-snapshot", "Registers its conservation check as a simulator invariant", "Go, test code")
rel("w33", "w-harness", "n-sim", "Listens and dials on simulated streams; plays the simulated TLS handshake; the fidelity runner dials the fake Postgres", "Go, test code")
rel("w34", "w-world", "w-harness", "Builds one test proxy per scenario and reads its wire snapshot", "Go, test code")
rel("w35", "w-world", "n-sim", "Starts the fake Postgres and the simulated clients; injects the scenario's fault", "Go, test code")
rel("w36", "w-harness", "pg", "Tier R: plays the startup fidelity script against two Postgres 17 servers, ssl off and ssl on with SCRAM; on request rewrites the recording CI replays", "scripts/tier-r.sh, Docker, go test")

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
     "SCRAM-SHA-256; answers a TLS request S or N and plays the simulated handshake; a max_connections limit with "
     "Postgres's refusal; transaction status; a small keyed table with "
     "write, statement and transaction logs; service-time models and a capacity limit; cancel requests; every "
     "database-side fault (the TLS ones — answers N, a handshake that fails or stalls, a certificate that fails "
     "verification — are a setting that can change during a run, not fault kinds); its own snapshots.",
     tech="Go, pgproto3")
comp(P, "s-workload", "Clients and tenants",
     "internal/sim/workload. Simulated clients grouped in tenants: open-loop arrivals (constant or Poisson) and "
     "closed-loop think time, latency measured from the intended send time, uniform, Zipf or fixed keys, "
     "transactions of several statements, retries with backoff; login by trust or SCRAM-SHA-256, with a TLS request "
     "first when the tenant asks; every client-side fault (the four TLS ones — stall after the request, bad hello, "
     "plaintext after the request, a second request — are a per-tenant setting); its own snapshot. A "
     "client sends one request at a time; later arrivals wait in its bounded backlog. Also a hand-driven client "
     "(Probe) for tests.")
comp(P, "s-testclient", "Test client",
     "internal/testclient. A Postgres client with no I/O: bytes in, messages out. Its own framer, a message codec, "
     "and a session machine (phase, status byte, key data, parameters) with trust login. It hands the data of "
     "each authentication message to its caller, which is how the simulated clients run SCRAM. Used "
     "by the simulated clients on simulated streams and by the tier R tests on TCP.", tech="Go, pgproto3")
comp(P, "s-simtls", "Simulated TLS handshake",
     "internal/sim/simtls. A handshake as an exchange with an outcome, not cryptography: a 16-byte client hello (or "
     "a bad one) and a 16-byte server reply that says completed with a TLS version, bad certificate, or alert. "
     "Models the outcome and the duration only.")
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
rel("m6", "n-engine", "s-net", "The engine's simulator driver (proxysim) implements the transport seam on these streams: listens, dials, reads, writes, closes", "Go, test code")
rel("m7", "s-workload", "s-net", "Opens client connections; sends and reads", "Go")
rel("m8", "s-fakepg", "s-net", "Accepts database connections; replies", "Go")
rel("m9", "s-faults", "s-net", "Applies the network faults at their instants", "Go")
rel("m10", "s-faults", "s-fakepg", "Applies the database fault kinds the fake registered: refuse, silence, slow, reset, garbage, kill, freeze, restart and more", "Go")
rel("m11", "s-faults", "s-workload", "Applies the client fault kinds the workload registered: half a message, stop reading, disconnect, reconnect burst, cancel", "Go")
rel("m12", "s-workload", "s-streams", "Arrivals, keys, request mix and retries, one stream each per client", "Go")
rel("m13", "s-fakepg", "s-streams", "Service-time jitter, one stream per connection; cancel keys", "Go")
rel("m14", "s-fakepg", "pgproto3", "Wire messages", "Go import")
rel("m15", "s-testclient", "pgproto3", "Wire messages", "Go import")
rel("m16", "s-fakepg", "n-pgwire", "Reuses the SCRAM functions (server side) and the NegotiateProtocolVersion encoder", "Go")
rel("m29", "s-workload", "s-testclient", "Every client frames, decodes and tracks its session with it", "Go")
rel("m30", "s-workload", "s-randdist", "Poisson gaps and Zipf keys", "Go")
rel("m43", "s-workload", "n-pgwire", "Reuses the SCRAM functions (client side)", "Go")
rel("m44", "s-fakepg", "s-simtls", "Reads the client hello; replies with the configured outcome", "Go")
rel("m45", "s-workload", "s-simtls", "Sends the client hello, or a bad one; reads the reply", "Go")
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
rel("m17", "n-engine", "s-trace", "The engine's simulator driver appends a record for each connection it closes and writes the engine's counters for the state digest", "Go, test code")
rel("m18", "n-engine", "s-inv", "The engine's simulator driver registers the engine's invariants (snapshot conservation, one client per lent connection) and its quiesce check", "Go, test code")
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
rel("m46", "n-engine", "s-simtls", "The engine's simulator driver plays the handshake on both legs: server for clients, client for the database", "Go, test code")
rel("m47", "n-engine", "s-fakepg", "The simulator driver's World starts the fake as the engine's database", "Go, test code")
rel("m48", "n-e2e", "s-world", "Implements the driver interface with the proxy between the clients and the fake, and runs a direct baseline through it", "Go test")
rel("m49", "n-e2e", "s-fakepg", "Tier S checks read the fake's statement and write logs: transaction integrity, session state, what ran after a fault", "Go test")
rel("m50", "n-e2e", "s-faults", "Scenario fault schedules; the operation fuzz target decodes its input into a timed operation list in the schedule's form", "Go test")
rel("m51", "n-e2e", "s-testclient", "The scripted clients of both tiers frame, decode and track their session with it", "Go test")

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
     "failed only when this instance cannot work (its client listener is down). A database outage is degraded.")
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
     "components that exist: the kernel, simnet, the fake Postgres (its authentication and TLS counters too) and "
     "the workload (its startup counters too). It lives here because the "
     "simulator packages are frozen; a later component writes the same two functions in its own package.",
     tags=("test",))
comp(P, "o-wiresrc", "Wire source mapping",
     "internal/obs/wiresrc. The metric declarations of component wire and the mapping from the wire snapshot to "
     "points. The side is in the metric name (client_..., database_...); labels are reason, outcome and mode; "
     "every known label value is emitted, zero included; no tenant label.")
comp(P, "o-clssrc", "Classifier source mapping",
     "internal/obs/clssrc. The metric declarations of component classifier and the mapping to points from three "
     "wire-layer snapshots: the classifier's (statements, by class, rejects by reason, opaque, safe-read, "
     "shareable, hinted, share keys), the reply builder's (replies by error-table row, failures) and the "
     "discard-until-Sync counters. All counters; every known label value is emitted, zero included; no tenant label.")
comp(P, "o-enginesrc", "Engine source mapping",
     "internal/obs/enginesrc. The metric declarations of component engine and the mapping to points from two "
     "snapshots: the engine's, and the shell's (mailbox depth and peak, goroutines, readers and writers, deadlines; "
     "names shell_...). Labels are outcome, reason and mode; no tenant label so far. Registers the engine's wire "
     "and classifier sources beside its own. Holds the health rule database: degraded while no database connection "
     "is usable and the last connect failed.")
comp(P, "o-catalog", "Metric catalog and schema document",
     "internal/obs/catalog. The registry of every source the product registers, and the generator of "
     "docs/csv-schema.md (columns of the three files and every metric). Its test fails when the committed document "
     "differs from the code; scripts/gen-docs.sh regenerates or checks it.")
comp(P, "o-simrun", "Scenario flush harness",
     "internal/obs/simrun. Runs a simulator world scenario, a wire scenario, a classifier scenario, or any run "
     "handed its simulator and sources (the end-to-end suite's tier S scenarios), with a "
     "collector that publishes on the virtual clock and "
     "with the CSV, JSON, Prometheus and log adapters attached, then checks every output against the final "
     "snapshot. Its tests run the five direct baselines, every wire scenario and both classifier scenarios twice "
     "per seed and compare the files byte for byte.",
     tags=("test",))
comp(P, "o-assert", "SQL assertions and their runner",
     "scripts/csv-assert.sh and test/analysis. The runner finds run directories, checks them with guard.sql (schema "
     "version, no empty required field, one run id per directory, no empty run), loads views.sql and runs each "
     "assertion file; a returned row is a violation. Fails closed and never skips. The assertion files check "
     "the direct baselines' counts and, for each wire scenario, that startup failures by reason, TLS requests by "
     "answer and handshake failures equal what was injected and sent, and for the classifier scenarios that "
     "rejects equal the statements sent and the fake Postgres started only what was forwarded (nothing in the "
     "reject-only scenario). For the end-to-end scenarios of both tiers: conservation per instance, the resets "
     "against the leases ended and, where the scenario has a fault, error-contract replies by row = faults "
     "injected; in tier S also proxy completed = workload completed and replies by row = what the clients saw.",
     tech="Bash, SQL", tags=("test",))
rel("o1", "n-shell", "o-collector", "Creates it in the real process, attaches the JSON, Prometheus and (when obs.csv.dir is set) CSV adapters, and publishes when the engine returns a publish action: once per interval and at the stop", "Go")
rel("o2", "o-collector", "o-contract", "Calls every source's Snapshot and mapping at each publish", "Go")
rel("o3", "o-contract", "o-hist", "Histograms inside snapshots", "Go")
rel("o4", "o-collector", "o-health", "Evaluates the rule table on its last snapshot", "Go")
rel("o5", "n-shell", "o-prom", "Admin listener serves /metrics", "HTTP")
rel("o6", "n-shell", "o-json", "Admin listener serves /stats", "HTTP")
rel("o7", "n-shell", "o-health", "Admin listener serves /healthz; adds the rule listener (failed while the client listener is down)", "HTTP")
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
rel("o18", "n-e2e", "o-csv", "Turns the flush on for every run in both tiers; checks the final flush equals the snapshot (tier R: reads the binary's files back and runs the round-trip check on them)", "Go test")
rel("o19", "n-e2e", "o-assert", "Runs the SQL assertions over the run directories of every scenario, both tiers", "scripts/chaos.sh, scripts/csv-assert.sh")
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
rel("o34", "o-simsrc", "n-sim", "Reads the snapshots of the kernel, simnet, the fake Postgres (both of its snapshots) and the workload", "Go")
rel("o35", "o-simsrc", "o-collector", "Registers one source per simulator component, with its metric declarations", "Go")
rel("o37", "o-catalog", "o-simsrc", "Lists every metric it declares", "Go")
rel("o38", "o-catalog", "o-csv", "Reads the column lists of the three files", "Go")
rel("o39", "o-catalog", "o-registry", "Builds the full registry, the collector's own metrics included", "Go")
rel("o40", "o-simrun", "n-sim", "Runs a world scenario through the simulator driver; publishes from a timer on the virtual clock", "Go")
rel("o41", "o-simrun", "o-collector", "Creates it, attaches the four adapters, publishes every 100 ms of virtual time (every 5 s and at the deadline check for a wire scenario) and at the end", "Go")
rel("o42", "o-simrun", "o-simsrc", "Takes the sources of the scenario's world", "Go")
rel("o43", "o-simrun", "o-csv", "Checks the final flush read back equals the final snapshot", "Go")
rel("o44", "o-simrun", "o-prom", "Checks the text with the grammar checker", "Go")
rel("o45", "o-simrun", "o-json", "Checks the document decodes to the final snapshot", "Go")
rel("o46", "o-simrun", "o-assert", "Its tests run the runner over the baseline runs and over planted bad runs", "Go test")
rel("o47", "o-assert", "duckdb", "Runs the guard, the views and each assertion file", "DuckDB CLI")
rel("o48", "o-assert", "csvfiles", "Finds the run directories and checks the three headers", "Bash")
rel("o52", "o-wiresrc", "n-pgwire", "Reads the wire snapshot", "Go")
rel("o53", "o-wiresrc", "o-collector", "Gives it one source for component wire, with its metric declarations", "Go")
rel("o54", "o-catalog", "o-wiresrc", "Lists every metric it declares", "Go")
rel("o55", "o-simrun", "n-pgwire", "Builds and runs a wire scenario (wireworld) or a classifier scenario (clsworld) and checks it with the scenario's own assertions", "Go")
rel("o56", "o-simrun", "o-wiresrc", "Takes the wire source of the scenario's test proxy or test gateway", "Go")
rel("o57", "o-clssrc", "n-pgwire", "Reads the classifier, reply-builder and discard-until-Sync snapshots", "Go")
rel("o58", "o-clssrc", "o-collector", "Gives it one source for component classifier, with its metric declarations", "Go")
rel("o59", "o-catalog", "o-clssrc", "Lists every metric it declares", "Go")
rel("o60", "o-simrun", "o-clssrc", "Takes the classifier source of the scenario's test gateway", "Go")
rel("o61", "o-enginesrc", "n-engine", "Reads the engine snapshot and the wire, classifier and reply snapshots the engine holds", "Go")
rel("o62", "o-enginesrc", "n-shell", "Reads the shell's counters through a function the shell hands over", "Go")
rel("o63", "o-enginesrc", "o-collector", "Registers the engine source (shell metrics included) and the engine's wire and classifier sources", "Go")
rel("o64", "o-enginesrc", "o-wiresrc", "Takes the wire source for the engine's wire snapshot", "Go")
rel("o65", "o-enginesrc", "o-clssrc", "Takes the classifier source for the engine's classifier and reply snapshots", "Go")
rel("o66", "o-enginesrc", "o-health", "Supplies the rule database", "Go")
rel("o67", "o-catalog", "o-enginesrc", "Lists every metric it declares", "Go")
rel("o68", "n-shell", "o-csv", "The real process flushes through the bounded queue when obs.csv.dir is set", "Go")
rel("o69", "n-e2e", "o-simrun", "Tier S runs every scenario under it, with the engine's and the simulator's sources", "Go test")
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
              "Observability hangs off the side: the engine publishes snapshots, and the admin listener serves them.",
        nodes=["app", "operator", "c-shell", "c-engine", "c-pgwire", "c-adm", "c-coal", "c-pool", "c-retry",
               "c-obs", "c-config", "pg", "telemetry"],
        rels=["c%d" % i for i in range(1, 16) if i != 8], boundary="mini-gateway process",
        detail={"c-engine": "component-engine", "c-pgwire": "component-wire", "c-adm": "component-admission",
                "c-pool": "component-pool", "c-coal": "component-coalescer",
                "c-retry": "component-retry", "c-obs": "component-observability"})

diagram("container-seams", "container", "Containers: drivers and seams", "03-container-drivers-and-seams", scope="gw", parent="context",
        shows="Who drives the engine (the shell or the simulator), where the Postgres codec boundary is, and the work vocabulary the cores are written against",
        intro="This diagram shows the verdict on the backend interface: there is no generic backend interface. Instead, "
              "there are three seams. The first is the engine's byte transport, which the shell implements over TCP "
              "and the engine's test package proxysim implements on the simulator. The second is the wire layer, the "
              "only product package allowed to import pgproto3. The third is the work vocabulary, the only thing the "
              "cores see. A second protocol would be a sibling of the wire layer producing the same vocabulary.",
        nodes=["developer", "c-sim", "c-e2e", "c-shell", "c-engine", "c-pgwire", "c-work", "c-adm", "c-pool", "c-coal",
               "c-retry", "pgproto3", "pg"],
        rels=["c3", "s1", "c4", "s2", "s3", "s18", "s4", "s5", "s6", "s8", "s9", "s10", "s17", "s11", "s12", "s13", "s14", "s15", "c10", "s16"],
        boundary="mini-gateway repository",
        detail={"c-sim": "component-simulator", "c-engine": "component-engine", "c-pgwire": "component-wire"})

diagram("deployment", "deployment", "Deployment: three instances, one database", "04-deployment-fleet", parent="container",
        shows="The supported multi-instance deployment: static fleet config, per-instance enforcement, no run-time coordination",
        intro="Each instance enforces its own limits. The config declares the instance count and divides the fleet "
              "budgets. The table in PLAN.md section 8.2 lists what multiplies, weakens, or survives at N instances. "
              "The weakened rows are measured in three-instance scenarios.",
        nodes=["d-clients", "i-spread", "d-tier", "i-gw1", "i-gw2", "i-gw3", "d-db", "i-pg", "d-ops", "i-tel"],
        rels=["x%d" % i for i in range(1, 10)], env=ENV, direction="LR",
        groups=[("d-clients", ["i-spread"]), ("d-tier", ["i-gw1", "i-gw2", "i-gw3"]), ("d-db", ["i-pg"]), ("d-ops", ["i-tel"])])


def component_diagram(id, title, file, scope, shows, intro, extra_nodes, rels):
    own = [e["id"] for e in E.values() if e["kind"] == "component" and e.get("parentId") == scope]
    diagram(id, "component", title, file, scope=scope, parent="container", shows=shows, intro=intro,
            nodes=own + extra_nodes, rels=rels, boundary=E[scope]["name"])


component_diagram("component-engine", "Components: engine", "05-component-engine", "c-engine",
                  "internal/proxy: the transport seam and its simulator driver, the two connection tables, the request pipeline and its stage seams, the lease end with its reset, the reply relay, the failure classifier, deadlines, the snapshot",
                  "The engine is where the seams meet. Below it, the transport seam has two implementations. Above it, "
                  "the pipeline calls each core through a small stage interface, so a core can be replaced by a "
                  "pass-through. That is how each scenario's ablation run is made. Chunk 6 built the walking skeleton: "
                  "the whole path of a request, with a stand-in at every stage whose core is not built yet. As a "
                  "result, the arrows to admission, the coalescer, the pool, and the retry policy are still dashed.",
                  ["n-shell", "n-sim", "n-pgwire", "n-adm", "n-coal", "n-pool", "n-retry", "n-obs", "n-work"],
                  ["e%d" % i for i in (1, 2, 22, 3, 4, 5, 6, 7, 27, 23, 8, 9, 10, 12, 13, 14, 15, 24, 25, 26, 29, 16, 17, 28, 18, 19, 20, 21)])
component_diagram("component-wire", "Components: wire layer", "06-component-wire-layer", "c-pgwire",
                  "internal/pgwire: our framer and message coding, both session machines, SCRAM, the two trackers, the wire snapshot, the SQL lexer, the classifier with its courtesy reject list, the safe-shape recogniser, the error contract and reply builders, the test proxies and their scenarios; planned: the pgproto3 codec boundary",
                  "pgproto3 provides message structs and nothing else. The framer, session machines, SCRAM, "
                  "transaction tracking, classification, and error mapping are ours. Everything here is a pure state "
                  "machine or a pure function. Chunk 4 built the sessions, and chunk 5 built the classifier and the "
                  "contracts. Because the pool will reset every connection after every lease, session safety does not "
                  "depend on reading SQL, so the reject list is a courtesy. A recogniser that does not use the lexer "
                  "decides what may be shared. The machines, trackers, and reply builders code their messages "
                  "by hand, so no file in the package imports pgproto3 and the codec boundary has no caller yet. Since "
                  "chunk 6 the engine drives this code. The two test proxies remain as the wire layer's own scenarios "
                  "in the simulator, between simulated clients and the fake Postgres.",
                  ["n-engine", "n-work", "n-sim", "pgproto3", "pg"],
                  ["w%d" % i for i in (1, 2, 3, 17, 4, 5, 53, 54, 55, 18, 19, 20, 21, 22, 23, 24, 25, 26, 12, 13, 27, 28, 29, 30,
                                         9, 10, 11, 14, 40, 41, 15, 42, 43, 16, 31, 32, 37, 38, 39, 33, 34, 35, 36,
                                         44, 45, 52, 46, 47, 48, 49, 50, 51)])
component_diagram("component-admission", "Components: admission", "07-component-admission", "c-adm",
                  "internal/admission: the rate gate, per-tenant queues, both CoDel modes behind one interface, the dispatcher, the adaptive limit",
                  "Decisions run in order: gate, then queue, then dispatch under the limit. The two queue rules are "
                  "equal, selectable implementations of one interface. Two control loops read two different signals: "
                  "the queue rule reads queue wait, and the adaptive limit reads database round-trip time.",
                  ["n-engine", "n-work"], ["a%d" % i for i in range(1, 16)])
component_diagram("component-pool", "Components: pool", "08-component-pool", "c-pool",
                  "internal/pool: the hard cap, the creation limiter, leases per transaction, the breaker, the dirty-connection table",
                  "A request never waits inside the pool: the dispatcher releases it only when a lease is available. "
                  "The pool enforces the cap, controls the creation rate, and lends a connection only when it is idle "
                  "and clean.",
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
                  "internal/sim and its sub-packages, internal/testclient and internal/randdist: the loop, the clock, simulated byte streams, the simulated TLS handshake, the fake Postgres, simulated clients, the test client, the scenario world with its baselines and fidelity runner, faults and the shrinker, the trace, invariants and the quiesce check, replay, the toy protocol",
                  "The harness owns the world around the engine. The engine's own test package, proxysim, implements "
                  "the transport seam on simulated streams, so the engine under test is the same engine that ships. "
                  "Several engines can run on one loop, which is how the three-instance scenarios will be made. The "
                  "kernel came first (chunk 1) and is proved on a toy echo protocol. Chunk 2 added the world the proxy "
                  "stands in: the fake Postgres, the simulated clients, and scenarios that run the clients straight at "
                  "the fake. Chunk 4 added SCRAM login, plus the TLS request, answer, and simulated handshake, to both "
                  "the fake and the clients. Chunk 6 placed the engine between the clients and the fake without "
                  "changing the simulator packages themselves. The direct baselines serve as the control arms.",
                  ["developer", "n-engine", "n-e2e", "n-pgwire", "pgproto3", "pg"], ["m%d" % i for i in range(1, 52) if i != 31])
component_diagram("component-observability", "Components: observability", "12-component-observability", "c-obs",
                  "internal/obs: the one raw export (snapshots), the metric registry, the collector, the adapter interface and its five adapters, and where each output goes: the CSV flush read by tests and DuckDB, and OTLP to a Collector to Prometheus",
                  "The hard rule is one common raw export, then the collector interface, then adapters. Components "
                  "fill plain structs, the collector assembles them, and every output form is an adapter that reads "
                  "only what the collector publishes. No component knows about CSV, DuckDB, OTel, or Prometheus, and "
                  "adding an adapter touches no component. Two paths leave the process. On the testing path, the CSV "
                  "flush adapter writes files that tests and DuckDB read with nothing else running. On the production "
                  "path, the OTLP adapter pushes to an OpenTelemetry Collector, which Prometheus scrapes. DuckDB is an "
                  "analysis tool over files, never a dependency of the proxy. Chunk 3 built this spine before the "
                  "proxy's components, starting with sources for the simulator's kernel, network, fake Postgres, and "
                  "workload. Later chunks added the wire layer's snapshot (chunk 4, the first product source), the "
                  "classifier's and reply builders' counters (chunk 5), and the engine with its shell (chunk 6). In "
                  "the real process, the shell creates the collector and publishes; in the simulator, the scenario "
                  "flush harness does.",
                  ["n-engine", "n-shell", "n-e2e", "n-sim", "n-pgwire", "csvfiles", "duckdb", "otelcol", "prom"],
                  ["o%d" % i for i in (1, 2, 25, 26, 27, 3, 24, 28, 29, 4, 15, 8, 9, 10, 11, 12, 31, 32, 5, 6, 7, 16, 18, 17, 19,
                                         13, 20, 21, 14, 33, 22, 23, 50, 51, 34, 35, 52, 53, 57, 58, 61, 62, 63, 64, 65, 66, 68, 37, 54, 59, 67, 38, 39, 40, 41, 42, 55, 56, 60, 69, 43, 44, 45, 46, 47, 48)])


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
