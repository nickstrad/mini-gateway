# Brief B4 - foundational papers / algorithms

Read `_common.md` rules below first (they are appended to this brief).
Output file: /tmp/zgateway-phase0/out/b4-foundations.md

Reduce each to THE EXACT MECHANISM this project would implement: state
variables, update rule, constants, units, a 5-15 line pseudo-code
sketch (pseudo-code only, not Go product code), the invariant a test
can assert, and the classic implementation bug. Verify formulas against
a source you read this run.

1. CoDel (Nichols & Jacobson 2012; RFC 8289) - sojourn time, target,
   interval, first_above_time, dropping state, next-drop = interval /
   sqrt(count). And the Facebook request-queue variant ("Fail at
   Scale": if the queue has not been empty in the last N ms use timeout
   M ms else N ms; N=100, M=5 - confirm from the source; folly
   `Codel.h` on GitHub is a primary source - read it and report its
   actual logic and defaults). Say which one a request queue should use
   and what "converges to target" means as a testable property.
2. Adaptive LIFO (Fail at Scale; folly/wangle LifoSem; Dropbox Bandaid
   proxy post) - the switch rule FIFO->LIFO, and its interaction with
   CoDel and with per-tenant queues.
3. GCRA (ATM Forum / ITU I.371; virtual scheduling form; TAT) - exact
   integer-nanosecond update; equivalence to a leaky bucket as a meter;
   burst tolerance tau; one int64 per tenant, CAS loop. Compare with
4. Token bucket (rate r, burst b; lazy refill on a virtual clock) -
   and say when GCRA vs token bucket matters here.
5. Fair queueing: Deficit Round Robin (Shreedhar & Varghese 1995),
   WFQ / start-time fair queueing, stochastic fairness queueing,
   and Kubernetes APF's shuffle sharding - which one fits "per-tenant
   queues in front of a fixed concurrency limit", with cost O(1).
6. Adaptive concurrency: AIMD (Chiu & Jain 1989), TCP Vegas
   (Brakmo & Peterson 1994: expected vs actual, alpha/beta), Netflix
   gradient (limit * minRTT/RTT + queue_size), Little's Law (L = lambda*W)
   as the sizing identity for pool size / in-flight limit - give the
   worked arithmetic for the spec's numbers (e.g. 20-32 connections,
   50 ms service time -> max throughput; 5,000 QPS noisy tenant).
7. Balls-into-bins and power-of-two-choices (Azar et al. 1994;
   Mitzenmacher 1996/2001): max load ln n/ln ln n vs ln ln n/ln 2; how
   the "fan-in/fan-out" connection-count model works (expected distinct
   bins hit when each of N clients opens k connections to M servers);
   whether P2C matters at all with ONE backend (pick among pool
   connections? among pipelines?) - be honest if it only matters for
   Variant C.
8. Coordinated omission (Gil Tene, "How NOT to Measure Latency";
   wrk2 README; HdrHistogram's expected-interval correction) - the
   open-loop generator rule: intended send time vs actual send time,
   what to record, Poisson vs constant arrivals; Schroeder et al.
   "Open versus closed: a cautionary tale" (NSDI 2006).
9. HDR Histogram (Tene) - bucket layout (significant digits, sub-bucket
   count, exponent buckets), memory size for 1 us..60 s at 2-3 sig
   digits, record() cost, merge, the from-scratch implementation size;
   alternatives worth a line: DDSketch, t-digest, Prometheus native
   histograms - recommend one for a zero-dependency Go build.
10. Benchmark statistics: Kalibera & Jones "Rigorous benchmarking in
    reasonable time", Georges et al. "Statistically rigorous Java
    performance evaluation", Mytkowicz "Producing wrong data without
    doing anything obviously wrong", bootstrap confidence intervals,
    A/A runs, benchstat's method (Mann-Whitney U) - the minimum
    credible protocol for `make bench` on a noisy single VM
    (vm-1 has no hardware perf counters).
11. Overload theory worth knowing: metastable failures (Bronson et al.
    HotOS 2021; Huang et al. OSDI 2022), retry storms and retry
    budgets, "The Tail at Scale" (hedging - note hedging conflicts with
    a no-retry-amplification proxy), Zipf distribution generation for
    the 99/1 hot-key workload (the exact generator, e.g. Gray et al. /
    YCSB zipfian, Go's math/rand Zipf), thundering herd / cache
    stampede literature (XFetch probabilistic early expiry - relevant
    only if caching is in scope), Nagle-style batching and
    "smart batching" (Martin Thompson) for the micro-batch flush rule,
    Redis pipelining economics.
12. Deterministic simulation testing lineage: FoundationDB (Will
    Wilson's Strange Loop talk), TigerBeetle VOPR, Antithesis,
    sled/turmoil/madsim, Go `testing/synctest` (Go 1.24/1.25) and its
    limits - what the minimum Commit-0 harness needs.
