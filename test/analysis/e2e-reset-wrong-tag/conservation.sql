-- requires: engine.requests_received engine.requests_accepted engine.requests_completed engine.requests_failed engine.requests_shed engine.requests_rejected engine.requests_queued engine.requests_in_flight
-- claim: e2e-reset-wrong-tag: conservation per instance from the exported counters: received = accepted + rejected, accepted = completed + failed + shed, and queued and in flight are 0 in the final flush; something was received.
SELECT run_id || ': schema_version ' || schema_version AS violation FROM runs WHERE schema_version <> 1
UNION ALL
(WITH per_run AS (
    SELECT run_id,
           coalesce(sum(value) FILTER (WHERE component = 'engine' AND metric = 'requests_received'), 0) AS received,
           coalesce(sum(value) FILTER (WHERE component = 'engine' AND metric = 'requests_accepted'), 0) AS accepted,
           coalesce(sum(value) FILTER (WHERE component = 'engine' AND metric = 'requests_completed'), 0) AS completed,
           coalesce(sum(value) FILTER (WHERE component = 'engine' AND metric = 'requests_failed'), 0) AS failed,
           coalesce(sum(value) FILTER (WHERE component = 'engine' AND metric = 'requests_shed'), 0) AS shed,
           coalesce(sum(value) FILTER (WHERE component = 'engine' AND metric = 'requests_rejected'), 0) AS rejected,
           coalesce(sum(value) FILTER (WHERE component = 'engine' AND metric = 'requests_queued'), 0) AS queued,
           coalesce(sum(value) FILTER (WHERE component = 'engine' AND metric = 'requests_in_flight'), 0) AS in_flight
    FROM final GROUP BY run_id
)
SELECT r.run_id || ': ' || 'received ' || coalesce(p.received, -1) || ', ' || 'accepted ' || coalesce(p.accepted, -1) || ', ' || 'rejected ' || coalesce(p.rejected, -1) || ', ' || 'completed ' || coalesce(p.completed, -1) || ', ' || 'failed ' || coalesce(p.failed, -1) || ', ' || 'shed ' || coalesce(p.shed, -1) || ', ' || 'queued ' || coalesce(p.queued, -1) || ', ' || 'in_flight ' || coalesce(p.in_flight, -1)
FROM runs r LEFT JOIN per_run p USING (run_id)
WHERE r.scenario = 'e2e-reset-wrong-tag'
  AND (p.run_id IS NULL OR p.received <> p.accepted + p.rejected OR p.accepted <> p.completed + p.failed + p.shed OR p.queued <> 0 OR p.in_flight <> 0 OR p.received < 1))
UNION ALL
SELECT 'no run of e2e-reset-wrong-tag' WHERE NOT EXISTS (SELECT 1 FROM runs WHERE scenario = 'e2e-reset-wrong-tag');
