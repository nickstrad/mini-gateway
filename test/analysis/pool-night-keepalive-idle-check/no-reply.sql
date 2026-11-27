-- requires: pool.backend_no_reply engine.backend_no_reply engine.requests_completed
-- claim: pool-night-keepalive-idle-check: the 5 a.m. scenario with keepalive, idle timeout and the check on: backend_no_reply stays 0 in every flush (pool and engine) while requests complete.
SELECT run_id || ': schema_version ' || schema_version AS violation FROM runs WHERE schema_version <> 1
UNION ALL
SELECT run_id || ': flush ' || flush_seq || ': ' || component || '.backend_no_reply ' || value
FROM metrics WHERE metric = 'backend_no_reply' AND value <> 0
UNION ALL
SELECT run_id || ': nothing completed' FROM final WHERE component = 'engine' AND metric = 'requests_completed' AND value < 1
UNION ALL
SELECT 'no run of pool-night-keepalive-idle-check' WHERE NOT EXISTS (SELECT 1 FROM runs WHERE scenario = 'pool-night-keepalive-idle-check');
