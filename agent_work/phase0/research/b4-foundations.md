# B4 - Foundational papers and algorithms (Mini-ZGateway Phase 0)

Research run: 2026-11-24. Author: fresh research agent (Fable). Research only; no product code.

**How to read the labels.** `read directly` means I fetched the primary text this run and read the
part the entry relies on. For long papers that was the abstract, the introduction and the passages
found by keyword, not the whole paper; I say so in the entry. Numbers marked *(from memory)* were
not confirmed against a source this run. Numbers marked *(computed this run)* come from two small
scripts I wrote and ran: `/tmp/zgateway-phase0/b4work/bin/calc.py` (arithmetic) and
`/tmp/zgateway-phase0/b4work/bin/codelsim.py` (a toy single-seed queue simulation). The simulation
is my own evidence, not a published result. Downloaded sources are in
`/tmp/zgateway-phase0/b4work/src/`.

**Four findings that change the design** (details in the entries):

1. **RFC CoDel is the wrong algorithm for a request queue.** Its drop rate ramps as
   `interval/sqrt(count)`, which assumes senders slow down when they see a drop. Open-loop request
   traffic does not. In my simulation at 10x overload the RFC queue grew without bound. Use the
   Facebook form.
2. **The spec's "N=100, M=5" is confirmed, but folly's code is a different algorithm** from the
   article's pseudo-code. Folly sheds a request when the interval's minimum delay exceeded 5 ms
   *and* this request waited more than 2 x target = 10 ms.
3. **`folly::LifoSem` is not Adaptive LIFO for requests.** It wakes *threads* in LIFO order. The
   article names HHVM as the Adaptive LIFO implementation and gives no numeric switch rule. The
   switch rule is ours to choose.
4. **"Wait converges to the CoDel target" is not what either service variant does.** Wait is
   *bounded* (by N) and saw-toothed. The invariant in spec section 4.2 needs rewording to be testable.

---

## 1. CoDel

### CoDel, network form (Nichols & Jacobson 2012; RFC 8289)
- **Source:** RFC 8289, "Controlled Delay Active Queue Management", Jan 2018. ACM Queue article "Controlling Queue Delay" (2012).
- **Verified?** read directly - https://www.rfc-editor.org/rfc/rfc8289.txt (sections 4.2, 4.3, 5.3-5.6 pseudo-code). The ACM Queue page returned 403; a Wayback copy came back truncated, so the article itself is `secondary only` via The Morning Paper (https://blog.acolyer.org/2015/11/19/fail-at-scale-controlling-queue-delay/).
- **Key idea:** Measure each packet's *sojourn time* (dequeue time minus enqueue time), not queue length. If the sojourn time stays above TARGET for a whole INTERVAL, a standing queue exists; start dropping, and drop faster the longer it persists.
- **What Mini-ZGateway would concretely borrow (admission):** the sojourn-time signal and the constants. State: `first_above_time`, `dropping`, `drop_next`, `count`, `lastcount`. Constants: `TARGET = 5 ms`, `INTERVAL = 100 ms`. All work happens at dequeue:
  ```
  ok_to_drop(now):   # after popping p
    if sojourn < TARGET or queue nearly empty: first_above_time = 0; return false
    if first_above_time == 0: first_above_time = now + INTERVAL; return false
    return now >= first_above_time
  dequeue(now):
    if dropping:
      if !ok: dropping = false
      while now >= drop_next and dropping:
        drop(p); count++; p = pop(); if !ok: dropping = false
        else drop_next = drop_next + INTERVAL/sqrt(count)
    else if ok:
      drop(p); p = pop(); dropping = true
      delta = count - lastcount; count = 1
      if delta > 1 and now - drop_next < 16*INTERVAL: count = delta
      drop_next = now + INTERVAL/sqrt(count); lastcount = count
  ```
- **What NOT to copy:** the control law. `INTERVAL/sqrt(count)` exists because TCP throughput falls with `sqrt(drop probability)`; the RFC says so. Request clients do not back off per drop. Simulation *(computed this run; 20 servers x 50 ms = 400/s capacity, Poisson arrivals, waits of served requests)*: at 1.2x load p50 wait 58 ms; at 2x p50 730 ms; at 10x the queue never stopped growing (85,292 left after 30 s). Reaching a 400/s drop rate needs `count = 1600`, which takes about 8 s of ramp.
- **Invariant a test can assert:** none worth keeping for this form. **Classic bug:** computing sojourn at enqueue instead of dequeue; forgetting to reset `first_above_time` when the queue empties.
- **Strength:** BACKGROUND
- **Recommendation:** EXCLUDE (as the implemented algorithm) - keep the sojourn-time idea and the 5 ms / 100 ms constants only.

### CoDel, Facebook request-queue form ("Fail at Scale", Maurer 2015)
- **Source:** Ben Maurer, "Fail at Scale", ACM Queue vol. 13 issue 8, Oct 27 2015.
- **Verified?** read directly, archived copy - https://web.archive.org/web/2023id_/https://queue.acm.org/detail.cfm?id=2839461 (the live page returned 403).
- **Key idea:** Do not drop by a control law; set the queue *timeout* per request. If the queue has not been empty for the last N ms, a standing queue exists, so allow only M ms of waiting. Otherwise allow N ms. Quote: "a value of 5 milliseconds for M and 100 ms for N tends to work well across a wide set of use cases."
- **What Mini-ZGateway would concretely borrow (admission):** this is the algorithm to implement. State per queue: `lastEmptyTime` (int64 ns). Constants `N = 100 ms`, `M = 5 ms`.
  ```
  enqueue(req, now):
    if queue.empty(): lastEmptyTime = now
    timeout = (lastEmptyTime < now - N) ? M : N
    req.enq = now; req.deadline = now + timeout
    push(req)
  dequeue(now):
    loop: req = pop()            # FIFO or LIFO, see section 2
      if req == nil: lastEmptyTime = now; return nil
      if now > req.deadline: shed(req, OVERLOADED_PRE_FORWARD); continue
      return req
  ```
- **What NOT to copy:** the article's pseudo-code writes "N seconds" where the prose says milliseconds; use the prose. The article says nothing about how the timeout is enforced (timer per request vs. check at dequeue). Check at dequeue plus a sweep of the old end; a timer per request is unbounded work.
- **Invariant:** every *served* request waited `<= N`; every request enqueued in overload mode waited `<= M` or was shed; queue depth stays bounded by about `arrival_rate x N` however long overload lasts. Simulation *(computed this run)*: at 10x load, p50 wait 4.3 ms, max 100.0 ms, max depth 580, and all 12,000 service slots were used (no idle capacity). **Classic bug:** updating `lastEmptyTime` only at dequeue, so an idle queue looks "never empty" and the first burst after idle gets the 5 ms timeout.
- **Strength:** CORE
- **Recommendation:** ADOPT - simplest correct rule, one int64 of state, constants confirmed from the source.

### folly `Codel` (actual code and defaults)
- **Source:** `folly/executors/Codel.h` and `Codel.cpp`, facebook/folly `main`.
- **Verified?** read directly - https://raw.githubusercontent.com/facebook/folly/main/folly/executors/Codel.h and `.../Codel.cpp`.
- **Key idea:** Track the *minimum* queueing delay seen in the current interval. At each interval boundary, set `overloaded = (minDelay > targetDelay)`. While overloaded, shed any dequeued request whose own delay exceeds `2 x targetDelay`. The header says plainly that this replaces CoDel's `100/sqrt(n)` law because "a different scheme works better for services".
- **What Mini-ZGateway would concretely borrow (admission, observability):** defaults `codel_interval = 100` ms, `codel_target_delay = 5` ms, slough timeout `= 2 x target = 10 ms`. State: `minDelayNs`, `intervalEndNs`, `resetDelay` flag, `overloaded` flag. The first request of each interval only seeds `minDelay` and is never shed. Also borrow the load gauge: `load% = min(100, 100 x minDelay / (2 x target))` - a ready-made per-tenant metric.
  ```
  overloaded(delay, now):
    if now > intervalEnd and !reset:
      reset = true; intervalEnd = now + interval; over = (minDelay > target)
    if reset: reset = false; minDelay = delay; return false
    if delay < minDelay: minDelay = delay
    return over and delay > 2*target
  ```
- **What NOT to copy:** the atomics dance (it exists for many threads calling at once; a single-owner queue does not need it). **Do not pair this detector with LIFO dequeue.** Under LIFO the dequeued request is the freshest, so its delay is tiny and `minDelay` reports "healthy". In my simulation, folly detection plus LIFO order at 10x load shed **zero** requests and the queue grew without bound.
- **Invariant:** shed implies `over && delay > 2*target`; no shed in the first interval. Under sustained overload served-wait p50 sat at about 2 x target (9.3-9.9 ms in simulation), p99 55-107 ms.
- **Strength:** SECONDARY
- **Recommendation:** ADOPT WITH CHANGE - borrow the load gauge and the "never shed the first sample" rule; use the article's `lastEmptyTime` rule as the detector because it does not depend on dequeue order.

**Which one, and what "converges to target" means.** Use the Facebook `lastEmptyTime` form. Neither
service variant converges to 5 ms. The behaviour is a saw-tooth: overload mode sheds until the queue
empties, the empty queue resets `lastEmptyTime`, the next 100 ms of arrivals get the long timeout,
then overload mode returns. The testable properties, on the virtual clock, with offered load held
above capacity for at least `20 x N`:

1. Wait of every served request `<= N` (exact, no tolerance).
2. Max queue depth is the same for a run of length T and a run of length 4T (bounded, not growing).
3. Served count over the window `>= 0.95 x capacity x window` (shedding never idles the backend).
4. Against a plain FIFO control with the same seed, FIFO wait grows linearly with time; the CoDel
   queue's does not.

