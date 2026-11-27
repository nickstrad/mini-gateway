-- requires: fakepg.statements workload.requests
-- claim: coalesce-hot-key-strict: a burst of 1,000 identical reads costs 1 or 2 database reads through the coalescer and 1,000 with it off (the ablation arm of the same seed); flights started + hits = the shareable reads the classifier saw; the waiters-per-flight histogram counts every flight that ended and its total lies between the bucket bounds of the waiters answered; the final flush shows nothing waiting.
SELECT run_id || ': schema_version ' || schema_version AS violation FROM runs WHERE schema_version <> 1
UNION ALL
(WITH arms AS (
    SELECT r.seed, r.arm,
           sum(f.value) FILTER (WHERE f.component = 'fakepg' AND f.metric = 'statements' AND f.mode = 'read') AS reads
    FROM runs r JOIN final f USING (run_id)
    WHERE r.scenario = 'coalesce-hot-key-strict' AND r.arm IN ('base', 'ablation')
    GROUP BY r.seed, r.arm
)
SELECT 'seed ' || b.seed || ': database reads ' || coalesce(b.reads, -1) || ' through the coalescer, ' || coalesce(a.reads, -1) || ' with it off'
FROM arms b LEFT JOIN arms a ON a.seed = b.seed AND a.arm = 'ablation'
WHERE b.arm = 'base' AND (b.reads IS NULL OR b.reads < 1 OR b.reads > 2 OR a.reads IS DISTINCT FROM 1000))
UNION ALL
(WITH c AS (
    SELECT r.run_id,
           sum(value) FILTER (WHERE component = 'coalescer' AND metric = 'flights_started') AS flights,
           sum(value) FILTER (WHERE component = 'coalescer' AND metric = 'hits') AS hits,
           sum(value) FILTER (WHERE component = 'coalescer' AND metric = 'offered') AS offered,
           sum(value) FILTER (WHERE component = 'classifier' AND metric = 'shareable') AS shareable,
           sum(value) FILTER (WHERE component = 'coalescer' AND metric = 'flight_ends' AND outcome IN ('completed', 'failed')) AS ended,
           sum(value) FILTER (WHERE component = 'coalescer' AND metric = 'waiter_ends' AND outcome IN ('reply', 'shared-error')) AS answered,
           sum(value) FILTER (WHERE component = 'coalescer' AND metric IN ('waiting', 'flights', 'table_size', 'stored_bytes')) AS left_over,
           sum(value) FILTER (WHERE component = 'engine' AND metric = 'requests_coalesced') AS coalesced
    FROM runs r JOIN final f USING (run_id) WHERE r.scenario = 'coalesce-hot-key-strict' AND r.arm = 'base' GROUP BY r.run_id
), h AS (
    SELECT run_id, sum(count) AS n, sum(count * bucket_lo) AS lo, sum(count * bucket_hi) AS hi
    FROM final_hist WHERE component = 'coalescer' AND metric = 'waiters_per_flight' GROUP BY run_id
)
SELECT c.run_id || ': flights ' || coalesce(flights, -1) || ' + hits ' || coalesce(hits, -1) || ' vs shareable ' || coalesce(shareable, -1)
       || ' offered ' || coalesce(offered, -1) || '; histogram ' || coalesce(h.n, -1) || ' flights in [' || coalesce(h.lo, -1) || ', '
       || coalesce(h.hi, -1) || ') for ' || coalesce(ended, -1) || ' ended flights and ' || coalesce(answered, -1) || ' answered waiters; left over '
       || coalesce(left_over, -1) || ', coalesced ' || coalesce(coalesced, -1)
FROM c LEFT JOIN h USING (run_id)
WHERE flights + hits IS DISTINCT FROM shareable OR offered IS DISTINCT FROM shareable OR shareable IS DISTINCT FROM 1000
   OR h.n IS DISTINCT FROM ended OR answered < h.lo OR answered > h.hi OR left_over <> 0 OR coalesced <> 0)
UNION ALL
SELECT 'no base run of coalesce-hot-key-strict' WHERE NOT EXISTS (SELECT 1 FROM runs WHERE scenario = 'coalesce-hot-key-strict' AND arm = 'base');
