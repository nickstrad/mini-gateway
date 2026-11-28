# Lease experiment, stage 0 — calibration note

Written by Fable on 2026-11-26. Plan: [../LEASE-EXPERIMENT-PLAN.md](../LEASE-EXPERIMENT-PLAN.md)
(piece E0, test R0). Scope by Nick's rulings of 2026-11-25
(`/root/briefs/zgateway-lease-exp-rulings.md`): stage 0 only, deploy variant only
(SCRAM-SHA-256, TLS on). Stages 1–3 are not authorized and nothing of them is built. Chunk 5
stays parked.

**Verdict on P2: confirmed in size, wrong about the cause.** Postgres 17 on this VM sustains
about **195 new connections per second** (burst: about 235/s), flat from 8 concurrent
connects to 64, with no failures. That is the "hundreds per second" the plan predicted, at
the low end. The cause is not that backends start one at a time: it is CPU. One
connect-and-close costs about 14 ms of Postgres CPU and 5.5 ms of client CPU, and the plateau
is the point where the 4 vCPUs are full. P2 is not off by an order of magnitude, so the plan's
conclusion stands: a remake costs about 100 times a reset, and remake after every lease is
not a usable policy.

## Conditions

Shared 4-vCPU VM (7.9 GiB); probe and Postgres on the same host, loopback only. Postgres 17.11
in a throwaway container, `shared_buffers=32MB`, `max_connections=600`. TLS 1.3
(TLS_AES_256_GCM_SHA384, RSA 2048 certificate, full handshake every time), SCRAM-SHA-256 with
4,096 iterations, login through the project's own `pgwire.BackendSession`. Figures are for
the container's own address ("bridge"); the published 127.0.0.1 port, which passes through
Docker's proxy, is about 12% slower on connects and twice as slow per round trip. Three full
runs plus three runs of the repaired reset measurement, all pooled; p50 unless marked.

## Calibration table

