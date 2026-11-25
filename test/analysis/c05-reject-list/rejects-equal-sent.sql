-- Chunk 5, scenario classifier-reject-only (tier S): every statement the simulated clients
-- sent was rejected by the classifier. Rows returned are violations; zero rows is a pass.
-- Reads the views of test/analysis/views.sql (chunk 3) over the CSV flush of PLAN.md 7.7.
-- Metric names (proposed for the registry in docs/csv-schema.md, wired at chunk 6):
--   classifier/statements             counter, all Query messages classified
--   classifier/rejects (reason=...)   counter, one row per reject reason
--   scenario/statements_sent          counter, Query messages the simulated clients sent
SELECT run_id, 'schema version' AS check_name, schema_version AS got, 1 AS want
FROM runs WHERE schema_version <> 1
UNION ALL
SELECT r.run_id, 'rejects = statements sent', coalesce(rej.n, -1), coalesce(sent.n, -1)
FROM runs r
LEFT JOIN (SELECT run_id, sum(value) AS n FROM final
           WHERE component = 'classifier' AND metric = 'rejects' GROUP BY run_id) rej USING (run_id)
LEFT JOIN (SELECT run_id, sum(value) AS n FROM final
           WHERE component = 'scenario' AND metric = 'statements_sent' GROUP BY run_id) sent USING (run_id)
WHERE r.scenario = 'classifier-reject-only'
  AND (rej.n IS NULL OR sent.n IS NULL OR rej.n = 0 OR rej.n <> sent.n)
UNION ALL
SELECT r.run_id, 'classified = rejects', coalesce(cls.n, -1), coalesce(rej.n, -1)
FROM runs r
LEFT JOIN (SELECT run_id, sum(value) AS n FROM final
           WHERE component = 'classifier' AND metric = 'statements' GROUP BY run_id) cls USING (run_id)
LEFT JOIN (SELECT run_id, sum(value) AS n FROM final
           WHERE component = 'classifier' AND metric = 'rejects' GROUP BY run_id) rej USING (run_id)
WHERE r.scenario = 'classifier-reject-only'
  AND (cls.n IS NULL OR rej.n IS NULL OR cls.n <> rej.n);
