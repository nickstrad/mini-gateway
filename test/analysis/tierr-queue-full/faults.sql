-- requires: engine.replies
-- claim: tierr-queue-full: tier R: one connection and a queue of two: of five simultaneous requests exactly two were shed.
SELECT run_id || ': schema_version ' || schema_version AS violation FROM runs WHERE schema_version <> 1
UNION ALL
(WITH per_run AS (
    SELECT run_id,
           coalesce(sum(value) FILTER (WHERE component = 'engine' AND metric = 'replies' AND reason = 'shed'), 0) AS shed
    FROM final GROUP BY run_id
)
SELECT r.run_id || ': ' || 'shed ' || coalesce(p.shed, -1)
FROM runs r LEFT JOIN per_run p USING (run_id)
WHERE r.scenario = 'tierr-queue-full'
  AND (p.run_id IS NULL OR p.shed <> 2))
UNION ALL
SELECT 'no run of tierr-queue-full' WHERE NOT EXISTS (SELECT 1 FROM runs WHERE scenario = 'tierr-queue-full');
