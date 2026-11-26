-- requires: fakepg.statements_started fakepg.startups_completed classifier.rejects
-- claim: classifier-reject-only: no refused statement reached the database. The fake Postgres
-- started 0 statements, although its sessions started (at least 1 startup completed) and the
-- classifier refused at least one statement.
SELECT run_id || ': schema_version ' || schema_version AS violation FROM runs WHERE schema_version <> 1
UNION ALL
(WITH per_run AS (
    SELECT run_id,
           coalesce(sum(value) FILTER (WHERE component = 'fakepg' AND metric = 'statements_started'), 0) AS started,
           coalesce(sum(value) FILTER (WHERE component = 'fakepg' AND metric = 'startups_completed'), 0) AS startups,
           coalesce(sum(value) FILTER (WHERE component = 'classifier' AND metric = 'rejects'), 0) AS rejects
    FROM final GROUP BY run_id
)
SELECT r.run_id || ': fake statements ' || coalesce(p.started, -1) || ', startups ' || coalesce(p.startups, -1) || ', rejects ' || coalesce(p.rejects, -1)
FROM runs r LEFT JOIN per_run p USING (run_id)
WHERE r.scenario = 'classifier-reject-only'
  AND (p.run_id IS NULL OR p.started <> 0 OR p.startups < 1 OR p.rejects < 1))
UNION ALL
SELECT 'no run of classifier-reject-only' WHERE NOT EXISTS (SELECT 1 FROM runs WHERE scenario = 'classifier-reject-only');
