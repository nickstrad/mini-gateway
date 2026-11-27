-- requires: pool.dials pool.creation_rate_per_s pool.rate_limited
-- claim: pool-cold-start: dials per flush interval, from differences between consecutive flushes, never pass the creation rate times the interval plus one; and the limit was hit.
SELECT run_id || ': schema_version ' || schema_version AS violation FROM runs WHERE schema_version <> 1
UNION ALL
(WITH d AS (
    SELECT m.run_id, m.flush_seq, m.t_ns, m.value AS dials,
           lag(m.value) OVER (PARTITION BY m.run_id ORDER BY m.flush_seq) AS prev,
           lag(m.t_ns) OVER (PARTITION BY m.run_id ORDER BY m.flush_seq) AS prev_t
    FROM metrics m JOIN runs r USING (run_id)
    WHERE r.scenario = 'pool-cold-start' AND m.component = 'pool' AND m.metric = 'dials'
), rate AS (
    SELECT run_id, max(value) AS per_s FROM metrics WHERE component = 'pool' AND metric = 'creation_rate_per_s' GROUP BY run_id
)
SELECT d.run_id || ': flush ' || d.flush_seq || ': ' || (d.dials - d.prev) || ' dials in ' || ((d.t_ns - d.prev_t) / 1000000) || ' ms at ' || r.per_s || ' per second'
FROM d JOIN rate r USING (run_id)
WHERE d.prev IS NOT NULL AND (d.dials - d.prev) > r.per_s * (d.t_ns - d.prev_t) // 1000000000 + 1)
UNION ALL
SELECT run_id || ': never rate limited' FROM final WHERE component = 'pool' AND metric = 'rate_limited' AND value < 1
UNION ALL
SELECT 'no run of pool-cold-start' WHERE NOT EXISTS (SELECT 1 FROM runs WHERE scenario = 'pool-cold-start');
