-- requires: engine.database_closes
-- claim: e2e-client-vanish: the three database connections whose client vanished mid-statement (inside and outside a transaction) or mid-transaction were closed, never reused.
SELECT run_id || ': schema_version ' || schema_version AS violation FROM runs WHERE schema_version <> 1
UNION ALL
(WITH per_run AS (
    SELECT run_id,
           coalesce(sum(value) FILTER (WHERE component = 'engine' AND metric = 'database_closes' AND reason = 'client-vanished'), 0) AS closes
    FROM final GROUP BY run_id
)
SELECT r.run_id || ': ' || 'closes ' || coalesce(p.closes, -1)
FROM runs r LEFT JOIN per_run p USING (run_id)
WHERE r.scenario = 'e2e-client-vanish'
  AND (p.run_id IS NULL OR p.closes <> 3))
UNION ALL
SELECT 'no run of e2e-client-vanish' WHERE NOT EXISTS (SELECT 1 FROM runs WHERE scenario = 'e2e-client-vanish');
