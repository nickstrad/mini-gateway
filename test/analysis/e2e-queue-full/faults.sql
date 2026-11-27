-- requires: engine.replies engine.requests_shed
-- claim: e2e-queue-full: more clients than connections plus queue: exactly the three over the bound were shed (53M01).
SELECT run_id || ': schema_version ' || schema_version AS violation FROM runs WHERE schema_version <> 1
UNION ALL
(WITH per_run AS (
    SELECT run_id,
           coalesce(sum(value) FILTER (WHERE component = 'engine' AND metric = 'replies' AND reason = 'shed'), 0) AS row,
           coalesce(sum(value) FILTER (WHERE component = 'engine' AND metric = 'requests_shed'), 0) AS shed
    FROM final GROUP BY run_id
)
SELECT r.run_id || ': ' || 'row ' || coalesce(p.row, -1) || ', ' || 'shed ' || coalesce(p.shed, -1)
FROM runs r LEFT JOIN per_run p USING (run_id)
WHERE r.scenario = 'e2e-queue-full'
  AND (p.run_id IS NULL OR p.row <> 3 OR p.shed <> 3))
UNION ALL
SELECT 'no run of e2e-queue-full' WHERE NOT EXISTS (SELECT 1 FROM runs WHERE scenario = 'e2e-queue-full');
