-- requires: engine.replies fakepg.faults
-- claim: e2e-kill-before-read: error-contract counts by row = faults injected: one pg.reset fault, one lost-after-read reply.
SELECT run_id || ': schema_version ' || schema_version AS violation FROM runs WHERE schema_version <> 1
UNION ALL
(WITH per_run AS (
    SELECT run_id,
           coalesce(sum(value) FILTER (WHERE component = 'engine' AND metric = 'replies' AND reason = 'lost-after-read'), 0) AS row,
           coalesce(sum(value) FILTER (WHERE component = 'fakepg' AND metric = 'faults' AND reason = 'pg.reset'), 0) AS injected
    FROM final GROUP BY run_id
)
SELECT r.run_id || ': ' || 'row ' || coalesce(p.row, -1) || ', ' || 'injected ' || coalesce(p.injected, -1)
FROM runs r LEFT JOIN per_run p USING (run_id)
WHERE r.scenario = 'e2e-kill-before-read'
  AND (p.run_id IS NULL OR p.row <> 1 OR p.injected <> 1))
UNION ALL
SELECT 'no run of e2e-kill-before-read' WHERE NOT EXISTS (SELECT 1 FROM runs WHERE scenario = 'e2e-kill-before-read');
