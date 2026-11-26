-- requires: engine.database_closes engine.replies fakepg.faults
-- claim: e2e-silent-db-query: a database that accepts a query and never answers ends at the statement deadline with lost-after-read (08006) for the safe read.
SELECT run_id || ': schema_version ' || schema_version AS violation FROM runs WHERE schema_version <> 1
UNION ALL
(WITH per_run AS (
    SELECT run_id,
           coalesce(sum(value) FILTER (WHERE component = 'engine' AND metric = 'database_closes' AND reason = 'statement-deadline'), 0) AS closes,
           coalesce(sum(value) FILTER (WHERE component = 'engine' AND metric = 'replies' AND reason = 'lost-after-read'), 0) AS row,
           coalesce(sum(value) FILTER (WHERE component = 'fakepg' AND metric = 'faults' AND reason = 'pg.silent-query'), 0) AS injected
    FROM final GROUP BY run_id
)
SELECT r.run_id || ': ' || 'closes ' || coalesce(p.closes, -1) || ', ' || 'row ' || coalesce(p.row, -1) || ', ' || 'injected ' || coalesce(p.injected, -1)
FROM runs r LEFT JOIN per_run p USING (run_id)
WHERE r.scenario = 'e2e-silent-db-query'
  AND (p.run_id IS NULL OR p.closes <> 1 OR p.row <> 1 OR p.injected <> 1))
UNION ALL
SELECT 'no run of e2e-silent-db-query' WHERE NOT EXISTS (SELECT 1 FROM runs WHERE scenario = 'e2e-silent-db-query');
