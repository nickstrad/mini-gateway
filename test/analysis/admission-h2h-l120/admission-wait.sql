-- requires: admission.queue_wait admission.rule admission.shed
-- claim: admission-h2h-l120: queue-wait percentiles from the bucket rows: under the timeout rule no served request waited the interval (100 ms) or longer, so every non-empty bucket starts below it and p99 is below it; under the control law the median served wait returns to the target (5 ms) at this moderate overload; both rules shed by the rule, not only by overflow; every run served something.
SELECT run_id || ': schema_version ' || schema_version AS violation FROM runs WHERE schema_version <> 1
UNION ALL
(WITH mode AS (
    SELECT run_id, max(mode) FILTER (WHERE value = 1) AS rule FROM final WHERE component = 'admission' AND metric = 'rule' GROUP BY run_id
), sheds AS (
    SELECT run_id, coalesce(sum(value) FILTER (WHERE component = 'admission' AND metric = 'shed' AND reason = 'rule'), 0) AS shed_rule
    FROM final GROUP BY run_id
), b AS (
    SELECT run_id, bucket_lo, bucket_hi, sum(count) AS n FROM final_hist
    WHERE component = 'admission' AND metric = 'queue_wait' GROUP BY ALL
), c AS (
    SELECT *, sum(n) OVER (PARTITION BY run_id ORDER BY bucket_hi) AS cum, sum(n) OVER (PARTITION BY run_id) AS total FROM b
), per_run AS (
    SELECT run_id, max(bucket_lo) AS max_lo, min(bucket_hi) FILTER (WHERE 100 * cum >= 99 * total) AS p99,
           min(bucket_hi) FILTER (WHERE 100 * cum >= 50 * total) AS p50, max(total) AS served FROM c GROUP BY run_id
)
SELECT r.run_id || ': rule ' || coalesce(m.rule, '?') || ', served ' || coalesce(p.served, 0) || ', p50 upper ' || coalesce(p.p50, -1)
       || ' ns, p99 upper ' || coalesce(p.p99, -1) || ' ns, largest bucket start ' || coalesce(p.max_lo, -1) || ' ns, shed by rule ' || coalesce(s.shed_rule, -1)
FROM runs r LEFT JOIN per_run p USING (run_id) LEFT JOIN mode m USING (run_id) LEFT JOIN sheds s USING (run_id)
WHERE r.scenario = 'admission-h2h-l120'
  AND (p.run_id IS NULL OR p.served < 1
       OR (m.rule = 'timeout' AND (p.max_lo >= 100000000 OR p.p99 > 100000000))
       OR (m.rule = 'control-law' AND p.p50 > 5000000)
       OR (m.rule IN ('timeout', 'control-law') AND coalesce(s.shed_rule, 0) < 1)))
UNION ALL
SELECT 'no run of admission-h2h-l120' WHERE NOT EXISTS (SELECT 1 FROM runs WHERE scenario = 'admission-h2h-l120');
