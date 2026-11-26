-- requires: engine.clients_dropped engine.relay_buffer_high_water engine.relay_buffer_capacity engine.relay_pauses
-- claim: e2e-slow-reader: a client that stops reading is dropped at the write deadline; the relay buffer never went past its capacity and reading the database was paused.
SELECT run_id || ': schema_version ' || schema_version AS violation FROM runs WHERE schema_version <> 1
UNION ALL
(WITH per_run AS (
    SELECT run_id,
           coalesce(sum(value) FILTER (WHERE component = 'engine' AND metric = 'clients_dropped'), 0) AS dropped,
           coalesce(max(value) FILTER (WHERE component = 'engine' AND metric = 'relay_buffer_high_water'), 0) AS high,
           coalesce(sum(value) FILTER (WHERE component = 'engine' AND metric = 'relay_buffer_capacity'), 0) AS cap,
           coalesce(sum(value) FILTER (WHERE component = 'engine' AND metric = 'relay_pauses'), 0) AS pauses
    FROM final GROUP BY run_id
)
SELECT r.run_id || ': ' || 'dropped ' || coalesce(p.dropped, -1) || ', ' || 'high ' || coalesce(p.high, -1) || ', ' || 'cap ' || coalesce(p.cap, -1) || ', ' || 'pauses ' || coalesce(p.pauses, -1)
FROM runs r LEFT JOIN per_run p USING (run_id)
WHERE r.scenario = 'e2e-slow-reader'
  AND (p.run_id IS NULL OR p.dropped <> 1 OR p.high > p.cap OR p.pauses < 1))
UNION ALL
SELECT 'no run of e2e-slow-reader' WHERE NOT EXISTS (SELECT 1 FROM runs WHERE scenario = 'e2e-slow-reader');
