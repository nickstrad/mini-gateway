-- requires: workload.requests workload.intended
-- claim: fair-noisy-neighbour: a join of the runs of one seed: the quiet tenant (tenant1) completed exactly as many requests with the flooder at ten times capacity (arm fair) as alone (arm alone), every one it intended, with no error; with one shared FIFO queue (arm fifo, where it ran) it completed fewer or failed some.
SELECT run_id || ': schema_version ' || schema_version AS violation FROM runs WHERE schema_version <> 1
UNION ALL
(WITH q AS (
    SELECT r.seed, r.arm,
           coalesce(sum(f.value) FILTER (WHERE f.metric = 'requests' AND f.outcome = 'ok'), 0) AS ok,
           coalesce(sum(f.value) FILTER (WHERE f.metric = 'requests' AND f.outcome <> 'ok'), 0) AS failed,
           coalesce(sum(f.value) FILTER (WHERE f.metric = 'intended'), 0) AS intended
    FROM runs r JOIN final f USING (run_id)
    WHERE r.scenario = 'fair-noisy-neighbour' AND f.component = 'workload' AND f.tenant = 'tenant1'
    GROUP BY r.seed, r.arm
)
SELECT 'seed ' || a.seed || ': quiet tenant alone ' || a.ok || ' ok of ' || a.intended || ' (' || a.failed || ' failed), with the flood '
       || coalesce(f.ok, -1) || ' ok (' || coalesce(f.failed, -1) || ' failed), behind one shared queue ' || coalesce(s.ok, -1) || ' ok'
FROM q a LEFT JOIN q f ON f.seed = a.seed AND f.arm = 'fair' LEFT JOIN q s ON s.seed = a.seed AND s.arm = 'fifo'
WHERE a.arm = 'alone'
  AND (a.ok < 1 OR a.ok <> a.intended OR a.failed <> 0 OR f.ok IS NULL OR f.ok <> a.ok OR f.failed <> 0
       OR (s.ok IS NOT NULL AND s.ok >= a.ok AND s.failed = 0)))
UNION ALL
SELECT 'no alone run of fair-noisy-neighbour' WHERE NOT EXISTS (SELECT 1 FROM runs WHERE scenario = 'fair-noisy-neighbour' AND arm = 'alone');
