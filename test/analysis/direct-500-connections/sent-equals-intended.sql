-- requires: workload.intended workload.sent workload.requests
-- claim: every intended request was sent and completed ok: intended = sent = ok = 2,500.
SELECT run_id || ': schema_version ' || schema_version AS violation FROM runs WHERE schema_version <> 1
UNION ALL
SELECT run_id || ': intended ' || intended || ', sent ' || sent || ', ok ' || ok
FROM (SELECT run_id,
             sum(value) FILTER (WHERE component = 'workload' AND metric = 'intended') AS intended,
             sum(value) FILTER (WHERE component = 'workload' AND metric = 'sent') AS sent,
             sum(value) FILTER (WHERE component = 'workload' AND metric = 'requests' AND outcome = 'ok') AS ok
      FROM final GROUP BY run_id)
WHERE intended IS DISTINCT FROM 2500 OR sent IS DISTINCT FROM intended OR ok IS DISTINCT FROM intended;
