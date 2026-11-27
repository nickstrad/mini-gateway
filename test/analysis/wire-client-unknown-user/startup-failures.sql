-- requires: wire.client_startups_failed wire.database_startups_failed fakepg.faults workload.startup_tls_faults fakepg.handshakes fakepg.tls_answered
-- claim: wire-client-unknown-user: startup failures by reason in the wire snapshot equal the startup faults injected by kind.
-- Injected: planted by the scenario (one client of an unknown user connects): 1 -> wire.client_startups_failed{reason=auth-failed}.
-- Every run: each injected count is at least 1 and equals the failures of its reason, no other failure of
-- these metrics is non-zero, and no fault of another kind was injected.
SELECT run_id || ': schema_version ' || schema_version AS violation FROM runs WHERE schema_version <> 1
UNION ALL
(WITH injection(component, metric, outcome, reason, failure_metric, failure_reason, planted) AS (
    VALUES
        ('', '', '', '', 'client_startups_failed', 'auth-failed', 1)
),
injected AS (
    SELECT r.run_id, i.failure_metric, i.failure_reason,
           coalesce(i.planted, (SELECT sum(f.value) FROM final f WHERE f.run_id = r.run_id AND f.component = i.component
                                AND f.metric = i.metric AND f.outcome = i.outcome AND f.reason = i.reason), 0) AS value
    FROM runs r CROSS JOIN injection i
),
failed AS (
    SELECT run_id, metric AS failure_metric, reason AS failure_reason, sum(value) AS value FROM final
    WHERE component = 'wire' AND metric IN ('client_startups_failed', 'database_startups_failed') AND value <> 0
    GROUP BY run_id, metric, reason
)
SELECT coalesce(i.run_id, f.run_id) || ': wire.' || coalesce(i.failure_metric, f.failure_metric) || ' reason '
       || coalesce(i.failure_reason, f.failure_reason) || ': injected ' || coalesce(i.value, 0) || ', failed ' || coalesce(f.value, 0)
FROM injected i FULL OUTER JOIN failed f
  ON i.run_id = f.run_id AND i.failure_metric = f.failure_metric AND i.failure_reason = f.failure_reason
WHERE coalesce(i.value, 0) <> coalesce(f.value, 0) OR (i.run_id IS NOT NULL AND i.value < 1))
UNION ALL
(WITH injection(component, metric, outcome, reason, failure_metric, failure_reason, planted) AS (
    VALUES
        ('', '', '', '', 'client_startups_failed', 'auth-failed', 1)
)
SELECT run_id || ': ' || component || '.' || metric || ' ' || outcome || reason || ' = ' || sum(value) || ', a fault this scenario does not inject'
FROM final x
WHERE value <> 0 AND (
    (component = 'fakepg' AND metric = 'faults')
    OR (component = 'fakepg' AND metric = 'handshakes' AND reason <> 'completed')
    OR (component = 'fakepg' AND metric = 'tls_answered' AND outcome = 'N')
    OR (component = 'workload' AND metric = 'startup_tls_faults' AND reason <> 'none'))
  AND NOT EXISTS (SELECT 1 FROM injection i WHERE i.component = x.component AND i.metric = x.metric
                  AND i.outcome = x.outcome AND i.reason = x.reason)
GROUP BY run_id, component, metric, outcome, reason);