---

## 2. Adaptive LIFO

### Adaptive LIFO (Fail at Scale)
- **Source:** same article as above.
- **Verified?** read directly (archived copy, URL above).
- **Key idea:** FIFO in normal operation. "When a queue is starting to form, the server switches to LIFO mode", because the oldest request's caller has probably given up. CoDel's short timeout plus LIFO gives the newest request the best chance of meeting the timeout.
- **What Mini-ZGateway would concretely borrow (admission):** the article gives **no numeric switch rule**. Proposed rule, reusing the CoDel signal so there is one detector: `lifo = (lastEmptyTime < now - N)`. Pop from the tail when `lifo`, from the head otherwise. Because requests are appended in arrival order, the head is always the oldest, so expiry is a head sweep:
  ```
  pop(now):
    while head != nil and now > head.deadline: shed(popHead())   # O(1) amortised
    if empty: lastEmptyTime = now; return nil
    return (lastEmptyTime < now - N) ? popTail() : popHead()
  ```
  A ring buffer (deque) of fixed capacity `MaxQueueDepth` gives O(1) at both ends and no allocation. On overflow, shed the **head** (oldest), not the arrival.
- **What NOT to copy / where it misleads:** without the head sweep, LIFO buries stale requests at the bottom. Simulation *(computed this run)*: Facebook timeout + LIFO, shedding only when a request is popped, at 10x load gave excellent served waits (p50 0.2 ms) but 5,450 stale requests still queued at the end and a peak depth of 11,380. Each buried request also holds a client waiting for an answer that will be a shed anyway; answer it early.
- **Invariant:** in LIFO mode the served request is the newest non-expired one; no request stays queued past its deadline by more than one dequeue interval; depth `<= MaxQueueDepth`. **Classic bug:** the detector reads the delay of the dequeued item (see folly entry) and goes blind in LIFO mode.
- **Strength:** CORE
- **Recommendation:** ADOPT WITH CHANGE - add the explicit switch rule and the head sweep, neither of which the source specifies.

### folly `LifoSem` / `LifoSemMPMCQueue`
- **Source:** `folly/synchronization/LifoSem.h`, `folly/executors/task_queue/LifoSemMPMCQueue.h`.
- **Verified?** read directly - raw.githubusercontent.com/facebook/folly/main/... (both files).
- **Key idea:** A semaphore whose *waiters* are woken in roughly LIFO order "to maximize performance rather than fairness" (the most recently parked thread is cache-warm). The queue beside it is an ordinary bounded `MPMCQueue`; task order is not LIFO.
- **What Mini-ZGateway would concretely borrow:** nothing for request ordering. The same idea applies to idle pool connections: hand out the most recently used connection first so the rest go idle and can be reaped (pool).
- **What NOT to copy:** do not cite LifoSem as the Adaptive LIFO implementation. The article names HHVM for that; I did not read HHVM's code (`could not verify`).
- **Strength:** BACKGROUND
- **Recommendation:** EXCLUDE as an admission-queue influence; WATCH for MRU connection reuse.

### Dropbox Bandaid proxy
- **Source:** "Meet Bandaid, the Dropbox service proxy", dropbox.tech, 2018.
- **Verified?** read directly - https://dropbox.tech/infrastructure/meet-bandaid-the-dropbox-service-proxy
- **Key idea:** Bandaid "always processes requests in Last In, First Out (LIFO) order" - not adaptive - on the argument that an unloaded queue is empty, so order does not matter then. Requests that sit past a configurable timeout are failed without processing. A fixed-size worker pool caps upstream concurrency. Several queues per upstream have priority levels, and equal-priority queues are popped in "semi-randomly" shuffled order.
- **What Mini-ZGateway would concretely borrow (admission, fairness):** the structure is the same as ours: per-class queues, one dequeuer, a fixed concurrency limit. Always-LIFO is a legitimate simpler option. They also advise against dropping on queue *length* because a full queue cannot tell overload from a burst - the same argument CoDel makes.
- **What NOT to copy:** always-LIFO reorders a single client's pipelined requests whenever two are queued. For RESP, replies on one client connection must come back in request order, so ordering matters more for us than for an HTTP proxy.
- **Strength:** SECONDARY
- **Recommendation:** ADOPT WITH CHANGE - same architecture; keep FIFO as the normal mode.

**Interaction with per-tenant queues.** Keep CoDel state (`lastEmptyTime`, mode) **per tenant
queue**, the way FQ-CoDel keeps a CoDel instance per flow queue (RFC 8290, read directly). Then the
mechanism behind the fairness invariant is exact: a tenant at 50 QPS drains its queue constantly,
never enters overload mode, and keeps the 100 ms timeout; a flooding tenant's queue never empties,
so it gets the 5 ms timeout and LIFO. The flooder absorbs the shedding by construction.

---

## 3-4. Rate limiting

### GCRA (virtual scheduling form)
- **Source:** ITU-T I.371 and ATM Forum UNI 3.1 (originals); `throttled/throttled` `rate.go` (implementation).
- **Verified?** secondary only for the standard - https://en.wikipedia.org/wiki/Generic_cell_rate_algorithm, which quotes I.371. read directly for an implementation - https://raw.githubusercontent.com/throttled/throttled/master/rate.go. The ITU document itself: not fetched.
- **Key idea:** Keep one number per tenant, the Theoretical Arrival Time (TAT): when the next request "should" arrive if the tenant sent at exactly the allowed rate. A request conforms if it is not more than tau earlier than TAT. No bucket to refill; the clock does the refilling.
- **What Mini-ZGateway would concretely borrow (fairness):** state is one `int64` (TAT in ns on the injected clock). Constants: `T = 1e9 / rate` ns (emission interval), `tau = (burst - 1) x T` ns.
  ```
  allow(now, n=1):
    loop:
      old  = atomic.Load(tat)
      base = max(old, now)              # no credit builds up while idle
      new  = base + n*T
      if new - now > tau + T: return REJECT, retry_after = new - now - (tau + T)
      if atomic.CAS(tat, old, new): return ALLOW
  ```
  `throttled` uses the same test with `tolerance = period x (MaxBurst + 1)`, which is `tau + T` here. A rejected request leaves TAT unchanged (the quoted I.371 text says so).
- **Equivalence to a leaky bucket used as a meter:** bucket level `= max(TAT - now, 0)`, capacity `tau + T`, drain 1 ns per ns, each request adds T. Decisions are identical to a token bucket with rate `1/T` and burst `b = 1 + tau/T` when the token bucket's arithmetic is exact.
- **What NOT to copy:** the storage layer and CAS attempt cap (10) in `throttled` exist for a remote store. Integer division `1e9/rate` loses precision at high rates (rate 7,000/s gives T = 142,857 ns, about 1 ppm error; above 1e9/s T is 0). Reject such configurations.
- **Invariant:** in any window `[t1, t2]`, admitted `<= burst + rate x (t2 - t1)`; from idle, exactly `burst` back-to-back requests pass. **Classic bugs:** advancing TAT on reject (a flooding tenant then locks itself out indefinitely); `new = old + T` without the `max(old, now)` clamp (unbounded credit after idle); `tau = burst x T` (off by one); wall clock instead of a monotonic clock.
- **Strength:** CORE
- **Recommendation:** ADOPT - one atomic word per tenant, integer-only, so decision traces are bit-identical in simulation.

### Token bucket (lazy refill)
- **Source:** `golang.org/x/time/rate` (`rate.go`).
- **Verified?** read directly - https://raw.githubusercontent.com/golang/time/master/rate/rate.go (`advance`, `reserveN`).
- **Key idea:** A bucket holds up to `b` tokens and gains `r` per second. A request takes one token or is refused. Nothing runs in the background: on each call, add `elapsed x r` tokens, capped at `b`.
- **What Mini-ZGateway would concretely borrow (fairness, pool creation-rate cap, retry budget):** state `tokens`, `last`. `tokens = min(b, tokens + (now - last) x r); if tokens >= n: tokens -= n; allow`. To stay integer, store tokens scaled (for example in units of 1/1e9 token) or just use GCRA.
- **What NOT to copy:** `x/time/rate` keeps `tokens` as `float64` under a mutex and converts through `d.Seconds() * limit`. Floats are deterministic on one machine but invite cross-platform trace differences; it is also an external module, which the zero-dependency rule excludes.
- **Invariant:** same window bound as GCRA. A good differential test: feed the same random arrival sequence to GCRA and to an exact-integer token bucket and require identical decisions. **Classic bug:** refilling without the cap, or updating `last` on a rejected call with truncated elapsed time so that fractional tokens are lost and the effective rate drifts low.
- **Strength:** SECONDARY
- **Recommendation:** ADOPT WITH CHANGE - use it where the *meaning* is a budget with deposits (retry budget, connection-creation cap); use GCRA for per-tenant request rate.

**When GCRA vs token bucket matters here.** For unit-cost requests they make the same decisions.
GCRA wins on the hot path (one word, lock-free, integer). Token bucket wins where tokens are
deposited by events rather than by time (retry budget) or costs vary. The bigger point: **neither is
a fairness mechanism for a stalled backend.** A static 250 QPS tenant limit does nothing when a
200 ms stall cuts capacity to 100-160 QPS. Rate limiting is the outer gate; per-tenant queues with
round-robin dequeue and a per-tenant in-flight cap provide the isolation.

---

## 5. Fair queueing

