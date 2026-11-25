-- requires: wire.client_tls_failed wire.database_tls_failed fakepg.handshakes workload.startup_tls_faults
-- claim: wire-client-smuggle: TLS handshake failures by result in the wire snapshot equal the handshake faults injected, and completed handshakes match on both sides.
-- Injected: none.
-- Every run: each injected count is at least 1 and equals the failures of its reason, no other failure of
-- these metrics is non-zero, and no other handshake fault was injected.
SELECT run_id || ': schema_version ' || schema_version AS violation FROM runs WHERE schema_version <> 1
UNION ALL
(WITH injection(component, metric, outcome, reason, failure_metric, failure_reason, planted) AS (
    SELECT * FROM (VALUES ('', '', '', '', '', '', 0)) WHERE false
),
injected AS (
    SELECT r.run_id, i.failure_metric, i.failure_reason,
           coalesce(i.planted, (SELECT sum(f.value) FROM final f WHERE f.run_id = r.run_id AND f.component = i.component
                                AND f.metric = i.metric AND f.outcome = i.outcome AND f.reason = i.reason), 0) AS value
    FROM runs r CROSS JOIN injection i
),
failed AS (
    SELECT run_id, metric AS failure_metric, reason AS failure_reason, sum(value) AS value FROM final
    WHERE component = 'wire' AND metric IN ('client_tls_failed', 'database_tls_failed') AND value <> 0
    GROUP BY run_id, metric, reason
)
SELECT coalesce(i.run_id, f.run_id) || ': wire.' || coalesce(i.failure_metric, f.failure_metric) || ' reason '
       || coalesce(i.failure_reason, f.failure_reason) || ': injected ' || coalesce(i.value, 0) || ', failed ' || coalesce(f.value, 0)
FROM injected i FULL OUTER JOIN failed f
  ON i.run_id = f.run_id AND i.failure_metric = f.failure_metric AND i.failure_reason = f.failure_reason
WHERE coalesce(i.value, 0) <> coalesce(f.value, 0) OR (i.run_id IS NOT NULL AND i.value < 1))
UNION ALL
(WITH injection(component, metric, outcome, reason, failure_metric, failure_reason, planted) AS (
    SELECT * FROM (VALUES ('', '', '', '', '', '', 0)) WHERE false
)
SELECT run_id || ': ' || component || '.' || metric || ' ' || outcome || reason || ' = ' || sum(value) || ', a fault this scenario does not inject'
FROM final x
WHERE value <> 0 AND (
    (component = 'fakepg' AND metric = 'handshakes' AND reason <> 'completed')
    OR (component = 'workload' AND metric = 'startup_tls_faults' AND reason IN ('bad-hello', 'stall-after-request')))
  AND NOT EXISTS (SELECT 1 FROM injection i WHERE i.component = x.component AND i.metric = x.metric
                  AND i.outcome = x.outcome AND i.reason = x.reason)
GROUP BY run_id, component, metric, outcome, reason)
UNION ALL
SELECT run_id || ': wire.client_tls_completed ' || coalesce(wire, 0) || ' != workload.startup_handshakes ok ' || coalesce(sent, 0)
FROM (SELECT run_id,
             sum(value) FILTER (WHERE component = 'wire' AND metric = 'client_tls_completed') AS wire,
             sum(value) FILTER (WHERE component = 'workload' AND metric = 'startup_handshakes' AND outcome = 'ok') AS sent
      FROM final GROUP BY run_id)
WHERE coalesce(wire, 0) <> coalesce(sent, 0)
UNION ALL
SELECT run_id || ': wire.database_tls_completed ' || coalesce(wire, 0) || ' != fakepg.handshakes completed ' || coalesce(fake, 0)
FROM (SELECT run_id,
             sum(value) FILTER (WHERE component = 'wire' AND metric = 'database_tls_completed') AS wire,
             sum(value) FILTER (WHERE component = 'fakepg' AND metric = 'handshakes' AND reason = 'completed') AS fake
      FROM final GROUP BY run_id)
WHERE coalesce(wire, 0) <> coalesce(fake, 0);
