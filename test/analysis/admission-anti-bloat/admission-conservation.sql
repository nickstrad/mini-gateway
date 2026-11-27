-- requires: admission.pushes admission.served admission.removed admission.shed admission.queued engine.requests_shed
-- claim: admission-anti-bloat: every push ends in exactly one of served, shed (by reason) or removed, nothing is queued in the final flush, every 53M01 the engine answered is a shed the admission queues counted, and something was pushed.
SELECT run_id || ': schema_version ' || schema_version AS violation FROM runs WHERE schema_version <> 1
UNION ALL
(WITH per_run AS (
    SELECT run_id,
           coalesce(sum(value) FILTER (WHERE component = 'admission' AND metric = 'pushes'), 0) AS pushes,
           coalesce(sum(value) FILTER (WHERE component = 'admission' AND metric = 'served'), 0) AS served,
           coalesce(sum(value) FILTER (WHERE component = 'admission' AND metric = 'removed'), 0) AS removed,
           coalesce(sum(value) FILTER (WHERE component = 'admission' AND metric = 'shed'), 0) AS shed,
           coalesce(sum(value) FILTER (WHERE component = 'admission' AND metric = 'queued'), 0) AS queued,
           coalesce(sum(value) FILTER (WHERE component = 'engine' AND metric = 'requests_shed'), 0) AS engine_shed
    FROM final GROUP BY run_id
)
SELECT r.run_id || ': pushes ' || coalesce(p.pushes, -1) || ', served ' || coalesce(p.served, -1) || ', removed ' || coalesce(p.removed, -1)
       || ', shed ' || coalesce(p.shed, -1) || ', queued ' || coalesce(p.queued, -1) || ', engine shed ' || coalesce(p.engine_shed, -1)
FROM runs r LEFT JOIN per_run p USING (run_id)
WHERE r.scenario = 'admission-anti-bloat'
  AND (p.run_id IS NULL OR p.pushes <> p.served + p.removed + p.shed + p.queued OR p.queued <> 0 OR p.shed <> p.engine_shed OR p.pushes < 1))
UNION ALL
SELECT 'no run of admission-anti-bloat' WHERE NOT EXISTS (SELECT 1 FROM runs WHERE scenario = 'admission-anti-bloat');
