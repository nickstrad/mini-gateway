-- requires: fakepg.sessions_peak pool.capacity engine.clients_accepted
-- claim: pool-storm: the storm against its baseline, as a join of the two arms of one seed: through the proxy the database sessions stay at or under the pool cap in every flush, while the same clients connecting directly (arm direct) opened more sessions than the cap.
SELECT run_id || ': schema_version ' || schema_version AS violation FROM runs WHERE schema_version <> 1
UNION ALL
(WITH peak AS (
    SELECT r.seed, r.arm, max(m.value) AS sessions
    FROM metrics m JOIN runs r USING (run_id)
    WHERE r.scenario = 'pool-storm' AND m.component = 'fakepg' AND m.metric = 'sessions_peak' GROUP BY r.seed, r.arm
), cap AS (
    SELECT r.seed, max(m.value) AS cap FROM metrics m JOIN runs r USING (run_id)
    WHERE r.scenario = 'pool-storm' AND r.arm = 'base' AND m.component = 'pool' AND m.metric = 'capacity' GROUP BY r.seed
)
SELECT 'seed ' || c.seed || ': proxy arm sessions peak ' || coalesce(p.sessions, -1) || ', direct arm ' || coalesce(d.sessions, -1) || ', cap ' || c.cap
FROM cap c LEFT JOIN peak p ON p.seed = c.seed AND p.arm = 'base' LEFT JOIN peak d ON d.seed = c.seed AND d.arm = 'direct'
WHERE p.sessions IS NULL OR d.sessions IS NULL OR p.sessions > c.cap OR d.sessions <= c.cap)
UNION ALL
SELECT 'no run of pool-storm' WHERE NOT EXISTS (SELECT 1 FROM runs WHERE scenario = 'pool-storm');
