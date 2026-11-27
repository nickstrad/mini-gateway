-- requires: engine.database_connect_failures engine.replies fakepg.faults
-- claim: e2e-silent-db-startup: a database that accepts and never answers startup ends at the connect deadline; the queued request is shed (53M01) at the queue deadline.
SELECT run_id || ': schema_version ' || schema_version AS violation FROM runs WHERE schema_version <> 1
UNION ALL
(WITH per_run AS (
    SELECT run_id,
           coalesce(sum(value) FILTER (WHERE component = 'engine' AND metric = 'database_connect_failures' AND reason = 'deadline'), 0) AS deadlines,
           coalesce(sum(value) FILTER (WHERE component = 'engine' AND metric = 'replies' AND reason = 'shed'), 0) AS shed,
           coalesce(sum(value) FILTER (WHERE component = 'fakepg' AND metric = 'faults' AND reason = 'pg.silent-startup'), 0) AS injected
    FROM final GROUP BY run_id
)
SELECT r.run_id || ': ' || 'deadlines ' || coalesce(p.deadlines, -1) || ', ' || 'shed ' || coalesce(p.shed, -1) || ', ' || 'injected ' || coalesce(p.injected, -1)
FROM runs r LEFT JOIN per_run p USING (run_id)
WHERE r.scenario = 'e2e-silent-db-startup'
  AND (p.run_id IS NULL OR p.deadlines < 1 OR p.shed <> 1 OR p.injected <> 1))
UNION ALL
SELECT 'no run of e2e-silent-db-startup' WHERE NOT EXISTS (SELECT 1 FROM runs WHERE scenario = 'e2e-silent-db-startup');
