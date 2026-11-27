-- requires: engine.requests_completed engine.requests_received workload.requests workload.sent
-- claim: e2e-reset-wrong-tag: two components, one query: the proxy received exactly the requests the clients sent, and the requests it completed (relayed to their end marker) are the clients' successes plus the errors the database itself returned.
SELECT run_id || ': schema_version ' || schema_version AS violation FROM runs WHERE schema_version <> 1
UNION ALL
(WITH per_run AS (
    SELECT run_id,
           coalesce(sum(value) FILTER (WHERE component = 'engine' AND metric = 'requests_received'), 0) AS received,
           coalesce(sum(value) FILTER (WHERE component = 'engine' AND metric = 'requests_completed'), 0) AS completed,
           coalesce(sum(value) FILTER (WHERE component = 'workload' AND metric = 'sent'), 0) AS sent,
           coalesce(sum(value) FILTER (WHERE component = 'workload' AND metric = 'requests' AND outcome = 'ok'), 0) AS ok,
           coalesce(sum(value) FILTER (WHERE component = 'workload' AND metric = 'errors_by_code' AND reason NOT IN ('53M01','53M02','53M03','53M04','53M05','0A000','08006','08007','57014','25P03','28000','28P01','53300','08P01')), 0) AS db_errors
    FROM final GROUP BY run_id
)
SELECT r.run_id || ': ' || 'received ' || coalesce(p.received, -1) || ', ' || 'sent ' || coalesce(p.sent, -1) || ', ' || 'completed ' || coalesce(p.completed, -1) || ', ' || 'ok ' || coalesce(p.ok, -1) || ', ' || 'db_errors ' || coalesce(p.db_errors, -1)
FROM runs r LEFT JOIN per_run p USING (run_id)
WHERE r.scenario = 'e2e-reset-wrong-tag'
  AND (p.run_id IS NULL OR p.received <> p.sent OR p.completed <> p.ok + p.db_errors))
UNION ALL
SELECT 'no run of e2e-reset-wrong-tag' WHERE NOT EXISTS (SELECT 1 FROM runs WHERE scenario = 'e2e-reset-wrong-tag');
