-- requires: fakepg.statements_started engine.requests_completed engine.resets_started
-- claim: e2e-reject-list: the fake Postgres started only the forwarded statements and the resets: nothing refused reached it.
SELECT run_id || ': schema_version ' || schema_version AS violation FROM runs WHERE schema_version <> 1
UNION ALL
(WITH per_run AS (
    SELECT run_id,
           coalesce(sum(value) FILTER (WHERE component = 'fakepg' AND metric = 'statements_started'), 0) AS fake,
           coalesce(sum(value) FILTER (WHERE component = 'engine' AND metric = 'requests_completed'), 0) AS completed,
           coalesce(sum(value) FILTER (WHERE component = 'engine' AND metric = 'resets_started'), 0) AS resets
    FROM final GROUP BY run_id
)
SELECT r.run_id || ': ' || 'fake ' || coalesce(p.fake, -1) || ', ' || 'completed ' || coalesce(p.completed, -1) || ', ' || 'resets ' || coalesce(p.resets, -1)
FROM runs r LEFT JOIN per_run p USING (run_id)
WHERE r.scenario = 'e2e-reject-list'
  AND (p.run_id IS NULL OR p.fake <> p.completed + p.resets))
UNION ALL
SELECT 'no run of e2e-reject-list' WHERE NOT EXISTS (SELECT 1 FROM runs WHERE scenario = 'e2e-reject-list');
