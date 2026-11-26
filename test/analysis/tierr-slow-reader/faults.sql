-- requires: engine.clients_dropped engine.relay_buffer_high_water engine.relay_buffer_capacity
-- claim: tierr-slow-reader: tier R: the slow reader was dropped and the relay buffer stayed within its capacity.
SELECT run_id || ': schema_version ' || schema_version AS violation FROM runs WHERE schema_version <> 1
UNION ALL
(WITH per_run AS (
    SELECT run_id,
           coalesce(sum(value) FILTER (WHERE component = 'engine' AND metric = 'clients_dropped'), 0) AS dropped,
           coalesce(max(value) FILTER (WHERE component = 'engine' AND metric = 'relay_buffer_high_water'), 0) AS high,
           coalesce(sum(value) FILTER (WHERE component = 'engine' AND metric = 'relay_buffer_capacity'), 0) AS cap
    FROM final GROUP BY run_id
)
SELECT r.run_id || ': ' || 'dropped ' || coalesce(p.dropped, -1) || ', ' || 'high ' || coalesce(p.high, -1) || ', ' || 'cap ' || coalesce(p.cap, -1)
FROM runs r LEFT JOIN per_run p USING (run_id)
WHERE r.scenario = 'tierr-slow-reader'
  AND (p.run_id IS NULL OR p.dropped <> 1 OR p.high > p.cap))
UNION ALL
SELECT 'no run of tierr-slow-reader' WHERE NOT EXISTS (SELECT 1 FROM runs WHERE scenario = 'tierr-slow-reader');
