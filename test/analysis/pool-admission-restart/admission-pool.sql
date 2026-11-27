-- requires: admission.pushes admission.served admission.removed admission.shed admission.queued engine.requests_shed engine.breaker_flushed pool.breaker_transitions engine.replies
-- claim: pool-admission-restart: with the binary's default timeout rule in front of the pool core, every push ends in exactly one of served, shed (by reason) or removed; the engine's sheds equal the admission queues' sheds, and the breaker's refusals of queued requests are exactly the admission queues' sheds with reason breaker, and there was at least one; the breaker opened and closed again and requests were answered 53M03 while it was open.
SELECT run_id || ': schema_version ' || schema_version AS violation FROM runs WHERE schema_version <> 1
UNION ALL
(WITH per_run AS (
    SELECT run_id,
           coalesce(sum(value) FILTER (WHERE component = 'admission' AND metric = 'pushes'), 0) AS pushes,
           coalesce(sum(value) FILTER (WHERE component = 'admission' AND metric = 'served'), 0) AS served,
           coalesce(sum(value) FILTER (WHERE component = 'admission' AND metric = 'removed'), 0) AS removed,
           coalesce(sum(value) FILTER (WHERE component = 'admission' AND metric = 'shed'), 0) AS shed,
           coalesce(sum(value) FILTER (WHERE component = 'admission' AND metric = 'shed' AND reason = 'breaker'), 0) AS shed_breaker,
           coalesce(sum(value) FILTER (WHERE component = 'admission' AND metric = 'queued'), 0) AS queued,
           coalesce(sum(value) FILTER (WHERE component = 'engine' AND metric = 'requests_shed'), 0) AS engine_shed,
           coalesce(sum(value) FILTER (WHERE component = 'engine' AND metric = 'breaker_flushed'), 0) AS flushed,
           sum(value) FILTER (WHERE component = 'pool' AND metric = 'breaker_transitions' AND mode = 'closed-open') AS opened,
           sum(value) FILTER (WHERE component = 'pool' AND metric = 'breaker_transitions' AND mode = 'half-open-closed') AS closed,
           sum(value) FILTER (WHERE component = 'engine' AND metric = 'replies' AND reason = 'breaker-open') AS refused
    FROM final GROUP BY run_id
)
SELECT r.run_id || ': pushes ' || coalesce(p.pushes, -1) || ', served ' || coalesce(p.served, -1) || ', removed ' || coalesce(p.removed, -1)
       || ', shed ' || coalesce(p.shed, -1) || ' (breaker ' || coalesce(p.shed_breaker, -1) || '), queued ' || coalesce(p.queued, -1)
       || ', engine shed ' || coalesce(p.engine_shed, -1) || ', breaker flushed ' || coalesce(p.flushed, -1)
       || ', breaker opened ' || coalesce(p.opened, -1) || ' closed ' || coalesce(p.closed, -1) || ', 53M03 replies ' || coalesce(p.refused, -1)
FROM runs r LEFT JOIN per_run p USING (run_id)
WHERE r.scenario = 'pool-admission-restart'
  AND (p.run_id IS NULL OR p.pushes <> p.served + p.removed + p.shed + p.queued OR p.queued <> 0 OR p.shed <> p.engine_shed
       OR p.shed_breaker <> p.flushed OR p.shed_breaker < 1 OR p.pushes < 1 OR p.opened IS NULL OR p.opened < 1 OR p.closed < 1 OR p.refused < 1))
UNION ALL
SELECT 'no run of pool-admission-restart' WHERE NOT EXISTS (SELECT 1 FROM runs WHERE scenario = 'pool-admission-restart');