| Cost | Value | Conditions |
| --- | --- | --- |
| Reset (`DISCARD ALL`), best case | 88 µs (p99 259 µs) | Clean session, tight loop, n = 15,000. `SELECT 1` beside it: 109 µs |
| Reset after an ordinary statement | 177 µs (p99 2.1 ms) | Own session per kind, n = 900. The same after `SET`, `set_config` in a function, `PREPARE`, held cursor, `LISTEN`, advisory lock, `SET ROLE`, `setseed`, custom setting: 149–186 µs, none more than 10 µs above this row |
| Reset when the lease ended in a transaction | 280 µs; 341 µs if it had failed | `ROLLBACK` then `DISCARD ALL`, two messages |
| Reset after a temp table | 3.07 ms (p99 24 ms); 3.89 ms with 1,000 rows and an index | The one expensive kind. The next reset on that backend is still 393 µs |
| Reset on a backend that once held a temp table | 140 µs against 114 µs | Every later clean reset; slower in 18 of 18 pairs |
| Connect, one at a time | 16.0 ms (p90 21.2, p99 37.5) | TCP 0.4 ms, TLS 6.9 ms, SCRAM and backend start 8.4 ms (3.1 ms of it the client's key derivation) |
| Connect, 4 / 16 / 64 at once | 21 ms / 53 ms / 153 ms | Per connect; the growth is waiting for the server's first answer |
| Close until the backend is gone | 0.8 ms or less | Upper bound; the poll that measures it takes 0.7 ms |
| First statement, fresh backend | `SELECT 1` 0.63 ms; key lookup 1.75 ms; plpgsql call 4.40 ms | Reused backend: 0.11, 0.18, 0.22 ms |
| First statement after a reset | `SELECT 1` 0.12 ms; key lookup 0.18 ms; plpgsql call 0.61 ms | The reset costs a plpgsql function about 0.4 ms of re-planning; simple statements nothing |
| **Backend starts, sustained** | **195/s at 16–32 workers** (184–200 across runs); 60/s at 1, 153 at 4, 182 at 8, 193 at 64 | Connect, then close, in a closed loop for 20 s; 0 failures in 119,000 |
| **Backend starts, burst** | 100 at once: 247/s; 500 at once: 234/s (2.1 s for all 500) | 15 repetitions each; 0 failures |
| CPU per connect-and-close | Postgres 13.7–15.6 ms; client 5.5–6.9 ms | At the plateau Postgres holds 2.7 CPUs, the client 1.1, the host 3.95 of 4 |

All values are in [calibration.json](calibration.json) (integers, nanoseconds; id
`sha256:bc619798…3e6edd`), which the probe derives from the raw rows.

## What was verified, and how

Opus built the probe and ran R0; I reviewed it (ruling 3). Review 1 failed on one measurement:
the reset-after-each-kind figures were an artefact of the order the kinds ran in. One repair
round fixed it and review 2 passed. Every number above was checked one of two ways. I
recomputed all of them from the raw sample files with separate SQL, and the calibration file
regenerates byte for byte. I also re-ran the clean reset and the whole P2 measurement on fresh
containers: sustained 60, 104, 157 and 181 per second at 1, 2, 4 and 8 workers, bursts of
199–255/s. Above 8 workers another agent's job took 3.5 CPUs during my run and the rate fell
to 126–167/s, which shows how directly the ceiling follows free CPU. The clean reset read
113 µs on that busier host.

## What P2 means for the remake arm

- **Remake after every lease does not survive.** Its ceiling is about 195 leases per second
  for the whole pool, whatever the pool size. The same pool with no release action does
  about 9,000 trivial leases per second per connection. It also spends the database's CPU on
  logins: one core is used up by every 70 remakes per second. It stays in the experiment only
  as the isolation ceiling that ruling 2 names.
- **The ceiling moves with cores, not with configuration.** By arithmetic, Postgres alone on
  these 4 vCPUs would reach about 280/s. That is an estimate, not a measurement.
- **Remake on tenant switch survives, in a narrow range.** It works where tenant switches
  stay well under the ceiling. Allowing logins a tenth to a quarter of the database's CPU
  gives 20 to 50 switches per second on this VM.
- **In that range the warm stock is small.** With a connect time of about 21 ms and at most
  50 switches per second, about one replacement is under way at a time, and the plan's rule
  asks for about 5 warm connections or fewer. The sizing rule has little room to beat a fixed size.
- **A warm connection is not a hot one (P5 confirmed).** A fresh backend's first statement
  costs 0.5 to 4.2 ms more than on a reused one, which is more than ten resets. Priming a
  warm connection before it is lent is worth testing.
- **The simulator's connect model must change.** The plan assumed a one-at-a-time queue. The
  data fit a shared-CPU model instead: about 20 ms of CPU work per connect, spread over the
  host's cores.

One result outside P2: **P1 was optimistic.** The plan took 44 µs for a reset (measured
earlier over a Unix socket). Over TCP and TLS it is 88 µs at best and about 177 µs after real
work. By the plan's formula, reset after every lease costs 8–15% of capacity at 1 ms leases
and 15–26% at 0.5 ms, not "a few percent". A lease of one trivial statement roughly doubles
in length. Reset on tenant switch therefore matters more than the plan assumed.

## Recommendation for stages 1–3 (Nick's ruling)

Go ahead, with a smaller experiment than planned. Proposed changes to the accepted cut:

1. Keep four policies under test: none, reset-always, reset-on-switch, remake-on-switch.
   Run remake-always only as the cold control, plus one warm run to show the ceiling does not
   move. This removes two of the 11 pairs.
2. Keep the warm stock for remake-on-switch, but shrink the sizing-rule work: the rule
   against fixed sizes over the small range that is reachable, without the reactive
   comparator, the dispersion knob and the smoothing sweep. If you want the full rule study
   for its own sake, it costs about one extra build run.
3. Replace the connect-cost model as above, and add a switch-rate budget to the report.
4. Keep stage 2 whole. The isolation oracle (Astra's piece) is what nothing measured so far
   can answer, and a re-opened chunk 5 needs it under any policy.

That saves roughly a quarter of the plan's cost. The alternative is to stop here: these
numbers already support option A of OPTIONS.md (reset after every lease) with reset-on-switch
as the optimisation, and only the isolation proof would be missing.

## Limits

One shared VM with other agents running; loopback only, so a real network adds its round trip
to every figure. The client shared the CPUs with Postgres, and the client's 3.1 ms key
derivation per connect is the proxy's code as landed (it caches nothing). 13 of 102 rate
cells were disturbed by other work; they are labelled and kept. The reset cost depends on how
recently the backend was busy (177 µs falls to about 104 µs over five consecutive resets), so
the table gives a range, not a constant. Residue items 3 and 4 of OPTIONS.md were not
touched; items 1 and 2 were observed as predicted.

## Where the rest is

The probe `cmd/mgcalib`, `scripts/lease-calib.sh` and the six run directories with the raw
rows (`runs/r0-*`) were on the local branch `exp/lease-lab` and were not kept; the branch was
deleted after the port of `agent_work/chunks/lease-lab-port/`. `calibration.json` beside this
note is the summary of those runs. The builder's notes (method per measurement, reproduce
commands, and the `calibration.json` schema in section 4) are
[lab-records/NOTES-E0.md](lab-records/NOTES-E0.md), and the reviews are
[lab-records/REVIEW-1.md](lab-records/REVIEW-1.md) and
[lab-records/REVIEW-2.md](lab-records/REVIEW-2.md).
