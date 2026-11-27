-- requires: engine.leases_started engine.leases_ended engine.leases_ended_by engine.resets_started engine.resets_completed engine.resets_failed engine.connections_resetting engine.resets_not_at_release
-- claim: tierr-queue-full: tier R, with the pool core in the binary: leases ended = ended with a reset + closed at release + lent as is + ended into the dirty-connection table = leases started; resets started = resets at release + resets the pool sent elsewhere (tenant switch, after a clean-up) = completed + failed (a reset cut by the stop counts as failed, reason closed); nothing resetting in the final flush; nothing lent as is (every chunk 6 scenario runs lease.reset: always).
SELECT run_id || ': schema_version ' || schema_version AS violation FROM runs WHERE schema_version <> 1
UNION ALL
(WITH per_run AS (
    SELECT run_id,
           coalesce(sum(value) FILTER (WHERE component = 'engine' AND metric = 'leases_started'), 0) AS started_leases,
           coalesce(sum(value) FILTER (WHERE component = 'engine' AND metric = 'leases_ended'), 0) AS ended,
           coalesce(sum(value) FILTER (WHERE component = 'engine' AND metric = 'leases_ended_by' AND outcome = 'reset'), 0) AS by_reset,
           coalesce(sum(value) FILTER (WHERE component = 'engine' AND metric = 'leases_ended_by' AND outcome = 'close'), 0) AS by_close,
           coalesce(sum(value) FILTER (WHERE component = 'engine' AND metric = 'leases_ended_by' AND outcome = 'as-is'), 0) AS as_is,
           coalesce(sum(value) FILTER (WHERE component = 'engine' AND metric = 'leases_ended_by' AND outcome = 'dirty'), 0) AS dirty,
           coalesce(sum(value) FILTER (WHERE component = 'engine' AND metric = 'resets_not_at_release'), 0) AS elsewhere,
           coalesce(sum(value) FILTER (WHERE component = 'engine' AND metric = 'resets_started'), 0) AS started,
           coalesce(sum(value) FILTER (WHERE component = 'engine' AND metric = 'resets_completed'), 0) AS completed,
           coalesce(sum(value) FILTER (WHERE component = 'engine' AND metric = 'resets_failed'), 0) AS failed,
           coalesce(sum(value) FILTER (WHERE component = 'engine' AND metric = 'connections_resetting'), 0) AS resetting
    FROM final GROUP BY run_id
)
SELECT r.run_id || ': ' || 'started_leases ' || coalesce(p.started_leases, -1) || ', ' || 'ended ' || coalesce(p.ended, -1) || ', ' || 'by_reset ' || coalesce(p.by_reset, -1) || ', ' || 'by_close ' || coalesce(p.by_close, -1) || ', ' || 'as_is ' || coalesce(p.as_is, -1) || ', ' || 'started ' || coalesce(p.started, -1) || ', ' || 'completed ' || coalesce(p.completed, -1) || ', ' || 'failed ' || coalesce(p.failed, -1) || ', ' || 'resetting ' || coalesce(p.resetting, -1)
FROM runs r LEFT JOIN per_run p USING (run_id)
WHERE r.scenario = 'tierr-queue-full'
  AND (p.run_id IS NULL OR p.ended <> p.by_reset + p.by_close + p.as_is + p.dirty OR p.ended <> p.started_leases OR p.started <> p.by_reset + p.elsewhere OR p.started <> p.completed + p.failed OR p.resetting <> 0 OR p.as_is <> 0))
UNION ALL
SELECT 'no run of tierr-queue-full' WHERE NOT EXISTS (SELECT 1 FROM runs WHERE scenario = 'tierr-queue-full');