### Deficit Round Robin (Shreedhar & Varghese 1995)
- **Source:** "Efficient Fair Queuing using Deficit Round Robin", SIGCOMM 1995.
- **Verified?** read directly, with a caveat - https://www2.cs.duke.edu/courses/spring09/cps214/papers/drr.pdf is a scan and the extracted text is garbled; I confirmed the mechanism against the pseudo-code at https://en.wikipedia.org/wiki/Deficit_round_robin. The O(1) condition (quantum at least the maximum packet size) is *from memory*.
- **Key idea:** One queue per flow and a list of non-empty queues. Each visit adds a fixed quantum to the queue's deficit counter; the queue may send while the head's size fits in the deficit. Unused deficit carries over, so large items are not starved, and work per item is constant.
- **What Mini-ZGateway would concretely borrow (fairness):** state per tenant: `queue`, `deficit`, `weight`; global: `activeList`.
  ```
  next():
    loop over activeList (round robin):
      t = current
      if t.inflight >= t.maxInflight: advance; continue
      t.deficit += quantum * t.weight
      while t.queue nonempty and cost(head) <= t.deficit:
        t.deficit -= cost(head); return pop(t)      # resume here next call
      if t.queue empty: t.deficit = 0; remove t from activeList
      advance
  ```
  With unit cost and quantum 1 this is plain round robin over active tenants. Keep the deficit field only if cost varies (an MGET of k keys costs k).
- **What NOT to copy:** byte-sized quanta and buffer stealing.
- **Invariant:** over any interval in which tenants i and j are both backlogged, `|served_i/w_i - served_j/w_j| <= quantum + maxCost`. **Classic bugs:** not zeroing the deficit when a queue empties (an idle tenant banks credit); quantum smaller than the largest cost (several passes per dequeue, O(1) lost); inserting a newly active tenant at the head (lets a tenant jump the line by draining and refilling).
- **Strength:** CORE
- **Recommendation:** ADOPT - O(1), integer state, fits "per-tenant queues in front of a fixed concurrency limit".

### WFQ / start-time fair queueing
- **Source:** Demers, Keshav & Shenker 1989; Goyal, Vin & Cheng 1996.
- **Verified?** could not verify (from memory). I fetched https://en.wikipedia.org/wiki/Fair_queuing but did not read it closely. The Kubernetes APF KEP (read directly) uses a virtual-time fair queueing variant for server requests.
- **Key idea:** Give each request a virtual start/finish tag and serve the smallest tag. This approximates bit-by-bit round robin more tightly than DRR, at the cost of a priority queue (O(log n)) and a virtual clock.
- **What Mini-ZGateway would concretely borrow:** nothing in v1.
- **What NOT to copy:** the virtual-time bookkeeping. With a handful of tenants and unit costs, the fairness gain over round robin is invisible.
- **Strength:** BACKGROUND
- **Recommendation:** EXCLUDE - more state and O(log n) for no measurable benefit at this scale.

### Stochastic fairness queueing / FQ-CoDel (RFC 8290)
- **Source:** McKenney 1990 (SFQ); RFC 8290 "The Flow Queue CoDel Packet Scheduler".
- **Verified?** read directly for RFC 8290 - https://www.rfc-editor.org/rfc/rfc8290.txt (default 1024 queues, quantum, new/old flow lists, per-queue CoDel state). McKenney's paper: could not verify (from memory).
- **Key idea:** When flows cannot be enumerated, hash each to one of a fixed number of queues and run DRR over the queues. Colliding flows share a queue. FQ-CoDel adds a CoDel instance per queue and gives newly active queues priority for one quantum.
- **What Mini-ZGateway would concretely borrow (admission, fairness):** two ideas: CoDel state per queue, and a "new flows" list so a quiet tenant's first request skips ahead of backlogged tenants.
- **What NOT to copy:** hashing. Our tenant id is explicit and the tenant count is small; a map from tenant to queue has no collisions. Still cap the number of tenant queues and send overflow to a shared default queue.
- **Strength:** SECONDARY
- **Recommendation:** ADOPT WITH CHANGE - per-queue CoDel state yes; hashing no.

### Kubernetes API Priority and Fairness - shuffle sharding
- **Source:** KEP-1040 "Priority and Fairness for API Server Requests".
- **Verified?** read directly - https://raw.githubusercontent.com/kubernetes/enhancements/master/keps/sig-api-machinery/1040-priority-and-fairness/README.md
- **Key idea:** Hash the flow id to deal a "hand" of H queues out of numQueues and enqueue on the one with the least work. A heavy flow fills only its own hand; a light flow almost always holds at least one queue the heavy flow does not. Example from the KEP: 128 queues and hand size 6 give about 5.4 billion possible hands.
- **What Mini-ZGateway would concretely borrow:** the vocabulary (concurrency "seats", queue length limit, least-work choice within the hand).
- **What NOT to copy:** the mechanism. It solves "far more flows than queues"; we have a queue per tenant.
- **Strength:** BACKGROUND
- **Recommendation:** WATCH ONLY - becomes relevant only if tenants are unbounded or untrusted.

**Which one fits.** Per-tenant bounded deques, an active list, DRR with quantum 1 (round robin),
a per-tenant in-flight cap, CoDel state per tenant queue. Dequeue is O(1). The dispatcher runs when
a concurrency slot frees.

---

## 6. Adaptive concurrency

### AIMD (Chiu & Jain 1989)
- **Source:** Chiu & Jain, "Analysis of the Increase and Decrease Algorithms for Congestion Avoidance in Computer Networks", 1989; Netflix `concurrency-limits` `AIMDLimit.java`.
- **Verified?** could not verify the paper - https://www.cse.wustl.edu/~jain/papers/ftp/cong_av.pdf downloaded but is a scan with no text layer. read directly for the implementation - https://raw.githubusercontent.com/Netflix/concurrency-limits/main/concurrency-limits-core/src/main/java/com/netflix/concurrency/limits/limit/AIMDLimit.java
- **Key idea:** Add a constant on success, multiply by a factor below 1 on a congestion signal. The paper's result *(from memory)* is that among linear controls only this pairing converges to both efficiency and fairness.
- **What Mini-ZGateway would concretely borrow (concurrency-limit):** the Netflix rule, verbatim:
  ```
  on_sample(rtt, inflight, didDrop):
    if didDrop or rtt > timeout: limit = floor(limit * backoffRatio)
    else if inflight * 2 >= limit: limit = limit + 1
    limit = clamp(limit, minLimit, maxLimit)
  ```
  Netflix defaults: `initialLimit 20`, `minLimit 20`, `maxLimit 200`, `backoffRatio 0.9` (must be in [0.5, 1.0)), `timeout 5 s`.
- **What NOT to copy:** the 5 s timeout default; for a Redis proxy a latency threshold of tens of milliseconds is the right order. A minimum of 20 is too high for a 20-connection pool; use 1 or 2.
- **Invariant:** `min <= limit <= max` always; after a step from 50 ms to 200 ms backend latency, the limit is non-increasing until latency recovers, then rises by at most 1 per completed request. **Classic bug:** raising the limit when the limiter is not the constraint (the `inflight*2 >= limit` guard exists for exactly this - without it the limit drifts to max during quiet periods and is useless when the stall arrives).
- **Strength:** CORE
- **Recommendation:** ADOPT - the spec's default controller; four lines and one integer.

### TCP Vegas (Brakmo & Peterson 1994) and Netflix `VegasLimit`
- **Source:** "TCP Vegas: New Techniques for Congestion Detection and Avoidance", SIGCOMM 1994; Netflix `VegasLimit.java`.
- **Verified?** read directly - paper at https://www.cs.princeton.edu/courses/archive/fall06/cos561/papers/vegas.pdf (section on congestion avoidance; the Greek letters were lost in text extraction), and https://raw.githubusercontent.com/Netflix/concurrency-limits/main/.../limit/VegasLimit.java
- **Key idea:** Compare what throughput *should* be if nothing were queued with what it is. Paper: `Expected = WindowSize / BaseRTT`, `Diff = Expected - Actual`; grow the window linearly when Diff is below alpha, shrink when above beta, hold in between. BaseRTT is the minimum RTT seen.
- **What Mini-ZGateway would concretely borrow (concurrency-limit):** the queue estimate in Netflix's form: `queue = ceil(limit x (1 - rtt_noload / rtt))`. Netflix defaults: `alpha = 3 x log10(limit)`, `beta = 6 x log10(limit)`, `threshold = log10(limit)`, step `log10(limit)`, `probeMultiplier 30` (re-measure the no-load RTT after about `30 x limit` samples, jittered by 0.5-1.0). The README gives the classic values: alpha 2-3, beta 4-6.
- **What NOT to copy:** the minimum-RTT baseline. Our "RTT" includes batch wait and pipeline position, so the minimum is biased low and the controller shrinks the limit for no reason. If the backend becomes permanently slower, a never-refreshed minimum pins the limit at the floor - which is why Netflix probes.
- **Invariant:** with constant RTT equal to the baseline the limit never decreases. **Classic bug:** stale minimum RTT.
- **Strength:** SECONDARY
- **Recommendation:** WATCH ONLY - understand it; implement AIMD first.

