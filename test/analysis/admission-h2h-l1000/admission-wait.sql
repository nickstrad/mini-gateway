-- requires: admission.queue_wait admission.rule
-- claim: admission-h2h-l1000: queue-wait percentiles from the bucket rows: under the timeout rule no served request waited the interval (100 ms) or longer, so every non-empty bucket starts below it and p99 is below it; every run served something.
SELECT run_id || ': schema_version ' || schema_version AS violation FROM runs WHERE schema_version <> 1
UNION ALL
(WITH mode AS (
    SELECT run_id, max(mode) FILTER (WHERE value = 1) AS rule FROM final WHERE component = 'admission' AND metric = 'rule' GROUP BY run_id
), b AS (
    SELECT run_id, bucket_lo, bucket_hi, sum(count) AS n FROM final_hist
    WHERE component = 'admission' AND metric = 'queue_wait' GROUP BY ALL
), c AS (
    SELECT *, sum(n) OVER (PARTITION BY run_id ORDER BY bucket_hi) AS cum, sum(n) OVER (PARTITION BY run_id) AS total FROM b
), per_run AS (
    SELECT run_id, max(bucket_lo) AS max_lo, min(bucket_hi) FILTER (WHERE 100 * cum >= 99 * total) AS p99, max(total) AS served FROM c GROUP BY run_id
)
SELECT r.run_id || ': rule ' || coalesce(m.rule, '?') || ', served ' || coalesce(p.served, 0) || ', p99 upper ' || coalesce(p.p99, -1) || ' ns, largest bucket start ' || coalesce(p.max_lo, -1) || ' ns'
FROM runs r LEFT JOIN per_run p USING (run_id) LEFT JOIN mode m USING (run_id)
WHERE r.scenario = 'admission-h2h-l1000'
  AND (p.run_id IS NULL OR p.served < 1 OR (m.rule = 'timeout' AND (p.max_lo >= 100000000 OR p.p99 > 100000000))))
UNION ALL
SELECT 'no run of admission-h2h-l1000' WHERE NOT EXISTS (SELECT 1 FROM runs WHERE scenario = 'admission-h2h-l1000');
