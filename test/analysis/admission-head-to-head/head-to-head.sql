-- The admission head-to-head table (chunk 8): both queue rules and both simulator comparison arms
-- at 1.2x, 2x and 10x of the backend's capacity, one row per (load, arm), from the flushed runs
-- of the scenarios admission-h2h-l120, admission-h2h-l200 and admission-h2h-l1000.
-- Run by scripts/admission-head-to-head.sh over a root that holds those runs. Not an assertion file.
-- Columns: served-wait percentiles are upper bucket bounds of the final queue_wait histogram (ns
-- converted to ms with two decimals); depth is the peak queue depth during the 2 s overload
-- phase; shed and served are counted at the end of the phase (t = 2 s); idle capacity is the
-- share of the phase the single connection was not serving a lease, from served x 5.5 ms
-- (one lease: 5 ms read + reset + network, measured at saturation).
WITH phase AS (
    SELECT r.scenario, r.arm, r.seed,
           sum(value) FILTER (WHERE component = 'admission' AND metric = 'served') AS served,
           sum(value) FILTER (WHERE component = 'admission' AND metric = 'shed') AS shed,
           sum(value) FILTER (WHERE component = 'admission' AND metric = 'shed' AND reason = 'rule') AS shed_rule,
           sum(value) FILTER (WHERE component = 'admission' AND metric = 'queued_peak') AS depth_peak
    FROM metrics m JOIN runs r USING (run_id)
    WHERE r.scenario LIKE 'admission-h2h-l%' AND m.t_ns = 2000000000
    GROUP BY ALL
),
b AS (
    SELECT r.scenario, r.arm, r.seed, h.bucket_hi, sum(h.count) AS n
    FROM final_hist h JOIN runs r USING (run_id)
    WHERE r.scenario LIKE 'admission-h2h-l%' AND h.component = 'admission' AND h.metric = 'queue_wait'
    GROUP BY ALL
),
c AS (
    SELECT *, sum(n) OVER (PARTITION BY scenario, arm, seed ORDER BY bucket_hi) AS cum,
              sum(n) OVER (PARTITION BY scenario, arm, seed) AS total FROM b
),
pct AS (
    SELECT scenario, arm, seed,
           min(bucket_hi) FILTER (WHERE 100 * cum >= 50 * total) AS p50,
           min(bucket_hi) FILTER (WHERE 100 * cum >= 99 * total) AS p99,
           max(bucket_hi) AS pmax
    FROM c GROUP BY ALL
)
SELECT CAST(regexp_extract(p.scenario, 'l([0-9]+)$', 1) AS INTEGER) / 100.0 AS load_x,
       p.arm,
       p.seed,
       round(q.p50 / 1e6, 2) AS wait_p50_ms,
       round(q.p99 / 1e6, 2) AS wait_p99_ms,
       round(q.pmax / 1e6, 2) AS wait_max_ms,
       p.depth_peak,
       p.served,
       p.shed,
       p.shed_rule,
       round(greatest(0, 100 - 100.0 * p.served * 5500000 / 2000000000), 1) AS idle_capacity_pct
FROM phase p JOIN pct q USING (scenario, arm, seed)
ORDER BY load_x, CASE p.arm WHEN 'timeout' THEN 1 WHEN 'control-law' THEN 2 WHEN 'fifo' THEN 3 ELSE 4 END, p.seed;
