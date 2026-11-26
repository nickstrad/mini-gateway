-- requires: engine.resets_failed engine.database_closes
-- claim: e2e-reset-wrong-tag: the one tampered reset reply fails the reset with reason wrong-tag and closes its connection.
SELECT run_id || ': schema_version ' || schema_version AS violation FROM runs WHERE schema_version <> 1
UNION ALL
(WITH per_run AS (
    SELECT run_id,
           coalesce(sum(value) FILTER (WHERE component = 'engine' AND metric = 'resets_failed' AND reason = 'wrong-tag'), 0) AS failed,
           coalesce(sum(value) FILTER (WHERE component = 'engine' AND metric = 'resets_failed'), 0) AS all_failed,
           coalesce(sum(value) FILTER (WHERE component = 'engine' AND metric = 'database_closes' AND reason = 'reset-failed'), 0) AS closes
    FROM final GROUP BY run_id
)
SELECT r.run_id || ': ' || 'failed ' || coalesce(p.failed, -1) || ', ' || 'all_failed ' || coalesce(p.all_failed, -1) || ', ' || 'closes ' || coalesce(p.closes, -1)
FROM runs r LEFT JOIN per_run p USING (run_id)
WHERE r.scenario = 'e2e-reset-wrong-tag'
  AND (p.run_id IS NULL OR p.failed <> 1 OR p.all_failed <> 1 OR p.closes <> 1))
UNION ALL
SELECT 'no run of e2e-reset-wrong-tag' WHERE NOT EXISTS (SELECT 1 FROM runs WHERE scenario = 'e2e-reset-wrong-tag');
