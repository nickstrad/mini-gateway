-- requires: fakepg.sessions_peak pool.capacity pool.exhausted
-- claim: pool-hard-cap: 1,000 clients connected at once; in every flush the fake's session peak stays at or under the pool cap, and the cap was reached (checkouts refused at the cap).
SELECT run_id || ': schema_version ' || schema_version AS violation FROM runs WHERE schema_version <> 1
UNION ALL
SELECT m.run_id || ': flush ' || m.flush_seq || ': database sessions peak ' || m.value || ' above the cap ' || c.value
FROM metrics m JOIN metrics c ON c.run_id = m.run_id AND c.flush_seq = m.flush_seq AND c.component = 'pool' AND c.metric = 'capacity'
WHERE m.component = 'fakepg' AND m.metric = 'sessions_peak' AND m.value > c.value
UNION ALL
SELECT run_id || ': the cap was never reached' FROM final WHERE component = 'pool' AND metric = 'exhausted' AND value < 1
UNION ALL
SELECT 'no run of pool-hard-cap' WHERE NOT EXISTS (SELECT 1 FROM runs WHERE scenario = 'pool-hard-cap');