### Netflix gradient limit
- **Source:** Netflix Tech Blog, "Performance Under Load" (2018); `GradientLimit.java`, `Gradient2Limit.java`.
- **Verified?** read directly - blog via local renderer (https://netflixtechblog.medium.com/performance-under-load-3e6fa9a60581) and both Java files on raw.githubusercontent.com.
- **Key idea:** `gradient = RTT_noload / RTT_actual`; `newLimit = currentLimit x gradient + queueSize`. A gradient of 1 means no queueing, so the limit grows by `queueSize`; below 1 it shrinks in proportion. The blog's default queue allowance is the square root of the limit. This matches the formula in the brief.
- **What Mini-ZGateway would concretely borrow (concurrency-limit, optional upgrade):** Gradient2, which replaces the minimum with a long-window average:
  ```
  on_sample(rtt, inflight):
    long = ema_long.add(rtt)                    # window 600 samples
    if long / rtt > 2: long = long * 0.95       # recover fast after a long bad period
    if inflight < limit / 2: return             # app-limited: do not grow
    g = clamp(tolerance * long / rtt, 0.5, 1.0) # tolerance 1.5
    new = limit * g + queueSize                 # queueSize default 4
    limit = clamp(limit*(1 - 0.2) + new*0.2, minLimit, maxLimit)   # smoothing 0.2
  ```
  Defaults read from source: initial 20, min 20, max 200.
- **What NOT to copy:** floating point in the decision path if traces must be bit-identical across platforms; use fixed-point (for example limit x 1024). Gradient v1's periodic "cut limit and re-probe" causes visible latency dips.
- **Invariant:** `g` in [0.5, 1.0], so one sample can never more than halve the limit; limit within bounds.
- **Strength:** SECONDARY
- **Recommendation:** ADOPT WITH CHANGE - as the optional upgrade after AIMD, in fixed-point.

### Little's Law - sizing arithmetic for the spec's numbers
- **Source:** Little 1961, `L = lambda x W`.
- **Verified?** secondary only - stated in The Morning Paper post read this run (URL above); https://en.wikipedia.org/wiki/Little%27s_law fetched, not read closely. The arithmetic below is mine.
- **Key idea:** Average number in a system equals arrival rate times average time in the system. Turned around, a concurrency limit L and a service time W cap throughput at `L / W`.
- **What Mini-ZGateway would concretely borrow (pool, concurrency-limit, benchmarking) - worked numbers, one request in flight per connection:**

  | Case | Arithmetic | Result |
  |---|---|---|
  | 20 connections, 50 ms | 20 / 0.050 | 400 QPS max |
  | 32 connections, 50 ms | 32 / 0.050 | 640 QPS max |
  | Same, during 200 ms stall | 20 / 0.2, 32 / 0.2 | 100 and 160 QPS |
  | Tenant A 5,000 + B 50 QPS at 50 ms | 5,050 / 400, 5,050 / 640 | 12.6x and 7.9x overload |
  | Same during the stall | 5,050 / 100, 5,050 / 160 | 50x and 32x overload |
  | Slots B needs at 50 ms | 50 x 0.050 | 2.5 in flight |
  | Slots B needs during stall | 50 x 0.200 | **10 in flight** |
  | Waiting requests at 5 ms wait, 400 QPS | 400 x 0.005 | 2 queued |
  | Waiting requests at 100 ms wait, 400 QPS | 400 x 0.100 | 40 queued |
  | 1,000 hot-key reads, no coalescing, 20 conns | 1,000 x 0.050 / 20 | last reply at about 2.5 s |
  | Same with coalescing | one backend read | about 50 ms |
  | Fan-in: 500 clients to 32 / 20 conns | 500 / 32, 500 / 20 | 15.6x and 25x |

  Consequences. (a) The spec's "about 15x reduction" matches a pool of 32, not 20. (b) During the 200 ms stall, B needs 10 slots: exactly half of a 20-connection pool. With two tenants and equal shares B's fair share is 10, so B is protected with **zero margin** at pool 20 and with margin at 32 (share 16). (c) `MaxQueueDepth` on the order of `capacity x N` (40-64 per tenant) is enough; a depth in the thousands only stores requests that will be shed.
- **What NOT to copy:** these numbers assume one in-flight request per connection. With RESP pipelining, in-flight is per connection times pipeline depth; the in-flight limit and the pool size are then **different knobs**. Real Redis service time is well under 1 ms, so 50 ms exists only as injected latency.
- **Strength:** CORE
- **Recommendation:** ADOPT - as the sizing identity and as test oracles (throughput must equal `min(offered, L/W)` in simulation).

---

## 7. Balls into bins and power of two choices

### Balls into bins / power of two choices (Azar et al. 1994; Mitzenmacher)
- **Source:** Mitzenmacher, Richa & Sitaraman, "The Power of Two Random Choices: A Survey of Techniques and Results" (2001); Azar, Broder, Karlin & Upfal.
- **Verified?** read directly for the survey's introduction - https://www.eecs.harvard.edu/~michaelm/postscripts/handbook2001.pdf. Also https://en.wikipedia.org/wiki/Balls_into_bins_problem. Azar et al.'s original paper: not fetched.
- **Key idea:** Throw n balls into n bins at random and the fullest bin holds about `log n / log log n` balls. Let each ball look at d >= 2 random bins and take the emptier one and the maximum drops to `log log n / log d + Theta(1)` - an exponential improvement from one extra probe. (The survey writes `log`; the brief's `ln n / ln ln n` vs `ln ln n / ln 2` is the same statement.) Computed for scale: n = 32 gives 2.79 vs 1.79; n = 1,000 gives 3.57 vs 2.79.
- **The fan-in/fan-out connection model.** Direct access: N clients each holding k connections put `N x k` connections on the backend tier. With M backends and random choice, one client reaches `M x (1 - (1 - 1/M)^k)` distinct backends *(computed: k = 4, M = 32 gives 3.82; k = 100, M = 100 gives 63.4)*, and each backend sees about `N x k / M` connections. Through a proxy tier of P instances the backend sees `P x pool`. I did **not** read the Meta ZGateway post in this bucket (one search attempt returned nothing relevant), so I cannot say this is exactly Meta's model: `could not verify`.
- **Does P2C matter with ONE backend?** No, not at the backend level: with one bin the model is degenerate and the count is just `P x pool`. The only place a choice exists is *which pool connection* gets the next pipelined request. With 20-32 connections an exact least-pending scan is cheap, so P2C buys nothing. What does matter: do not assign by random or hash, because random placement leaves some connections with deep pipelines while others idle. P2C earns its place only in Variant C (choosing among replicas).
- **Invariant (if least-pending is used):** `max(pending) - min(pending) <= 1` across healthy connections under steady load.
- **Strength:** BACKGROUND (A, B); SECONDARY (C)
- **Recommendation:** WATCH ONLY - use exact least-pending in v1; revisit P2C for Variant C.

---

## 8. Load generation

### Coordinated omission (Tene; wrk2)
- **Source:** Gil Tene, "How NOT to Measure Latency" (talk); wrk2 README; HdrHistogram README.
- **Verified?** read directly for wrk2 - https://raw.githubusercontent.com/giltene/wrk2/master/README.md - and for the correction - HdrHistogram README plus `RecordCorrectedValue` in https://raw.githubusercontent.com/HdrHistogram/hdrhistogram-go/master/hdr.go. The talk: secondary only (InfoQ abstract page; video not watched).
- **Key idea:** A generator that waits for a response before sending the next request slows down exactly when the server is slow, so the slow period is under-sampled. wrk2: measure "from the time the transmission *should* have occurred according to the constant throughput configured for the run".
- **What Mini-ZGateway would concretely borrow (benchmarking):** the open-loop rule.
  ```
  intended[i] = t0 + i/rate                 # constant; or t0 + sum of Exp(rate) draws for Poisson
  at each tick: send every request whose intended <= now, never waiting on replies
  on reply:  latency = now - intended[i]    # the headline number
             service = now - actual_send[i] # also recorded, labelled "uncorrected"
  report achieved rate vs intended rate; count requests the generator could not send
  ```
  RESP is pipelinable, so a fully asynchronous generator is feasible (wrk2 notes that this only works for non-blocking protocols).
- **What NOT to copy:** HdrHistogram's expected-interval back-fill (`v - interval, v - 2 x interval, ...`) when you already record from intended time; doing both counts the stall twice. wrk2's calibration period and thread model.
- **Invariant:** number of latency samples equals number of intended sends (minus an explicit "not sent" counter); the schedule is a pure function of seed and rate. **Classic bug:** scheduling the next send relative to the previous *actual* send or reply (`sleep(1/rate)` in a loop).
- **Strength:** CORE
- **Recommendation:** ADOPT - record from intended time; keep the uncorrected series beside it.

### Open versus closed systems (Schroeder, Wierman & Harchol-Balter, NSDI 2006)
- **Source:** "Open Versus Closed: A Cautionary Tale", NSDI 2006.
- **Verified?** read directly, partially - https://www.usenix.org/legacy/event/nsdi06/tech/full_papers/schroeder/schroeder.pdf (the eight numbered principles; two-column extraction cut some sentences).
- **Key idea:** A closed generator (fixed number of users, each waiting for its reply) and an open one (arrivals independent of completions) give very different response times at the same load, and scheduling policy matters far more in open systems. Real systems are "partly open".
- **What Mini-ZGateway would concretely borrow (benchmarking):** label every scenario. Noisy neighbour, hot key and retry storm are open-loop. The reconnection storm (500 clients) is naturally closed. Use **Poisson** arrivals for tenant traffic (many independent callers) and **constant** spacing for calibration and A/A runs (least variance). State which in every result table.
- **What NOT to copy:** nothing to avoid; the caution is ours: `redis-benchmark` is a closed-loop tool, so its numbers cannot show the shedding results.
- **Strength:** SECONDARY
- **Recommendation:** ADOPT - as a labelling rule in `cmd/loadgen`.

---

## 9. Histograms

### HDR Histogram (Tene)
- **Source:** HdrHistogram README; `hdrhistogram-go` `hdr.go`.
- **Verified?** read directly - https://raw.githubusercontent.com/HdrHistogram/HdrHistogram/master/README.md and the Go port's index functions (URL above).
- **Key idea:** Buckets like a floating-point number: exponentially growing ranges, each split into a fixed number of equal sub-buckets. That gives a fixed relative error (2 or 3 significant digits) over a huge range, in a fixed-size array of counters, with no allocation on record.
- **What Mini-ZGateway would concretely borrow (observability, benchmarking):** the layout. `subBucketCount = nextPow2(2 x 10^digits)` (256 for 2 digits, 2,048 for 3).
  ```
  record(v):                                   # v in the lowest unit, e.g. microseconds
    b   = 64 - clz(v | subBucketMask) - unitMagnitude - (subBucketHalfCountMagnitude + 1)
    sub = v >> (b + unitMagnitude)
    idx = ((b + 1) << subBucketHalfCountMagnitude) + sub - subBucketHalfCount
    atomic.Add(&counts[idx], 1)
  merge(a, b): same geometry -> add counts element by element
  ```
  Memory for 1 us to 60 s *(computed this run from the Go/Java geometry)*: 2 digits = 2,560 counters = **20.5 KB** with int64; 3 digits = 17,408 counters = **139 KB**. Record cost per the README: 3-6 ns (2012 Intel). From-scratch size: my estimate is 150-250 lines of Go for record, merge, percentile and reset; the Go port's single file is 1,184 lines including encoding and iterators.
- **What NOT to copy:** auto-resizing, the wire encoding, double histograms, the packed variant. Per tenant x outcome x metric, 3 digits gets heavy: 100 tenants x 2 outcomes x 2 metrics x 139 KB = 56 MB, against 8 MB at 2 digits.
- **Invariant:** for any recorded v within range, `|valueAt(quantile) - true| / true <= 10^-digits`; `total count == sum(counts)`; `merge(a, b)` percentiles equal those of recording both streams into one. **Classic bugs:** values above the maximum silently dropped (clamp and count them); percentile returning the bucket's lowest rather than highest equivalent value; reading counts while writers run and reporting a total that does not match.
- **Strength:** CORE
- **Recommendation:** ADOPT WITH CHANGE - write a minimal integer-only version; 2 digits per tenant inside the proxy, 3 digits in the load generator.

### DDSketch, t-digest, Prometheus native histograms
- **Source:** Masson, Rim & Lee, "DDSketch" (VLDB 2019); Dunning's t-digest; Prometheus native histograms specification.
- **Verified?** secondary only - arXiv abstract (https://arxiv.org/abs/1908.10693), t-digest README (https://raw.githubusercontent.com/tdunning/t-digest/main/README.md), introduction of https://prometheus.io/docs/specs/native_histograms/. None read in depth.
- **Key idea:** DDSketch: logarithmic buckets with a relative-error guarantee, fully mergeable. t-digest: clusters of centroids, very accurate at extreme quantiles ("part per million"), mergeable, but rank-error-based and floating point. Prometheus native histograms: sparse exponential buckets as an exposition format.
- **What Mini-ZGateway would concretely borrow:** nothing in v1. DDSketch is the fallback if the range must be unbounded.
- **What NOT to copy:** t-digest's result depends on insertion and merge order, which fights the bit-identical replay law. Native histograms are a wire format for a scraper, not a recording structure.
- **Strength:** BACKGROUND
- **Recommendation:** EXCLUDE - one from-scratch HDR-style histogram covers every need; expose fixed classic Prometheus buckets derived from it if a `/metrics` endpoint is wanted.

---

## 10. Benchmark statistics

### benchstat (Mann-Whitney U)
- **Source:** `golang.org/x/perf/cmd/benchstat` documentation.
- **Verified?** read directly - https://pkg.go.dev/golang.org/x/perf/cmd/benchstat
- **Key idea:** For each benchmark, report the median and a 95% confidence interval for the median, and compare before/after with the "Mann-Whitney U-test"; a "~" means no significant difference. "Each benchmark should be run at least 10 times"; "Pick a number of benchmark runs (at least 10, ideally 20) and stick to it"; "interleave before and after runs".
- **What Mini-ZGateway would concretely borrow (benchmarking):** emit results in Go benchmark text format so benchstat can read them; run each configuration at least 10 times, interleaved A B A B.
- **What NOT to copy:** treating p < 0.05 as "matters" - the docs warn that significant is not the same as large. benchstat is an optional external *tool*; do not import it into the build.
- **Strength:** CORE
- **Recommendation:** ADOPT - as the comparison format and the minimum run count.

### Kalibera & Jones; Georges et al.; Mytkowicz et al.
- **Source:** "Rigorous Benchmarking in Reasonable Time" (ISMM 2013); "Statistically Rigorous Java Performance Evaluation" (OOPSLA 2007); "Producing Wrong Data Without Doing Anything Obviously Wrong!" (ASPLOS 2009).
- **Verified?** read directly, abstract and introduction plus keyword passages only - https://kar.kent.ac.uk/33611/45/p63-kaliber.pdf, https://dri.es/files/oopsla07-georges.pdf, https://users.cs.northwestern.edu/~robby/courses/322-2013-spring/mytkowicz-wrong-data.pdf
- **Key idea:** Kalibera & Jones: variation lives at several levels (iteration, process run, build); repeat most at the level that varies most, and report effect sizes with confidence intervals. Georges: report a mean with a confidence interval, not "best of 30"; declare steady state when the coefficient of variation of the last k measurements falls below "0.01 or 0.02". Mytkowicz: innocuous setup details - UNIX environment size, link order - bias results enough to flip conclusions; randomise the setup.
- **What Mini-ZGateway would concretely borrow (benchmarking):** the unit of repetition is the **run** (fresh proxy process, fresh backend state), not the request. Discard warm-up. Keep the environment and binary fixed within a comparison and randomise run order.
- **What NOT to copy:** the full multi-level variance analysis. Bootstrap confidence intervals: recommended, but I read no bootstrap source this run (*from memory*): resample runs with replacement, recompute the median difference, take the 2.5th and 97.5th percentiles.
- **Strength:** SECONDARY
- **Recommendation:** ADOPT WITH CHANGE - the principles, not the machinery.

### Apollo GraphQL, "How we know a router performance result is real"
- **Source:** Apollo blog, Dan Anderson Hall, dated October 7, 2026.
- **Verified?** read directly - https://www.apollographql.com/blog/how-we-know-a-router-performance-result-is-real . **The post exists as the spec describes** (title, month, topic).
- **Key idea:** Three gates in order. Gate 1, is the run valid: the generator delivered within 2% of its target rate, no metric drifting more than 2% per 4 minutes, CPU throttled less than 5% of the time. Gate 2, do repeats agree: coefficient of variation below 15% for most single numbers; latency distributions compared by Wasserstein distance. Gate 3, did it change: an equivalence zone of +/-10% per percentile and +/-5% for single numbers, with four verdicts (equivalent, regression, improvement, inconclusive). They also report that seeding the mock cut run-to-run variation from 244% to about 11%, that five repeats was too few to judge p99, and that a negative control moved less than 0.5%.
- **What Mini-ZGateway would concretely borrow (benchmarking):** the gate structure and the "inconclusive" verdict. Gate 1's achieved-rate check is the open-loop generator's self-test.
- **What NOT to copy:** separate nodes for generator and system under test (vm-1 is one VM), 500-run matrices, Wasserstein distance.
- **Strength:** CORE
- **Recommendation:** ADOPT WITH CHANGE - three gates, scaled down.

**Minimum credible `make bench` protocol for vm-1** (one noisy VM, no hardware counters, generator
shares the machine):

1. **Simulation first.** Reproduce each policy comparison (FIFO vs CoDel, coalescing on/off) on the
   virtual clock. It is bit-identical, so it needs no statistics. This is the primary evidence.
2. **A/A before A/B.** Run the same binary as both arms. The verdict must be "no difference". The
   A/A spread is the noise floor; publish it.
3. **At least 10 runs per arm, interleaved**, fixed duration, warm-up discarded, fixed seeds, fresh
   backend state per run.
4. **Gate 1 per run:** achieved rate within 2% of intended, generator "not sent" count zero.
   Otherwise discard the run and say so.
5. **Report** the median across runs with a 95% interval for p50, p99 and p99.9, plus the raw
   per-run values. Compare with Mann-Whitney U. Call a result "inconclusive" when the interval
   straddles a pre-declared zone.
6. Separate the generator from the proxy and Redis with `taskset` and `GOMAXPROCS`, and state it.
7. Wall-clock time only. Do not claim CPU-level effects.

The headline effects are large (15x connections, 1,000 reads to 1, seconds to milliseconds), so they
survive this protocol easily. The statistics matter for the small number: proxy overhead at idle.

---

## 11. Overload theory

### Metastable failures (Bronson et al. HotOS 2021; Huang et al. OSDI 2022)
- **Source:** "Metastable Failures in Distributed Systems"; "Metastable Failures in the Wild".
- **Verified?** read directly, abstract and introduction - https://sigops.org/s/conferences/hotos/2021/papers/hotos21-s11-bronson.pdf and https://www.usenix.org/system/files/osdi22-huang-lexiang.pdf
- **Key idea:** A system in a *vulnerable* state is hit by a *trigger* and enters a bad state that persists "even when the trigger is removed", held there by a *sustaining effect* (a feedback loop such as retries) while goodput stays unusably low. Huang et al. classify two kinds of trigger and two kinds of amplification.
- **What Mini-ZGateway would concretely borrow (testing, errors+retries):** a recovery invariant for every chaos scenario: *after the fault is removed, goodput returns to at least 99% of its pre-fault value within a stated virtual-time bound, and queue depth returns to baseline.* Also the warning that optimisations create hidden capacity: a backend sized for coalesced traffic is in the vulnerable state if the coalescer is bypassed.
- **What NOT to copy:** fleet-scale mitigation (load-shedding tiers, circuit breakers across services).
- **Strength:** CORE
- **Recommendation:** ADOPT - as one more invariant, cheap to assert in the simulator.

### Retry storms and retry budgets (Google SRE book; Finagle)
- **Source:** SRE book chapter 21 "Handling Overload"; Finagle `RetryBudget.scala`.
- **Verified?** read directly - https://sre.google/sre-book/handling-overload/ and https://raw.githubusercontent.com/twitter/finagle/develop/finagle-core/src/main/scala/com/twitter/finagle/service/RetryBudget.scala
- **Key idea:** SRE book: "a per-request retry budget of up to three attempts" and "a per-client retry budget... A request will only be retried as long as this ratio is below 10%." It also describes client-side adaptive throttling from `requests` and `accepts` over two minutes with multiplier K = 2 (I saw the text; the probability formula is an image I could not read - *from memory*: `max(0, (requests - K x accepts) / (requests + 1))`). Finagle: a token bucket where each request deposits and each retry withdraws; defaults `ttl 10 s`, `minRetriesPerSec 10`, `percentCanRetry 0.2`.
- **What Mini-ZGateway would concretely borrow (errors+retries):** an integer token bucket: each first attempt deposits 1; a retry withdraws 10 (a 10% budget, the spec's figure); balance capped. Rule: retry only pre-forward failures; never retry a write after any byte reached the backend.
  ```
  on_first_attempt(): budget = min(cap, budget + 1)
  may_retry(req): return req.preForward and req.attempts < 2 and take(budget, 10)
  ```
- **What NOT to copy:** Finagle's 20% default and its floor of 10 retries/s (at low traffic the floor is the whole budget); the two-minute window.
- **Invariant:** `retries <= 0.1 x first_attempts + cap/10` over any window; zero retries of post-forward writes. **Classic bug:** counting retries as deposits, which makes the budget self-funding.
- **Strength:** CORE
- **Recommendation:** ADOPT WITH CHANGE - the deposit/withdraw bucket at 10%.

### The Tail at Scale - hedged requests (Dean & Barroso 2013)
- **Source:** CACM, Feb 2013.
- **Verified?** read directly - https://cacm.acm.org/research/the-tail-at-scale/ (via local renderer; plain fetch returned 403).
- **Key idea:** Send a second copy of a request to another replica if the first has been outstanding longer than the 95th-percentile latency; this "limits the additional load to approximately 5%" and shortens the tail. "Tied requests" add cross-server cancellation.
- **What Mini-ZGateway would concretely borrow:** nothing in Variants A and B.
- **What NOT to copy:** hedging against ONE backend sends duplicate work to the very server that is slow. It is retry amplification under another name and conflicts directly with the proxy's no-amplification stance.
- **Strength:** BACKGROUND
- **Recommendation:** EXCLUDE (A, B); WATCH ONLY for Variant C, reads only, charged to the retry budget.

### Zipf generation (Go `math/rand`; YCSB / Gray et al.)
- **Source:** Go `src/math/rand/zipf.go`; YCSB `ZipfianGenerator.java`.
- **Verified?** read directly - https://raw.githubusercontent.com/golang/go/master/src/math/rand/zipf.go and https://raw.githubusercontent.com/brianfrankcooper/YCSB/master/core/src/main/java/site/ycsb/generator/ZipfianGenerator.java. Also confirmed locally with `go doc math/rand/v2.NewZipf` (Go 1.26.8).
- **Key idea:** Go: `NewZipf(r, s, v, imax)` draws k in [0, imax] with `P(k)` proportional to `(v + k)^(-s)`, **requires `s > 1` and `v >= 1`**, and uses Hormann-Derflinger rejection-inversion. YCSB: default constant `0.99` (below 1, so Go cannot reproduce it), Gray et al.'s method: precompute `zetan = sum(1/i^theta)`, `alpha = 1/(1 - theta)`, `eta = (1 - (2/n)^(1 - theta)) / (1 - zeta(2)/zetan)`; then with uniform u: `u x zetan < 1` gives item 0; `< 1 + 0.5^theta` gives item 1; otherwise `n x (eta x u - eta + 1)^alpha`.
- **What Mini-ZGateway would concretely borrow (benchmarking, testing):** `rand.NewZipf` from the standard library with a seeded source is enough and deterministic.
- **What NOT to copy / where the spec misleads:** "99/1 Zipfian" is not a Zipf parameter. *(Computed this run)* share of traffic on the hottest 1% of keys: with 100,000 keys, s = 1.01 gives 63%, s = 1.2 gives 85%, s = 1.5 gives 98%; YCSB's 0.99 gives 61%. Getting 99% onto 1% of keys needs s of roughly 1.5 or more, at which point one key alone takes 38% of traffic. If "99/1" is meant literally, a two-class hot-set generator (99% of requests uniform over 1% of keys) states it exactly.
- **Invariant:** same seed gives the same key sequence; the empirical top-1% share matches the computed value within a tolerance.
- **Strength:** CORE
- **Recommendation:** ADOPT WITH CHANGE - standard-library Zipf, with the exponent and the resulting top-1% share printed in every report.

### XFetch - probabilistic early expiration (Vattani, Chierichetti & Lowenstein, VLDB 2015)
- **Source:** "Optimal Probabilistic Cache Stampede Prevention".
- **Verified?** read directly - https://cseweb.ucsd.edu/~avattani/papers/cache_stampede.pdf (the algorithm listing).
- **Key idea:** Recompute a cached value slightly *before* it expires, with a probability that rises as expiry nears: recompute if `Time() - delta x beta x log(rand()) >= expiry`, where delta is the measured recompute time and beta defaults to 1.
- **What Mini-ZGateway would concretely borrow:** nothing in Variant A or B. In-flight coalescing already handles a stampede on a key with no cache.
- **What NOT to copy:** any TTL cache in v1; it brings staleness and invalidation, which the spec confines to Variant C.
- **Strength:** BACKGROUND
- **Recommendation:** EXCLUDE (A, B); WATCH ONLY for Variant C.

### Smart batching (Martin Thompson) and Nagle-style batching
- **Source:** Mechanical Sympathy blog, "Smart Batching" (2011). Nagle's algorithm (RFC 896).
- **Verified?** read directly for smart batching - https://mechanical-sympathy.blogspot.com/2011/10/smart-batching.html. Nagle: could not verify (from memory).
- **Key idea:** No timer. One writer waits for the first item and then drains whatever else queued while the previous write was in progress: "wait for data to become available and as soon as it is, send it right away." At low load a batch is one item with no added delay; under load batches grow on their own. His example: 10 messages at 100 us per write average 190 us worst case batched, against a serial queue.
- **What Mini-ZGateway would concretely borrow (batching):** the flush rule, per backend connection writer.
  ```
  loop:
    first = queue.take()                      # blocks; idle latency unaffected
    batch = [first]
    while len(batch) < maxBatch and bytes < maxBytes and (r = queue.poll()) != nil:
      batch.append(r)
    write(encode(batch))                      # one syscall
  ```
  This meets the spec's "flush immediately when full or under low load" by construction, and has no timer events to simulate.
- **What NOT to copy:** Thompson's own caveat - an unbounded queue gives no back-pressure; bound it. A fixed 200 us-1 ms window (the spec's example) adds latency at low load unless guarded; treat it as an optional experiment. Nagle on the socket would add a second, hidden batching delay; keep it off (*from memory*: Go enables TCP_NODELAY by default).
- **Invariant:** batch size `<= maxBatch`; replies are matched to requests in order; with one request in the system, time from enqueue to write is zero on the virtual clock.
- **Strength:** CORE
- **Recommendation:** ADOPT - timer-free batching as the default flush rule.

### Redis pipelining economics
- **Source:** Redis documentation, "Redis pipelining".
- **Verified?** read directly - https://redis.io/docs/latest/develop/using-commands/pipelining/
- **Key idea:** Pipelining saves round trips and, more importantly, system calls: serving a command is cheap but "very costly from the point of view of doing the socket I/O... calling the read() and write() syscall". The server must queue replies in memory, so send in bounded batches; the page's example is 10k commands.
- **What Mini-ZGateway would concretely borrow (batching, pool):** many client requests share one backend connection and one `write`; replies return in order, so the per-connection state is a FIFO of pending waiters. Cap pipeline depth per connection.
- **What NOT to copy:** 10k as a batch size for a latency-sensitive proxy; a slow command ahead of you delays every reply behind it (head-of-line blocking). Blocking commands, MULTI/EXEC and pub/sub cannot share a multiplexed connection (*from memory*).
- **Invariant:** the i-th reply on a connection is delivered to the i-th pending waiter; pending depth `<= maxPipeline`.
- **Strength:** CORE (Variant A)
- **Recommendation:** ADOPT - this is the multiplexing model for Variant A.

---

## 12. Deterministic simulation testing

### FoundationDB simulation
- **Source:** FoundationDB documentation, "Simulation and Testing"; Will Wilson, "Testing Distributed Systems w/ Deterministic Simulation" (Strange Loop 2014).
- **Verified?** read directly for the documentation - https://apple.github.io/foundationdb/testing.html. The talk: could not verify (video, not watched).
- **Key idea:** Run the whole system "within a single-threaded process" on simulated time, network and disks, so that any run is perfectly repeatable from its seed; then inject failures far more aggressively than production would. The docs describe a "swizzle-clogging" fault pattern: stop a random subset of connections one by one, then restore them in random order.
- **What Mini-ZGateway would concretely borrow (testing):** single thread, one seed, simulated time, and the clog/unclog schedule as a ready-made fault pattern for the virtual backend.
- **What NOT to copy:** a custom language runtime (Flow). Our equivalent is discipline: core logic as plain state machines with no goroutines and no real I/O.
- **Strength:** CORE
- **Recommendation:** ADOPT - the model for `internal/sim`.

### TigerBeetle VOPR, Antithesis, turmoil, madsim, sled
- **Source:** TigerBeetle docs; Antithesis docs; `tokio-rs/turmoil` and `madsim-rs/madsim` READMEs.
- **Verified?** read directly - https://raw.githubusercontent.com/tigerbeetle/tigerbeetle/main/docs/internals/vopr.md, https://antithesis.com/docs/resources/deterministic_simulation_testing/, and both READMEs on raw.githubusercontent.com. The sled simulation guide: not fetched (madsim cites it).
- **Key idea:** TigerBeetle: the simulator is "deterministic based on a seed number and the Git commit" and "can speed up time arbitrarily". Antithesis: make "clocks, thread interleaving, and system-provided sources of randomness" deterministic - done below the application with a deterministic hypervisor. turmoil runs "multiple concurrent hosts within a single thread"; madsim replaces the async runtime.
- **What Mini-ZGateway would concretely borrow (testing):** the reproduction recipe "seed + commit" printed on every failure, and the list of non-determinism sources to close: clock, interleaving, randomness.
- **What NOT to copy:** the tools themselves - all are Rust, Zig or a commercial service. Runtime-swapping does not carry over to Go, whose scheduler cannot be replaced.
- **Strength:** BACKGROUND
- **Recommendation:** WATCH ONLY - lineage and vocabulary; nothing to import.

### Go `testing/synctest`
- **Source:** Go standard library; Go 1.25 release notes.
- **Verified?** read directly - https://pkg.go.dev/testing/synctest and https://go.dev/doc/go1.25 ; confirmed present locally (Go 1.26.8 on vm-1).
- **Key idea:** `synctest.Test` runs a function in a "bubble" with a fake clock starting at midnight UTC 2000-01-01. Time advances only when every goroutine in the bubble is "durably blocked". `synctest.Wait` blocks until that point. Experimental in Go 1.24, generally available in 1.25.
- **What Mini-ZGateway would concretely borrow (testing):** use it for the goroutine-level shell - timers, context deadlines, write-deadline teardown - with real `time` calls and no sleeps.
- **Limits (from the docs):** channel operations, `time.Sleep`, `sync.Cond.Wait` and `WaitGroup.Wait` block durably. **Locking a `sync.Mutex`, network I/O and system calls do not.** So real sockets cannot be used inside a bubble ("Use a fake network implementation"). And the docs promise fake time, not a seeded scheduler: the order in which runnable goroutines execute is still the runtime's, so synctest alone cannot guarantee the spec's bit-identical replay law. (That last inference is mine; the docs do not discuss replay.)
- **Strength:** SECONDARY
- **Recommendation:** ADOPT WITH CHANGE - a second layer under the custom simulator, not a replacement for it.

**What the minimum Commit-0 harness needs.**

1. A `Clock` interface (`Now`, `AfterFunc`/timer) with a virtual implementation: a min-heap of
   events keyed by `(time_ns, sequence)`. The sequence number breaks ties so equal-time events run
   in insertion order.
2. One seeded generator (`math/rand/v2` PCG, confirmed in the local standard library) passed
   explicitly everywhere. No global `rand`, no decision that depends on map iteration order or on
   which `select` case wins.
3. A single-threaded event loop. Admission queue, limiter, coalescer, pool and batcher are state
   machines driven by `step(event) -> actions`; goroutines and sockets live only in a thin shell.
4. A fake backend and connection whose latency, stalls, resets, half-open drops and 1-byte reads
   are drawn from the same generator.
5. A decision trace (accepted, coalesced, shed, timed out, with virtual timestamps) folded into a
   hash. The replay meta-test runs a scenario twice with one seed and requires equal hashes, and
   requires a different seed to give a different hash.
6. The seed and commit printed on every failure.
7. Invariant checks after every event, not only at the end.

---

## Conflicts / tensions noticed

- **CoDel by the book vs CoDel for services.** The spec cites Nichols & Jacobson as the algorithm,
  but only the Facebook timeout form works for open-loop requests. Following the RFC costs an
  unbounded queue at high overload (simulated); following Facebook costs fidelity to the cited
  paper and gives a saw-tooth, not convergence.
- **LIFO vs the detector.** LIFO needs a standing-queue signal that does not depend on dequeue
  order. Folly's minimum-delay detector fails under LIFO (zero sheds in simulation). The
  `lastEmptyTime` signal works but oscillates.
- **LIFO vs per-connection reply order.** RESP replies must return in request order on each client
  connection. LIFO across clients is fine; LIFO within one client's pipeline forces reply
  reordering buffers. Cost of avoiding it: one in-queue request per client connection at a time.
- **Batching vs concurrency limiting vs latency signals.** A batch of 32 on one connection is one
  round trip but 32 in-flight requests. AIMD and gradient controllers read "RTT"; batch wait and
  pipeline position inflate it. Either measure backend time per batch, or accept a biased signal.
- **Coalescing vs fairness accounting.** One backend read serves waiters from several tenants.
  Charge only the tenant who started it and others get free reads; charge everyone and the
  backend's real load is over-counted.
- **Rate limit vs concurrency share.** GCRA caps a tenant's rate in good times; it does not protect
  anyone during a stall. Per-tenant in-flight caps protect during a stall but waste capacity when
  other tenants are idle unless the cap is work-conserving (borrowing).
- **Retry budget vs transparent retry.** A proxy that never retries is simplest and cannot amplify;
  a proxy that retries pre-forward failures hides connection churn from clients. Hedging conflicts
  outright with one backend.
- **Custom simulator vs `synctest`.** The custom loop gives bit-identical replay but tests state
  machines, not the real goroutine wiring. `synctest` tests real wiring but not seeded
  interleavings. Using both doubles the test surface.
- **Per-tenant 3-digit histograms vs memory.** 139 KB each against 20.5 KB at 2 digits.
- **Timer batch window (spec) vs smart batching.** The window is easy to explain and tune; the
  timer-free rule has no knob and no idle-latency cost.

## Variant implications

- **Pushes toward Variant A (Redis RESP):** Redis pipelining economics and smart batching (the
  multiplexing model is native to RESP); Little's Law numbers (clean when one connection carries
  many requests); open-loop generation (RESP is pipelinable, so an asynchronous generator is easy);
  `rand.NewZipf` for key skew; coalescing of GETs.
- **Pushes toward Variant B (Postgres):** the fan-in arithmetic is most dramatic for a
  process-per-connection database; Little's Law becomes exact (one in-flight statement per
  connection, so pool size *is* the concurrency limit); AIMD on pool size is cleaner. Batching and
  LIFO matter less; transaction pinning complicates DRR cost accounting.
- **Only matter for Variant C:** power of two choices, the balls-into-bins fan-out model with
  M > 1, hedged and tied requests, XFetch and any TTL cache, shuffle sharding if tenants become
  unbounded.
- **Variant-neutral:** CoDel timeout rule, Adaptive LIFO, GCRA, DRR, retry budget, HDR histogram,
  coordinated-omission rule, benchmark protocol, metastability recovery invariant, the simulator.

## Steering questions this bucket creates for Nick

1. **Which CoDel?** (a) RFC 8289 control law; (b) Facebook `lastEmptyTime` timeout, N = 100 ms,
   M = 5 ms; (c) folly minimum-delay with 2 x target slough. **Recommend (b)**, plus folly's load
   gauge as a metric.
2. **Reword the CoDel invariant?** The spec says wait "converges to the CoDel target". Options:
   keep the wording and pick a tolerance; or replace with "served wait <= N, queue depth bounded
   independent of overload duration, backend never idle". **Recommend the replacement** - it is
   exact and matches what the algorithm does.
3. **LIFO switch rule.** (a) always LIFO (Bandaid); (b) LIFO when `lastEmptyTime` is older than N;
   (c) LIFO above a depth threshold. **Recommend (b)**, per tenant queue, with a head sweep.
4. **Pool size for the headline numbers: 20 or 32?** At 20, the quiet tenant needs exactly its
   whole fair share during the 200 ms stall (zero margin), and the fan-in ratio is 25x, not "about
   15x". **Recommend 32** for the published scenario, or lower the stall to 150 ms.
5. **Rate limiter.** GCRA, token bucket, or both? **Recommend GCRA** for per-tenant request rate
   and a deposit-style token bucket for the retry budget and connection-creation cap.
6. **Fairness scheduler.** Plain round robin over tenant queues (DRR, quantum 1), or weighted DRR
   with costs (MGET of k keys costs k)? **Recommend unit-cost round robin in Commit 5**, with the
   deficit field present so weights and costs can be added without a redesign.
7. **Adaptive concurrency.** AIMD only, or AIMD plus gradient? **Recommend AIMD only** for v1 with
   a latency threshold as the congestion signal; gradient as a labelled stretch goal.
8. **What does "99/1 Zipfian" mean?** (a) literal: 99% of requests on 1% of keys (two-class hot
   set); (b) Zipf with a stated exponent, reporting the resulting share. **Recommend (b) with
   s = 1.2 over 100,000 keys** (85% on the top 1%, 20% on the top key) and a separate single-hot-key
   scenario for the stampede.
9. **Batch flush rule.** Timer window (200 us-1 ms) or timer-free smart batching? **Recommend
   timer-free**, with `maxBatch` and `maxBytes` caps.
10. **Does the proxy itself ever retry?** (a) never; (b) pre-forward failures only, under a 10%
    budget. **Recommend (b)**, with (a) as the fallback if it complicates error semantics.
11. **Histogram precision.** 2 digits everywhere, or 2 in the proxy and 3 in the load generator?
    **Recommend the split.**
12. **Is `benchstat` allowed as an optional dev tool?** It is not a runtime dependency. **Recommend
    yes**: emit Go benchmark format; also ship a small built-in median and interval report so
    `make bench` works without it.
13. **Simulator architecture.** Custom single-threaded event loop only, `synctest` only, or both?
    **Recommend both**, with the replay law asserted only on the custom loop.

## Verification log

| # | URL / path | Result |
|---|---|---|
| 1 | /root/Software/mini-zgateway/docs/project-source.md | read in full |
| 2 | https://www.rfc-editor.org/rfc/rfc8289.txt | OK, pseudo-code read |
| 3 | https://queue.acm.org/detail.cfm?id=2209336 (Controlling Queue Delay) | 403 via curl and via local renderer (bot challenge) |
| 4 | https://web.archive.org/web/2023id_/https://queue.acm.org/detail.cfm?id=2209336 | fetched, but copy truncated; not relied on |
| 5 | https://queue.acm.org/detail.cfm?id=2839461 (Fail at Scale) | 403 via curl and via local renderer |
| 6 | https://web.archive.org/web/2023id_/https://queue.acm.org/detail.cfm?id=2839461 | OK, CoDel / LIFO / concurrency sections read |
| 7 | https://blog.acolyer.org/2015/11/19/fail-at-scale-controlling-queue-delay/ | OK, read |
| 8 | raw.githubusercontent.com/facebook/folly/main/folly/executors/Codel.h, Codel.cpp | OK, read in full |
| 9 | raw.githubusercontent.com/facebook/folly/main/folly/synchronization/LifoSem.h | OK, header comment read |
| 10 | raw.githubusercontent.com/facebook/folly/main/folly/executors/task_queue/LifoSemMPMCQueue.h | OK, read |
| 11 | raw.githubusercontent.com/facebook/folly/main/folly/executors/CPUThreadPoolExecutor.cpp | OK, skimmed for queue types |
| 12 | https://dropbox.tech/infrastructure/meet-bandaid-the-dropbox-service-proxy | OK, queueing sections read |
| 13 | https://en.wikipedia.org/wiki/Generic_cell_rate_algorithm | OK, read (secondary for I.371) |
| 14 | raw.githubusercontent.com/throttled/throttled/master/rate.go | OK, GCRA function read |
| 15 | raw.githubusercontent.com/golang/time/master/rate/rate.go | OK, `advance` / `reserveN` read |
| 16 | https://en.wikipedia.org/wiki/Token_bucket | fetched, not read |
| 17 | https://www.cs.cmu.edu/~srini/15-744/readings/SV95.pdf (DRR) | 404 |
| 18 | https://www2.cs.duke.edu/courses/spring09/cps214/papers/drr.pdf | OK, but scanned; text garbled |
| 19 | https://en.wikipedia.org/wiki/Deficit_round_robin | OK, pseudo-code read |
| 20 | https://en.wikipedia.org/wiki/Stochastic_fairness_queueing | 404 |
| 21 | https://en.wikipedia.org/wiki/Fair_queuing | fetched, not read closely |
| 22 | https://www.rfc-editor.org/rfc/rfc8290.txt (FQ-CoDel) | OK, keyword passages read |
| 23 | raw.githubusercontent.com/kubernetes/enhancements/.../1040-priority-and-fairness/README.md | OK, shuffle-sharding section read |
| 24 | https://kubernetes.io/docs/concepts/cluster-administration/flow-control/ | fetched, not read |
| 25 | https://www.cse.wustl.edu/~jain/papers/ftp/cong_av.pdf (Chiu & Jain) | downloaded; scan without text layer; unreadable |
| 26 | https://www.cs.princeton.edu/courses/archive/spring11/cos461/docs/chiu-jain.pdf | 404 |
| 27 | https://www.cs.princeton.edu/courses/archive/fall06/cos561/papers/vegas.pdf | OK, congestion-avoidance section read |
| 28 | raw.githubusercontent.com/Netflix/concurrency-limits/main/.../limit/AIMDLimit.java | OK, read |
| 29 | .../limit/VegasLimit.java, GradientLimit.java, Gradient2Limit.java | OK, update rules and defaults read |
| 30 | raw.githubusercontent.com/Netflix/concurrency-limits/main/README.md | OK, read |
| 31 | https://netflixtechblog.medium.com/performance-under-load-3e6fa9a60581 | 403 via curl; OK via local renderer |
| 32 | https://en.wikipedia.org/wiki/Little%27s_law | fetched, not read closely |
| 33 | https://www.eecs.harvard.edu/~michaelm/postscripts/handbook2001.pdf | OK, introduction read |
| 34 | https://en.wikipedia.org/wiki/Balls_into_bins_problem | OK, read |
| 35 | https://en.wikipedia.org/wiki/2-choice_hashing | fetched, not read |
| 36 | websearch "ZGateway ZippyDB proxy Meta engineering" | no relevant results; Meta post not read in this bucket |
| 37 | raw.githubusercontent.com/giltene/wrk2/master/README.md | OK, read |
| 38 | https://www.infoq.com/presentations/latency-response-time/ (Tene talk) | page abstract only |
| 39 | https://www.usenix.org/legacy/event/nsdi06/tech/full_papers/schroeder/schroeder.pdf | OK, principles list read (partly garbled) |
| 40 | raw.githubusercontent.com/HdrHistogram/HdrHistogram/master/README.md | OK, read |
| 41 | raw.githubusercontent.com/HdrHistogram/hdrhistogram-go/master/hdr.go | OK, index and correction functions read |
| 42 | raw.githubusercontent.com/HdrHistogram/HdrHistogram/.../AbstractHistogram.java | fetched, not read |
| 43 | https://arxiv.org/abs/1908.10693 (DDSketch) | abstract only |
| 44 | raw.githubusercontent.com/tdunning/t-digest/main/README.md | OK, skimmed |
| 45 | https://prometheus.io/docs/specs/native_histograms/ | introduction only |
| 46 | https://pkg.go.dev/golang.org/x/perf/cmd/benchstat | OK, read |
| 47 | https://kar.kent.ac.uk/33611/45/p63-kaliber.pdf | OK, abstract / intro / keywords |
| 48 | https://dri.es/files/oopsla07-georges.pdf | OK, abstract / intro / keywords |
| 49 | https://users.cs.northwestern.edu/~robby/courses/322-2013-spring/mytkowicz-wrong-data.pdf | OK, abstract / intro |
| 50 | https://www.apollographql.com/blog/how-we-know-a-router-performance-result-is-real | OK, read in full; exists, dated Oct 7 2026 |
| 51 | https://sigops.org/s/conferences/hotos/2021/papers/hotos21-s11-bronson.pdf | OK, abstract / intro |
| 52 | https://www.usenix.org/system/files/osdi22-huang-lexiang.pdf | OK, abstract / intro |
| 53 | https://sre.google/sre-book/handling-overload/ | OK, throttling and retry sections read |
| 54 | https://sre.google/sre-book/addressing-cascading-failures/ | fetched, not read |
| 55 | raw.githubusercontent.com/twitter/finagle/develop/.../service/RetryBudget.scala | OK, read |
| 56 | https://twitter.github.io/finagle/guide/Clients.html | fetched, not read |
| 57 | https://linkerd.io/2-edge/tasks/configuring-retries/ | 404 |
| 58 | https://cacm.acm.org/research/the-tail-at-scale/ | 403 via curl; OK via local renderer |
| 59 | https://research.google/pubs/the-tail-at-scale/ | fetched, not read |
| 60 | raw.githubusercontent.com/golang/go/master/src/math/rand/zipf.go | OK, read in full |
| 61 | raw.githubusercontent.com/brianfrankcooper/YCSB/.../ZipfianGenerator.java | OK, read |
| 62 | https://cseweb.ucsd.edu/~avattani/papers/cache_stampede.pdf | OK, algorithm listing read |
| 63 | https://mechanical-sympathy.blogspot.com/2011/10/smart-batching.html | OK, read |
| 64 | https://redis.io/docs/latest/develop/using-commands/pipelining/ | OK, read |
| 65 | https://apple.github.io/foundationdb/testing.html | OK, read |
| 66 | raw.githubusercontent.com/tigerbeetle/tigerbeetle/main/docs/internals/vopr.md | OK, read |
| 67 | raw.githubusercontent.com/tigerbeetle/tigerbeetle/main/docs/about/vopr.md | 404 |
| 68 | raw.githubusercontent.com/tigerbeetle/tigerbeetle/main/docs/concepts/safety.md | OK, skimmed |
| 69 | https://antithesis.com/docs/resources/deterministic_simulation_testing/ | OK, opening sections read |
| 70 | raw.githubusercontent.com/tokio-rs/turmoil/main/README.md | OK, read |
| 71 | raw.githubusercontent.com/madsim-rs/madsim/main/README.md | OK, read |
| 72 | https://pkg.go.dev/testing/synctest | OK, read |
| 73 | https://go.dev/doc/go1.25 | OK, synctest note read |
| 74 | https://go.dev/blog/synctest | fetched, not read |
| 75 | local `go doc math/rand/v2`, `go doc testing/synctest` (Go 1.26.8) | OK |
| 76 | raw.githubusercontent.com/golang/sync/master/singleflight/singleflight.go | fetched, not read (outside this bucket) |
| - | Not attempted: ITU-T I.371 text, Azar et al. 1994, McKenney 1990, Demers et al. 1989, HHVM adaptive-LIFO source, Will Wilson's talk video, sled simulation guide, RFC 896 | content from these is labelled *from memory* or `could not verify` |
