-- requires: engine.resets_failed engine.database_closes fakepg.faults
-- claim: e2e-kill-after-reply: a database reset after the reply costs the proxy one connection, seen during the reset or while idle, and no client error.
SELECT run_id || ': schema_version ' || schema_version AS violation FROM runs WHERE schema_version <> 1
UNION ALL
(WITH per_run AS (
    SELECT run_id,
           coalesce(sum(value) FILTER (WHERE component = 'engine' AND metric = 'resets_failed' AND reason = 'connection-lost'), 0) AS lost_reset,
           coalesce(sum(value) FILTER (WHERE component = 'engine' AND metric = 'database_closes' AND reason = 'connection-lost'), 0) AS lost_idle,
           coalesce(sum(value) FILTER (WHERE component = 'fakepg' AND metric = 'faults' AND reason = 'pg.reset'), 0) AS injected
    FROM final GROUP BY run_id
)
SELECT r.run_id || ': ' || 'lost_reset ' || coalesce(p.lost_reset, -1) || ', ' || 'lost_idle ' || coalesce(p.lost_idle, -1) || ', ' || 'injected ' || coalesce(p.injected, -1)
FROM runs r LEFT JOIN per_run p USING (run_id)
WHERE r.scenario = 'e2e-kill-after-reply'
  AND (p.run_id IS NULL OR p.lost_reset + p.lost_idle <> 1 OR p.injected <> 1))
UNION ALL
SELECT 'no run of e2e-kill-after-reply' WHERE NOT EXISTS (SELECT 1 FROM runs WHERE scenario = 'e2e-kill-after-reply');
