-- requires: fakepg.sessions_peak fakepg.conns_open_peak workload.clients
-- claim: 500 clients connecting directly gave the fake Postgres 500 sessions and 500 sockets at peak.
-- Each row returned is a violation.
SELECT run_id || ': schema_version ' || schema_version AS violation FROM runs WHERE schema_version <> 1
UNION ALL
SELECT run_id || ': clients ' || coalesce(clients, -1) || ', sessions peak ' || coalesce(sessions_peak, -1)
       || ', sockets peak ' || coalesce(conns_peak, -1)
FROM (SELECT run_id,
             max(value) FILTER (WHERE component = 'workload' AND metric = 'clients') AS clients,
             max(value) FILTER (WHERE component = 'fakepg' AND metric = 'sessions_peak') AS sessions_peak,
             max(value) FILTER (WHERE component = 'fakepg' AND metric = 'conns_open_peak') AS conns_peak
      FROM final GROUP BY run_id)
WHERE clients IS DISTINCT FROM 500 OR sessions_peak IS DISTINCT FROM 500 OR conns_peak IS DISTINCT FROM 500;
