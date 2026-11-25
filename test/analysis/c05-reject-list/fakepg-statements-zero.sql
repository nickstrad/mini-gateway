-- Chunk 5, scenario classifier-reject-only (tier S): nothing reached the fake Postgres.
-- Rows returned are violations; zero rows is a pass. A run with no fakepg row at all is a
-- violation too (the counters-must-move rule: the fake must have exported the metric).
-- Metric (proposed): fakepg/statements_started, counter.
SELECT run_id, 'schema version' AS check_name, schema_version AS got, 1 AS want
FROM runs WHERE schema_version <> 1
UNION ALL
SELECT r.run_id, 'fake statements = 0', coalesce(pg.n, -1), 0
FROM runs r
LEFT JOIN (SELECT run_id, sum(value) AS n FROM final
           WHERE component = 'fakepg' AND metric = 'statements_started' GROUP BY run_id) pg USING (run_id)
WHERE r.scenario = 'classifier-reject-only'
  AND (pg.n IS NULL OR pg.n <> 0)
UNION ALL
SELECT NULL, 'no run of classifier-reject-only', 0, 1
WHERE NOT EXISTS (SELECT 1 FROM runs WHERE scenario = 'classifier-reject-only');
