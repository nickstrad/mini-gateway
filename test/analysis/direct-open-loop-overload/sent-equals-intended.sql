-- requires: workload.intended workload.sent workload.not_yet_sent
-- claim: under open-loop overload the workload sent what it intended: in the final flush sent = intended = 800,
-- and in every flush intended = sent + not yet sent (nothing dropped, nothing failed before send).
SELECT run_id || ': schema_version ' || schema_version AS violation FROM runs WHERE schema_version <> 1
UNION ALL
SELECT run_id || ': final intended ' || intended || ', sent ' || sent
FROM (SELECT run_id,
             sum(value) FILTER (WHERE component = 'workload' AND metric = 'intended') AS intended,
             sum(value) FILTER (WHERE component = 'workload' AND metric = 'sent') AS sent
      FROM final GROUP BY run_id)
WHERE intended IS DISTINCT FROM 800 OR sent IS DISTINCT FROM intended
UNION ALL
SELECT run_id || ': flush ' || flush_seq || ': intended ' || intended || ' <> sent ' || sent || ' + not yet sent ' || backlog
FROM (SELECT run_id, flush_seq,
             sum(value) FILTER (WHERE component = 'workload' AND metric = 'intended') AS intended,
             sum(value) FILTER (WHERE component = 'workload' AND metric = 'sent') AS sent,
             sum(value) FILTER (WHERE component = 'workload' AND metric = 'not_yet_sent') AS backlog
      FROM metrics GROUP BY run_id, flush_seq)
WHERE intended IS DISTINCT FROM sent + backlog;
