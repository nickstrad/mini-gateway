-- requires: engine.requests_completed pool.breaker_transitions engine.replies
-- claim: pool-db-restart: the breaker opened and closed again, requests were answered 53M03 (breaker-open) while it was open, and completed-per-interval recovered after the restart: flush intervals that end after the restart window (the fault restarts the database at 2 s for 3 s) completed requests.
SELECT run_id || ': schema_version ' || schema_version AS violation FROM runs WHERE schema_version <> 1
UNION ALL
(WITH c AS (
    SELECT m.run_id, m.t_ns, m.value - lag(m.value) OVER (PARTITION BY m.run_id ORDER BY m.flush_seq) AS done
    FROM metrics m JOIN runs r USING (run_id)
    WHERE r.scenario = 'pool-db-restart' AND m.component = 'engine' AND m.metric = 'requests_completed'
)
SELECT run_id || ': no request completed in any flush interval after the restart window (5 s)'
FROM (SELECT run_id, coalesce(sum(done) FILTER (WHERE t_ns > 6000000000), 0) AS after FROM c GROUP BY run_id)
WHERE after < 1)
UNION ALL
SELECT run_id || ': breaker transitions ' || coalesce(opened, -1) || ' opened, ' || coalesce(closed, -1) || ' closed, 53M03 replies ' || coalesce(refused, -1)
FROM (SELECT run_id,
             sum(value) FILTER (WHERE component = 'pool' AND metric = 'breaker_transitions' AND mode = 'closed-open') AS opened,
             sum(value) FILTER (WHERE component = 'pool' AND metric = 'breaker_transitions' AND mode = 'half-open-closed') AS closed,
             sum(value) FILTER (WHERE component = 'engine' AND metric = 'replies' AND reason = 'breaker-open') AS refused
      FROM final GROUP BY run_id)
WHERE opened IS NULL OR opened < 1 OR closed < 1 OR refused < 1
UNION ALL
SELECT 'no run of pool-db-restart' WHERE NOT EXISTS (SELECT 1 FROM runs WHERE scenario = 'pool-db-restart');
