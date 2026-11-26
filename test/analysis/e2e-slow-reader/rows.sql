-- requires: engine.replies workload.requests
-- claim: e2e-slow-reader: every proxy-made error a client saw is counted under its error-contract row, and every counted row for a request reached a client: shed = 53M01, unsupported-feature + unsupported-extended = 0A000, lost-after-read = 08006, lost-after-write = 08007.
SELECT run_id || ': schema_version ' || schema_version AS violation FROM runs WHERE schema_version <> 1
UNION ALL
(WITH per_run AS (
    SELECT run_id,
           coalesce(sum(value) FILTER (WHERE component = 'engine' AND metric = 'replies' AND reason = 'shed'), 0) AS shed,
           coalesce(sum(value) FILTER (WHERE component = 'workload' AND metric = 'errors_by_code' AND reason = '53M01'), 0) AS c53m01,
           coalesce(sum(value) FILTER (WHERE component = 'engine' AND metric = 'replies' AND reason IN ('unsupported-feature', 'unsupported-extended')), 0) AS unsupported,
           coalesce(sum(value) FILTER (WHERE component = 'workload' AND metric = 'errors_by_code' AND reason = '0A000'), 0) AS c0a000,
           coalesce(sum(value) FILTER (WHERE component = 'engine' AND metric = 'replies' AND reason = 'lost-after-read'), 0) AS after_read,
           coalesce(sum(value) FILTER (WHERE component = 'workload' AND metric = 'errors_by_code' AND reason = '08006'), 0) AS c08006,
           coalesce(sum(value) FILTER (WHERE component = 'engine' AND metric = 'replies' AND reason = 'lost-after-write'), 0) AS after_write,
           coalesce(sum(value) FILTER (WHERE component = 'workload' AND metric = 'errors_by_code' AND reason = '08007'), 0) AS c08007
    FROM final GROUP BY run_id
)
SELECT r.run_id || ': ' || 'shed ' || coalesce(p.shed, -1) || ', ' || 'c53m01 ' || coalesce(p.c53m01, -1) || ', ' || 'unsupported ' || coalesce(p.unsupported, -1) || ', ' || 'c0a000 ' || coalesce(p.c0a000, -1) || ', ' || 'after_read ' || coalesce(p.after_read, -1) || ', ' || 'c08006 ' || coalesce(p.c08006, -1) || ', ' || 'after_write ' || coalesce(p.after_write, -1) || ', ' || 'c08007 ' || coalesce(p.c08007, -1)
FROM runs r LEFT JOIN per_run p USING (run_id)
WHERE r.scenario = 'e2e-slow-reader'
  AND (p.run_id IS NULL OR p.shed <> p.c53m01 OR p.unsupported <> p.c0a000 OR p.after_read <> p.c08006 OR p.after_write <> p.c08007))
UNION ALL
SELECT 'no run of e2e-slow-reader' WHERE NOT EXISTS (SELECT 1 FROM runs WHERE scenario = 'e2e-slow-reader');
