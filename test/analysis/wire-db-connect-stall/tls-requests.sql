-- requires: wire.client_prestartup_requests wire.database_tls_answers workload.startup_tls_requests fakepg.tls_answered wire.database_tls_requests workload.startup_tls_answered
-- claim: wire-db-connect-stall: TLS requests by kind and answer equal the requests sent. Client side: the requests the proxy
-- answered S or N equal the TLS requests the workload sent (at least 1), each answer equals the answers the
-- workload received, and no GSS or cancel request was counted (none is sent). Database side: the proxy's TLS
-- requests equal the fake's answers, and each answer the proxy counted equals the answer the fake gave.
SELECT run_id || ': schema_version ' || schema_version AS violation FROM runs WHERE schema_version <> 1
UNION ALL
(WITH answers(outcome) AS (VALUES ('S'), ('N')),
per_run AS (
    SELECT run_id,
           coalesce(sum(value) FILTER (WHERE component = 'wire' AND metric = 'client_prestartup_requests' AND mode = 'tls' AND outcome IN ('S', 'N')), 0) AS answered,
           coalesce(sum(value) FILTER (WHERE component = 'workload' AND metric = 'startup_tls_requests'), 0) AS sent,
           coalesce(sum(value) FILTER (WHERE component = 'wire' AND metric = 'client_prestartup_requests' AND mode <> 'tls'), 0) AS other_kinds,
           coalesce(sum(value) FILTER (WHERE component = 'wire' AND metric = 'database_tls_requests'), 0) AS db_requests,
           coalesce(sum(value) FILTER (WHERE component = 'fakepg' AND metric = 'tls_answered'), 0) AS fake_answers
    FROM final GROUP BY run_id
),
by_answer AS (
    SELECT r.run_id, a.outcome,
           coalesce((SELECT sum(value) FROM final f WHERE f.run_id = r.run_id AND f.component = 'wire' AND f.metric = 'client_prestartup_requests' AND f.mode = 'tls' AND f.outcome = a.outcome), 0) AS proxy,
           coalesce((SELECT sum(value) FROM final f WHERE f.run_id = r.run_id AND f.component = 'workload' AND f.metric = 'startup_tls_answered' AND f.outcome = a.outcome), 0) AS client,
           coalesce((SELECT sum(value) FROM final f WHERE f.run_id = r.run_id AND f.component = 'wire' AND f.metric = 'database_tls_answers' AND f.outcome = a.outcome), 0) AS db,
           coalesce((SELECT sum(value) FROM final f WHERE f.run_id = r.run_id AND f.component = 'fakepg' AND f.metric = 'tls_answered' AND f.outcome = a.outcome), 0) AS fake
    FROM runs r CROSS JOIN answers a
)
SELECT run_id || ': TLS requests answered by the proxy ' || answered || ', sent by the workload ' || sent
FROM per_run WHERE answered <> sent OR sent < 1
UNION ALL
SELECT run_id || ': ' || other_kinds || ' GSS or cancel requests counted, none sent' FROM per_run WHERE other_kinds <> 0
UNION ALL
SELECT run_id || ': database TLS requests ' || db_requests || ', fake answers ' || fake_answers FROM per_run WHERE db_requests <> fake_answers
UNION ALL
SELECT run_id || ': answer ' || outcome || ': proxy ' || proxy || ', workload ' || client FROM by_answer WHERE proxy <> client
UNION ALL
SELECT run_id || ': database answer ' || outcome || ': proxy ' || db || ', fakepg ' || fake FROM by_answer WHERE db <> fake);
