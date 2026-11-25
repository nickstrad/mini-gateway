-- requires: fakepg.statements workload.requests workload.intended
-- claim: 1,000 identical reads sent directly were 1,000 database reads (no sharing without a proxy).
SELECT run_id || ': schema_version ' || schema_version AS violation FROM runs WHERE schema_version <> 1
UNION ALL
SELECT run_id || ': database reads ' || coalesce(reads, -1) || ', intended ' || coalesce(intended, -1) || ', ok ' || coalesce(ok, -1)
FROM (SELECT run_id,
             sum(value) FILTER (WHERE component = 'fakepg' AND metric = 'statements' AND mode = 'read') AS reads,
             sum(value) FILTER (WHERE component = 'workload' AND metric = 'intended') AS intended,
             sum(value) FILTER (WHERE component = 'workload' AND metric = 'requests' AND outcome = 'ok') AS ok
      FROM final GROUP BY run_id)
WHERE reads IS DISTINCT FROM 1000 OR intended IS DISTINCT FROM 1000 OR ok IS DISTINCT FROM 1000;
