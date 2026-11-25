-- requires: fakepg.sessions_peak fakepg.refused workload.errors_by_code
-- claim: a reconnect burst of 500 clients against max_connections 100 never passed 100 sessions in any flush,
-- was refused with 53300 at least 400 times, and the clients saw exactly the refusals the database counted.
SELECT run_id || ': schema_version ' || schema_version AS violation FROM runs WHERE schema_version <> 1
UNION ALL
SELECT run_id || ': flush ' || flush_seq || ': sessions peak ' || value
FROM metrics WHERE component = 'fakepg' AND metric = 'sessions_peak' AND value > 100
UNION ALL
SELECT run_id || ': refused ' || coalesce(refused, -1) || ', clients saw 53300 ' || coalesce(seen, -1)
FROM (SELECT run_id,
             sum(value) FILTER (WHERE component = 'fakepg' AND metric = 'refused' AND reason = 'too-many') AS refused,
             sum(value) FILTER (WHERE component = 'workload' AND metric = 'errors_by_code' AND reason = '53300') AS seen
      FROM final GROUP BY run_id)
WHERE refused IS NULL OR refused < 400 OR seen IS DISTINCT FROM refused;
